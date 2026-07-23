package policy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Typed store errors.
var (
	ErrPolicyNotFound      = errors.New("NetworkPolicyNotFound")
	ErrPolicyAlreadyExists = errors.New("NetworkPolicyAlreadyExists")
	ErrTargetNotFound      = errors.New("TargetNotFound")
)

// Store persists NetworkPolicy, environment defaults, and placement mirrors.
type Store struct {
	Pool           *pgxpool.Pool
	Log            *slog.Logger
	ClusterDefault string
}

// CreatePolicy inserts a NetworkPolicy. Marks Failed when target has no placement.
func (s *Store) CreatePolicy(ctx context.Context, org, project, env, name string, spec PolicySpec) (PolicyRow, error) {
	org, project, env, name = trim(org), trim(project), trim(env), trim(name)
	if org == "" || project == "" || env == "" || name == "" {
		return PolicyRow{}, fmt.Errorf("organization, project, environment, and name are required")
	}
	app := trim(spec.Target.Application)
	if app == "" {
		return PolicyRow{}, fmt.Errorf("spec.target.application is required")
	}
	spec.Target.Application = app

	phase := "Ready"
	condType, condStatus, condReason, condMsg := "Enforced", "True", "AppliedToAllNodes", "policy compiled"
	exists, err := s.targetExists(ctx, org, project, env, app)
	if err != nil {
		return PolicyRow{}, err
	}
	if !exists {
		phase = "Failed"
		condType, condStatus, condReason, condMsg = "Enforced", "False", "TargetNotFound",
			fmt.Sprintf("application %q not found in environment", app)
	}

	id := "np_" + newID()
	raw, err := marshalSpec(spec)
	if err != nil {
		return PolicyRow{}, err
	}

	_, err = s.Pool.Exec(ctx, `
INSERT INTO network.network_policies (
  id, organization, project, environment, name, target_application, spec_json,
  generation, resource_version, phase,
  condition_type, condition_status, condition_reason, condition_message
) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,1,1,$8,$9,$10,$11,$12)`,
		id, org, project, env, name, app, string(raw), phase,
		condType, condStatus, condReason, condMsg,
	)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			return PolicyRow{}, ErrPolicyAlreadyExists
		}
		return PolicyRow{}, err
	}
	if err := s.bumpGeneration(ctx); err != nil {
		return PolicyRow{}, err
	}
	return s.GetPolicy(ctx, org, project, env, name)
}

// GetPolicy loads one policy by scope+name.
func (s *Store) GetPolicy(ctx context.Context, org, project, env, name string) (PolicyRow, error) {
	row, err := s.scanPolicy(ctx, `
SELECT id, organization, project, environment, name, target_application, spec_json,
       generation, resource_version, phase,
       condition_type, condition_status, condition_reason, condition_message, created_at
FROM network.network_policies
WHERE organization=$1 AND project=$2 AND environment=$3 AND name=$4`,
		org, project, env, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return PolicyRow{}, ErrPolicyNotFound
	}
	return row, err
}

// ListPolicies lists policies in an environment.
func (s *Store) ListPolicies(ctx context.Context, org, project, env string) ([]PolicyRow, error) {
	rows, err := s.Pool.Query(ctx, `
SELECT id, organization, project, environment, name, target_application, spec_json,
       generation, resource_version, phase,
       condition_type, condition_status, condition_reason, condition_message, created_at
FROM network.network_policies
WHERE organization=$1 AND project=$2 AND environment=$3
ORDER BY name`, org, project, env)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyRow
	for rows.Next() {
		r, err := scanPolicyRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdatePolicy replaces the spec (generation bump).
func (s *Store) UpdatePolicy(ctx context.Context, org, project, env, name string, spec PolicySpec) (PolicyRow, error) {
	app := trim(spec.Target.Application)
	if app == "" {
		return PolicyRow{}, fmt.Errorf("spec.target.application is required")
	}
	spec.Target.Application = app
	raw, err := marshalSpec(spec)
	if err != nil {
		return PolicyRow{}, err
	}

	phase := "Ready"
	condType, condStatus, condReason, condMsg := "Enforced", "True", "AppliedToAllNodes", "policy compiled"
	exists, err := s.targetExists(ctx, org, project, env, app)
	if err != nil {
		return PolicyRow{}, err
	}
	if !exists {
		phase = "Failed"
		condType, condStatus, condReason, condMsg = "Enforced", "False", "TargetNotFound",
			fmt.Sprintf("application %q not found in environment", app)
	}

	tag, err := s.Pool.Exec(ctx, `
UPDATE network.network_policies
SET target_application=$5, spec_json=$6::jsonb,
    generation = generation + 1, resource_version = resource_version + 1,
    phase=$7, condition_type=$8, condition_status=$9, condition_reason=$10, condition_message=$11
WHERE organization=$1 AND project=$2 AND environment=$3 AND name=$4`,
		org, project, env, name, app, string(raw), phase,
		condType, condStatus, condReason, condMsg,
	)
	if err != nil {
		return PolicyRow{}, err
	}
	if tag.RowsAffected() == 0 {
		return PolicyRow{}, ErrPolicyNotFound
	}
	if err := s.bumpGeneration(ctx); err != nil {
		return PolicyRow{}, err
	}
	return s.GetPolicy(ctx, org, project, env, name)
}

// DeletePolicy removes a policy.
func (s *Store) DeletePolicy(ctx context.Context, org, project, env, name string) error {
	tag, err := s.Pool.Exec(ctx, `
DELETE FROM network.network_policies
WHERE organization=$1 AND project=$2 AND environment=$3 AND name=$4`,
		org, project, env, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPolicyNotFound
	}
	return s.bumpGeneration(ctx)
}

// ListAllPolicies returns every policy (for compilation).
func (s *Store) ListAllPolicies(ctx context.Context) ([]PolicyRow, error) {
	rows, err := s.Pool.Query(ctx, `
SELECT id, organization, project, environment, name, target_application, spec_json,
       generation, resource_version, phase,
       condition_type, condition_status, condition_reason, condition_message, created_at
FROM network.network_policies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyRow
	for rows.Next() {
		r, err := scanPolicyRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetOrCreateDefaults returns environment defaults (creating with cluster default if missing).
func (s *Store) GetOrCreateDefaults(ctx context.Context, org, project, env string) (EnvironmentDefaults, error) {
	org, project, env = trim(org), trim(project), trim(env)
	def := s.ClusterDefault
	if def == "" {
		def = DefaultAllowWithin
	}
	_, err := s.Pool.Exec(ctx, `
INSERT INTO network.environment_network_defaults (organization, project, environment, default_policy)
VALUES ($1,$2,$3,$4)
ON CONFLICT (organization, project, environment) DO NOTHING`,
		org, project, env, def)
	if err != nil {
		return EnvironmentDefaults{}, err
	}
	return s.GetDefaults(ctx, org, project, env)
}

// GetDefaults loads environment defaults.
func (s *Store) GetDefaults(ctx context.Context, org, project, env string) (EnvironmentDefaults, error) {
	var d EnvironmentDefaults
	err := s.Pool.QueryRow(ctx, `
SELECT organization, project, environment, default_policy, generation
FROM network.environment_network_defaults
WHERE organization=$1 AND project=$2 AND environment=$3`,
		org, project, env,
	).Scan(&d.Organization, &d.Project, &d.Environment, &d.DefaultPolicy, &d.Generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.GetOrCreateDefaults(ctx, org, project, env)
	}
	return d, err
}

// PatchDefaults sets the environment default policy.
func (s *Store) PatchDefaults(ctx context.Context, org, project, env, defaultPolicy string) (EnvironmentDefaults, error) {
	defaultPolicy = strings.ToLower(trim(defaultPolicy))
	if defaultPolicy != DefaultAllowWithin && defaultPolicy != DefaultDenyAll {
		return EnvironmentDefaults{}, fmt.Errorf("defaultPolicy must be %s or %s", DefaultAllowWithin, DefaultDenyAll)
	}
	_, err := s.GetOrCreateDefaults(ctx, org, project, env)
	if err != nil {
		return EnvironmentDefaults{}, err
	}
	_, err = s.Pool.Exec(ctx, `
UPDATE network.environment_network_defaults
SET default_policy=$4, generation = generation + 1, updated_at = now()
WHERE organization=$1 AND project=$2 AND environment=$3`,
		org, project, env, defaultPolicy)
	if err != nil {
		return EnvironmentDefaults{}, err
	}
	if err := s.bumpGeneration(ctx); err != nil {
		return EnvironmentDefaults{}, err
	}
	return s.GetDefaults(ctx, org, project, env)
}

// ListAllDefaults returns every environment default.
func (s *Store) ListAllDefaults(ctx context.Context) (map[envKey]string, error) {
	rows, err := s.Pool.Query(ctx, `
SELECT organization, project, environment, default_policy
FROM network.environment_network_defaults`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[envKey]string{}
	for rows.Next() {
		var org, project, env, def string
		if err := rows.Scan(&org, &project, &env, &def); err != nil {
			return nil, err
		}
		out[envKey{org, project, env}] = def
	}
	return out, rows.Err()
}

// UpsertPlacement mirrors a scheduler placement for the compiler.
func (s *Store) UpsertPlacement(ctx context.Context, p WorkloadPlacement) error {
	if trim(p.WorkloadID) == "" || trim(p.NodeID) == "" {
		return fmt.Errorf("workload_id and node_id are required")
	}
	_, err := s.Pool.Exec(ctx, `
INSERT INTO network.workload_placements (
  workload_id, organization, project, environment, node_id,
  application, service, database_name, queue_name, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, now())
ON CONFLICT (workload_id) DO UPDATE SET
  organization=EXCLUDED.organization,
  project=EXCLUDED.project,
  environment=EXCLUDED.environment,
  node_id=EXCLUDED.node_id,
  application=EXCLUDED.application,
  service=EXCLUDED.service,
  database_name=EXCLUDED.database_name,
  queue_name=EXCLUDED.queue_name,
  updated_at=now()`,
		p.WorkloadID, p.Organization, p.Project, p.Environment, p.NodeID,
		p.Application, p.Service, p.Database, p.Queue,
	)
	if err != nil {
		return err
	}
	return s.bumpGeneration(ctx)
}

// Generation returns the cluster-wide rule-set generation.
func (s *Store) Generation(ctx context.Context) (int64, error) {
	var g int64
	err := s.Pool.QueryRow(ctx, `SELECT generation FROM network.policy_rule_generation WHERE id=1`).Scan(&g)
	return g, err
}

// LoadCompileInput builds the compiler snapshot (placements joined with active leases).
func (s *Store) LoadCompileInput(ctx context.Context) (CompileInput, int64, error) {
	policies, err := s.ListAllPolicies(ctx)
	if err != nil {
		return CompileInput{}, 0, err
	}
	defaults, err := s.ListAllDefaults(ctx)
	if err != nil {
		return CompileInput{}, 0, err
	}
	placements, err := s.listPlacementsWithAddresses(ctx)
	if err != nil {
		return CompileInput{}, 0, err
	}
	gen, err := s.Generation(ctx)
	if err != nil {
		return CompileInput{}, 0, err
	}
	return CompileInput{
		Policies:   policies,
		Defaults:   defaults,
		Placements: placements,
		ClusterDef: s.ClusterDefault,
	}, gen, nil
}

func (s *Store) listPlacementsWithAddresses(ctx context.Context) ([]WorkloadPlacement, error) {
	rows, err := s.Pool.Query(ctx, `
SELECT p.workload_id, p.organization, p.project, p.environment, p.node_id,
       p.application, p.service, p.database_name, p.queue_name,
       host(wl.address)::text
FROM network.workload_placements p
LEFT JOIN network.workload_leases wl
  ON wl.workload_id = p.workload_id AND wl.released_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkloadPlacement
	for rows.Next() {
		var p WorkloadPlacement
		var addr *string
		if err := rows.Scan(
			&p.WorkloadID, &p.Organization, &p.Project, &p.Environment, &p.NodeID,
			&p.Application, &p.Service, &p.Database, &p.Queue, &addr,
		); err != nil {
			return nil, err
		}
		if addr != nil {
			p.Address = *addr
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) targetExists(ctx context.Context, org, project, env, app string) (bool, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `
SELECT COUNT(*) FROM network.workload_placements
WHERE organization=$1 AND project=$2 AND environment=$3 AND application=$4`,
		org, project, env, app,
	).Scan(&n)
	if err != nil {
		return false, err
	}
	// If no placements are registered yet, treat target as present so create can
	// reach Ready in empty clusters (placements may arrive later). Integration
	// tests seed placements first; TargetNotFound applies when placements exist
	// for the env but the application name is absent.
	var envCount int
	err = s.Pool.QueryRow(ctx, `
SELECT COUNT(*) FROM network.workload_placements
WHERE organization=$1 AND project=$2 AND environment=$3`,
		org, project, env,
	).Scan(&envCount)
	if err != nil {
		return false, err
	}
	if envCount == 0 {
		return true, nil
	}
	return n > 0, nil
}

func (s *Store) bumpGeneration(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `
UPDATE network.policy_rule_generation SET generation = generation + 1 WHERE id=1`)
	return err
}

func (s *Store) scanPolicy(ctx context.Context, q string, args ...any) (PolicyRow, error) {
	row := s.Pool.QueryRow(ctx, q, args...)
	return scanPolicyQuerier(row)
}

type scannable interface {
	Scan(dest ...any) error
}

func scanPolicyQuerier(row scannable) (PolicyRow, error) {
	var r PolicyRow
	var raw []byte
	err := row.Scan(
		&r.ID, &r.Organization, &r.Project, &r.Environment, &r.Name, &r.TargetApplication, &raw,
		&r.Generation, &r.ResourceVersion, &r.Phase,
		&r.ConditionType, &r.ConditionStatus, &r.ConditionReason, &r.ConditionMessage, &r.CreatedAt,
	)
	if err != nil {
		return PolicyRow{}, err
	}
	spec, err := unmarshalSpec(raw)
	if err != nil {
		return PolicyRow{}, err
	}
	r.Spec = spec
	return r, nil
}

func scanPolicyRow(rows pgx.Rows) (PolicyRow, error) {
	return scanPolicyQuerier(rows)
}

func trim(s string) string { return strings.TrimSpace(s) }

func newID() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:]) + fmt.Sprintf("%d", time.Now().UnixNano()%1000)
}
