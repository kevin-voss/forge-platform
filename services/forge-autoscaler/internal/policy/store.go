package policy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Typed store errors.
var (
	ErrNotFound            = errors.New("ScalingPolicyNotFound")
	ErrAlreadyExists       = errors.New("ScalingPolicyAlreadyExists")
	ErrConflict            = errors.New("resource_version_conflict")
	ErrIdempotencyConflict = errors.New("idempotency_key_conflict")
)

// Store persists ScalingPolicy envelopes with optimistic concurrency.
type Store struct {
	Pool *pgxpool.Pool
	Hub  *Hub
}

// Create inserts a ScalingPolicy. When idempotencyKey is non-empty, replays prior responses.
func (s *Store) Create(ctx context.Context, project, env, name string, spec ScalingPolicySpec, idempotencyKey, rawBody string) (Envelope, int, error) {
	project, env, name = trim(project), trim(env), trim(name)
	if project == "" || env == "" || name == "" {
		return Envelope{}, 0, fmt.Errorf("project, environment, and name are required")
	}
	if err := validateSpec(spec); err != nil {
		return Envelope{}, 0, err
	}
	if spec.Schedules == nil {
		spec.Schedules = []Schedule{}
	}

	if idempotencyKey != "" {
		if err := validateIdempotencyKey(idempotencyKey); err != nil {
			return Envelope{}, 0, err
		}
		bodyHash := hashBody(rawBody)
		if status, body, ok, err := s.lookupIdempotency(ctx, idempotencyKey); err != nil {
			return Envelope{}, 0, err
		} else if ok {
			storedHash, _ := s.idempotencyHash(ctx, idempotencyKey)
			if storedHash != bodyHash {
				return Envelope{}, 0, ErrIdempotencyConflict
			}
			var envEnvelope Envelope
			if err := json.Unmarshal(body, &envEnvelope); err != nil {
				return Envelope{}, 0, err
			}
			return envEnvelope, status, nil
		}
	}

	id := "sp_" + newID()
	status := DefaultStatus(1)
	specRaw, err := marshalSpec(spec)
	if err != nil {
		return Envelope{}, 0, err
	}
	statusRaw, err := marshalStatus(status)
	if err != nil {
		return Envelope{}, 0, err
	}

	_, err = s.Pool.Exec(ctx, `
INSERT INTO scaling_policies (
  id, name, project, environment, generation, resource_version, spec_json, status_json
) VALUES ($1,$2,$3,$4,1,nextval('scaling_policy_rv_seq'),$5::jsonb,$6::jsonb)`,
		id, name, project, env, string(specRaw), string(statusRaw),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Envelope{}, 0, ErrAlreadyExists
		}
		return Envelope{}, 0, err
	}

	row, err := s.Get(ctx, project, env, name)
	if err != nil {
		return Envelope{}, 0, err
	}
	envelope := row.ToEnvelope()
	if s.Hub != nil {
		if err := s.Hub.Publish(ctx, EventAdded, envelope); err != nil {
			return Envelope{}, 0, err
		}
	}
	if idempotencyKey != "" {
		if err := s.saveIdempotency(ctx, idempotencyKey, hashBody(rawBody), 201, envelope); err != nil {
			return Envelope{}, 0, err
		}
	}
	return envelope, 201, nil
}

// Get loads one policy by scope+name.
func (s *Store) Get(ctx context.Context, project, env, name string) (Row, error) {
	row, err := s.scan(ctx, `
SELECT id, name, project, environment, generation, resource_version, spec_json, status_json, created_at, updated_at
FROM scaling_policies
WHERE project=$1 AND environment=$2 AND name=$3`,
		trim(project), trim(env), trim(name))
	if errors.Is(err, pgx.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	return row, err
}

// List lists policies in an environment.
func (s *Store) List(ctx context.Context, project, env string) ([]Row, error) {
	rows, err := s.Pool.Query(ctx, `
SELECT id, name, project, environment, generation, resource_version, spec_json, status_json, created_at, updated_at
FROM scaling_policies
WHERE project=$1 AND environment=$2
ORDER BY name`, trim(project), trim(env))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAll returns every ScalingPolicy (for the evaluation loop).
func (s *Store) ListAll(ctx context.Context) ([]Row, error) {
	rows, err := s.Pool.Query(ctx, `
SELECT id, name, project, environment, generation, resource_version, spec_json, status_json, created_at, updated_at
FROM scaling_policies
ORDER BY project, environment, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReplaceSpec replaces the spec when expectedRV matches; bumps generation.
func (s *Store) ReplaceSpec(ctx context.Context, project, env, name string, expectedRV int64, spec ScalingPolicySpec) (Envelope, error) {
	if err := validateSpec(spec); err != nil {
		return Envelope{}, err
	}
	if spec.Schedules == nil {
		spec.Schedules = []Schedule{}
	}
	specRaw, err := marshalSpec(spec)
	if err != nil {
		return Envelope{}, err
	}

	tag, err := s.Pool.Exec(ctx, `
UPDATE scaling_policies
SET spec_json=$5::jsonb,
    generation = generation + 1,
    resource_version = nextval('scaling_policy_rv_seq'),
    updated_at = now()
WHERE project=$1 AND environment=$2 AND name=$3 AND resource_version=$4`,
		trim(project), trim(env), trim(name), expectedRV, string(specRaw),
	)
	if err != nil {
		return Envelope{}, err
	}
	if tag.RowsAffected() == 0 {
		current, getErr := s.Get(ctx, project, env, name)
		if errors.Is(getErr, ErrNotFound) {
			return Envelope{}, ErrNotFound
		}
		if getErr != nil {
			return Envelope{}, getErr
		}
		return Envelope{}, fmt.Errorf("%w: expected %d current %d", ErrConflict, expectedRV, current.ResourceVersion)
	}

	row, err := s.Get(ctx, project, env, name)
	if err != nil {
		return Envelope{}, err
	}
	envelope := row.ToEnvelope()
	if s.Hub != nil {
		if err := s.Hub.Publish(ctx, EventModified, envelope); err != nil {
			return Envelope{}, err
		}
	}
	return envelope, nil
}

// PatchSpec merges provided fields into the existing spec (generation bump).
func (s *Store) PatchSpec(ctx context.Context, project, env, name string, expectedRV int64, patch ScalingPolicySpec, patchRaw map[string]json.RawMessage) (Envelope, error) {
	current, err := s.Get(ctx, project, env, name)
	if err != nil {
		return Envelope{}, err
	}
	if current.ResourceVersion != expectedRV {
		return Envelope{}, fmt.Errorf("%w: expected %d current %d", ErrConflict, expectedRV, current.ResourceVersion)
	}
	merged := current.Spec
	if _, ok := patchRaw["targetRef"]; ok {
		merged.TargetRef = patch.TargetRef
	}
	if _, ok := patchRaw["minReplicas"]; ok {
		merged.MinReplicas = patch.MinReplicas
	}
	if _, ok := patchRaw["maxReplicas"]; ok {
		merged.MaxReplicas = patch.MaxReplicas
	}
	if _, ok := patchRaw["metrics"]; ok {
		merged.Metrics = patch.Metrics
	}
	if _, ok := patchRaw["behavior"]; ok {
		merged.Behavior = patch.Behavior
	}
	if _, ok := patchRaw["schedules"]; ok {
		merged.Schedules = patch.Schedules
	}
	return s.ReplaceSpec(ctx, project, env, name, expectedRV, merged)
}

// ReplaceStatus replaces status_json when expectedRV matches (no generation bump).
func (s *Store) ReplaceStatus(ctx context.Context, project, env, name string, expectedRV int64, status ScalingPolicyStatus) (Envelope, error) {
	statusRaw, err := marshalStatus(status)
	if err != nil {
		return Envelope{}, err
	}
	tag, err := s.Pool.Exec(ctx, `
UPDATE scaling_policies
SET status_json=$5::jsonb,
    resource_version = nextval('scaling_policy_rv_seq'),
    updated_at = now()
WHERE project=$1 AND environment=$2 AND name=$3 AND resource_version=$4`,
		trim(project), trim(env), trim(name), expectedRV, string(statusRaw),
	)
	if err != nil {
		return Envelope{}, err
	}
	if tag.RowsAffected() == 0 {
		current, getErr := s.Get(ctx, project, env, name)
		if errors.Is(getErr, ErrNotFound) {
			return Envelope{}, ErrNotFound
		}
		if getErr != nil {
			return Envelope{}, getErr
		}
		return Envelope{}, fmt.Errorf("%w: expected %d current %d", ErrConflict, expectedRV, current.ResourceVersion)
	}
	row, err := s.Get(ctx, project, env, name)
	if err != nil {
		return Envelope{}, err
	}
	envelope := row.ToEnvelope()
	if s.Hub != nil {
		if err := s.Hub.Publish(ctx, EventStatusModified, envelope); err != nil {
			return Envelope{}, err
		}
	}
	return envelope, nil
}

// Delete removes a ScalingPolicy.
func (s *Store) Delete(ctx context.Context, project, env, name string) error {
	row, err := s.Get(ctx, project, env, name)
	if err != nil {
		return err
	}
	var deleteRV int64
	err = s.Pool.QueryRow(ctx, `SELECT nextval('scaling_policy_rv_seq')`).Scan(&deleteRV)
	if err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx, `
DELETE FROM scaling_policies
WHERE project=$1 AND environment=$2 AND name=$3`,
		trim(project), trim(env), trim(name))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	deleted := row.ToEnvelope()
	deleted.Metadata.ResourceVersion = FormatRV(deleteRV)
	if s.Hub != nil {
		if err := s.Hub.Publish(ctx, EventDeleted, deleted); err != nil {
			return err
		}
	}
	return nil
}

// CurrentResourceVersion returns the current RV or 0 when not found.
func (s *Store) CurrentResourceVersion(ctx context.Context, project, env, name string) (int64, error) {
	row, err := s.Get(ctx, project, env, name)
	if errors.Is(err, ErrNotFound) {
		return 0, ErrNotFound
	}
	return row.ResourceVersion, err
}

func (s *Store) scan(ctx context.Context, q string, args ...any) (Row, error) {
	return scanQuerier(s.Pool.QueryRow(ctx, q, args...))
}

type scannable interface {
	Scan(dest ...any) error
}

func scanQuerier(row scannable) (Row, error) {
	var r Row
	var specRaw, statusRaw []byte
	err := row.Scan(
		&r.ID, &r.Name, &r.Project, &r.Environment, &r.Generation, &r.ResourceVersion,
		&specRaw, &statusRaw, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return Row{}, err
	}
	spec, err := unmarshalSpec(specRaw)
	if err != nil {
		return Row{}, err
	}
	status, err := unmarshalStatus(statusRaw)
	if err != nil {
		return Row{}, err
	}
	r.Spec = spec
	r.Status = status
	return r, nil
}

func scanRow(rows pgx.Rows) (Row, error) {
	return scanQuerier(rows)
}

func validateSpec(spec ScalingPolicySpec) error {
	if strings.TrimSpace(spec.TargetRef.Kind) == "" || strings.TrimSpace(spec.TargetRef.Name) == "" {
		return fmt.Errorf("spec.targetRef.kind and spec.targetRef.name are required")
	}
	if spec.MinReplicas < 0 || spec.MaxReplicas < 1 || spec.MinReplicas > spec.MaxReplicas {
		return fmt.Errorf("spec.minReplicas/maxReplicas are invalid")
	}
	if len(spec.Metrics) == 0 {
		return fmt.Errorf("spec.metrics must contain at least one metric")
	}
	return nil
}

func validateIdempotencyKey(key string) error {
	if len(key) < 1 || len(key) > 128 {
		return fmt.Errorf("Idempotency-Key must be 1–128 characters")
	}
	for _, c := range key {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' {
			continue
		}
		return fmt.Errorf("Idempotency-Key contains invalid character")
	}
	return nil
}

func hashBody(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Store) lookupIdempotency(ctx context.Context, key string) (int, []byte, bool, error) {
	var status int
	var body []byte
	err := s.Pool.QueryRow(ctx, `
SELECT response_status, response_body FROM idempotency_keys WHERE key=$1`, key,
	).Scan(&status, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, err
	}
	return status, body, true, nil
}

func (s *Store) idempotencyHash(ctx context.Context, key string) (string, error) {
	var hash string
	err := s.Pool.QueryRow(ctx, `SELECT body_hash FROM idempotency_keys WHERE key=$1`, key).Scan(&hash)
	return hash, err
}

func (s *Store) saveIdempotency(ctx context.Context, key, bodyHash string, status int, envelope Envelope) error {
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `
INSERT INTO idempotency_keys (key, body_hash, response_status, response_body)
VALUES ($1,$2,$3,$4::jsonb)
ON CONFLICT (key) DO NOTHING`, key, bodyHash, status, string(raw))
	return err
}

func isUniqueViolation(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique")
}

func trim(s string) string { return strings.TrimSpace(s) }

func newID() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:]) + fmt.Sprintf("%d", time.Now().UnixNano()%1000)
}

// ConflictCurrentRV extracts the current RV from a conflict error message when present.
func ConflictCurrentRV(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	const marker = "current "
	if i := strings.LastIndex(msg, marker); i >= 0 {
		return strings.TrimSpace(msg[i+len(marker):])
	}
	return ""
}
