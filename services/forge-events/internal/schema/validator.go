package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Sentinel errors for schema validation.
var (
	ErrUnknownSchema        = errors.New("unknown schema")
	ErrUnknownSchemaVersion = errors.New("unknown schema version")
	ErrValidationFailed     = errors.New("schema validation failed")
)

// Error is returned when publish-time validation fails.
type Error struct {
	Subject    string
	Reason     string // unknown_schema | unknown_version | validation_failed
	Violations []Violation
	err        error
}

func (e *Error) Error() string {
	if e == nil {
		return "schema error"
	}
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("schema %s for %s", e.Reason, e.Subject)
}

func (e *Error) Unwrap() error { return e.err }

// Validate checks data against the registered schema for subject.
// schemaVersion 0 selects the latest registered version.
// In warn mode, failures are logged and nil is returned (publish allowed).
func (r *Registry) Validate(subject string, data json.RawMessage, schemaVersion int) error {
	start := time.Now()
	entry, err := r.Lookup(subject, schemaVersion)
	if err != nil {
		reason := "unknown_schema"
		if errors.Is(err, ErrUnknownSchemaVersion) {
			reason = "unknown_version"
		}
		r.metrics.Rejected.Add(1)
		r.log.Warn("event schema rejected",
			"span", "events.validate",
			"subject", subject,
			"reason", reason,
			"violations_count", 0,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		ve := &Error{
			Subject: subject,
			Reason:  reason,
			err:     err,
		}
		if r.mode == ModeWarn {
			r.log.Warn("schema validation warn mode allowing publish",
				"subject", subject, "reason", reason)
			return nil
		}
		return ve
	}

	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		r.metrics.Rejected.Add(1)
		ve := &Error{
			Subject: subject,
			Reason:  "validation_failed",
			Violations: []Violation{{
				Path:    "",
				Message: "data must be valid JSON",
				Keyword: "type",
			}},
			err: ErrValidationFailed,
		}
		r.log.Warn("event schema rejected",
			"span", "events.validate",
			"subject", subject,
			"reason", ve.Reason,
			"violations_count", 1,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		if r.mode == ModeWarn {
			return nil
		}
		return ve
	}

	if err := entry.compiled.Validate(doc); err != nil {
		violations := flattenViolations(err)
		r.metrics.Rejected.Add(1)
		r.log.Warn("event schema rejected",
			"span", "events.validate",
			"subject", subject,
			"schema_version", entry.SchemaVersion,
			"reason", "validation_failed",
			"violations_count", len(violations),
			"duration_ms", time.Since(start).Milliseconds(),
		)
		ve := &Error{
			Subject:    subject,
			Reason:     "validation_failed",
			Violations: violations,
			err:        ErrValidationFailed,
		}
		if r.mode == ModeWarn {
			r.log.Warn("schema validation warn mode allowing publish",
				"subject", subject, "violations_count", len(violations))
			return nil
		}
		return ve
	}

	r.log.Debug("event schema validated",
		"span", "events.validate",
		"subject", subject,
		"schema_version", entry.SchemaVersion,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

func flattenViolations(err error) []Violation {
	var out []Violation
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []Violation{{Path: "", Message: err.Error()}}
	}
	for _, e := range ve.BasicOutput().Errors {
		if e.Error == "" {
			continue
		}
		// Skip the root aggregate "doesn't validate with ..." when causes exist.
		if strings.Contains(e.Error, "doesn't validate with") && len(ve.Causes) > 0 && e.KeywordLocation == "" {
			continue
		}
		path := strings.TrimPrefix(e.InstanceLocation, "/")
		out = append(out, Violation{
			Path:    path,
			Message: e.Error,
			Keyword: keywordFromLocation(e.KeywordLocation),
		})
	}
	if len(out) == 0 {
		out = append(out, Violation{Path: "", Message: ve.Error()})
	}
	return out
}

func keywordFromLocation(loc string) string {
	loc = strings.Trim(loc, "/")
	if loc == "" {
		return ""
	}
	parts := strings.Split(loc, "/")
	return parts[len(parts)-1]
}
