package logs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateRequiresScopingFilter(t *testing.T) {
	_, err := ValidateAndNormalize("", "", "", "", "", "", "", "", "10", "", "", time.Now().UTC(), DefaultCaps())
	if err == nil || !strings.Contains(err.Error(), "scoping filter") {
		t.Fatalf("expected scoping filter error, got %v", err)
	}
}

func TestBuildLogQLProjectAndTrace(t *testing.T) {
	f := Filters{Project: "prj_1", TraceID: "abc123", Limit: 10, Direction: DirectionBackward}
	q := BuildLogQL(f)
	if !strings.Contains(q, `forge_project="prj_1"`) {
		t.Fatalf("missing project label: %s", q)
	}
	if !strings.Contains(q, `| json`) {
		t.Fatalf("missing json pipeline: %s", q)
	}
	if !strings.Contains(q, `trace_id="abc123"`) {
		t.Fatalf("missing trace filter: %s", q)
	}
}

func TestBuildLogQLAllFilters(t *testing.T) {
	f := Filters{
		Project:    "prj_1",
		Deployment: "dpl_1",
		Service:    "control",
		RequestID:  "req_1",
		TraceID:    "tr_1",
		Q:          "boom(x)",
	}
	q := BuildLogQL(f)
	for _, want := range []string{
		`forge_project="prj_1"`,
		`forge_deployment="dpl_1"`,
		`forge_service="control"`,
		`request_id="req_1"`,
		`trace_id="tr_1"`,
		`|~`,
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("LogQL missing %q in %s", want, q)
		}
	}
	if strings.Contains(q, "boom(x)") && !strings.Contains(q, `boom\(x\)`) {
		t.Fatalf("q not escaped: %s", q)
	}
}

func TestBuildLogQLTraceOnlyUsesJobSelector(t *testing.T) {
	q := BuildLogQL(Filters{TraceID: "t1"})
	if !strings.HasPrefix(q, `{job=~".+"}`) {
		t.Fatalf("want job fallback selector, got %s", q)
	}
}

func TestLimitAndRangeClamped(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	f, err := ValidateAndNormalize(
		"prj_1", "", "", "", "", "",
		now.Add(-48*time.Hour).Format(time.RFC3339),
		now.Format(time.RFC3339),
		"5000", "", "", now, DefaultCaps(),
	)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if f.Limit != 1000 {
		t.Fatalf("Limit = %d, want 1000", f.Limit)
	}
	if !f.Capped || len(f.Warnings) == 0 {
		t.Fatalf("expected capped warnings, got %+v", f)
	}
	if f.Until.Sub(f.Since) != 24*time.Hour {
		t.Fatalf("range = %s, want 24h", f.Until.Sub(f.Since))
	}
}

func TestEscapeQInjectSafe(t *testing.T) {
	q := BuildLogQL(Filters{Project: "p", Q: `"|{}`})
	if strings.Contains(q, `"|{}`) {
		t.Fatalf("raw injection left in query: %s", q)
	}
	if !strings.Contains(q, `|~`) {
		t.Fatalf("missing regexp filter: %s", q)
	}
}

type stubLoki struct {
	values []StreamValue
	err    error
	lastQ  string
}

func (s *stubLoki) QueryRange(_ context.Context, logql string, _, _ time.Time, _ int, _ string) ([]StreamValue, error) {
	s.lastQ = logql
	if s.err != nil {
		return nil, s.err
	}
	return s.values, nil
}

func TestServiceQueryNormalizesAndOrders(t *testing.T) {
	t1 := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Second)
	stub := &stubLoki{values: []StreamValue{
		{Timestamp: t2, Line: `{"message":"b","service":"gateway","trace_id":"T","level":"info","forge.deployment":"dpl_1"}`, Labels: map[string]string{"forge_service": "gateway"}},
		{Timestamp: t1, Line: `{"message":"a","service":"control","trace_id":"T","level":"info","forge.deployment":"dpl_1"}`, Labels: map[string]string{"forge_service": "control"}},
	}}
	svc := &Service{Loki: stub, Caps: DefaultCaps()}
	f := Filters{
		TraceID:   "T",
		Since:     t1.Add(-time.Minute),
		Until:     t2.Add(time.Minute),
		Limit:     10,
		Direction: DirectionForward,
	}
	res, err := svc.Query(context.Background(), f)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("entries = %d", len(res.Entries))
	}
	if res.Entries[0].Service != "control" || res.Entries[1].Service != "gateway" {
		t.Fatalf("order = %v, %v", res.Entries[0].Service, res.Entries[1].Service)
	}
	if res.Entries[0].Deployment != "dpl_1" {
		t.Fatalf("deployment = %q", res.Entries[0].Deployment)
	}
	if !strings.Contains(stub.lastQ, `trace_id="T"`) {
		t.Fatalf("logql = %s", stub.lastQ)
	}
}

func TestServiceLokiDown(t *testing.T) {
	svc := &Service{Loki: &stubLoki{err: errors.New("connection refused")}, Caps: DefaultCaps()}
	_, err := svc.Query(context.Background(), Filters{
		Project: "p", Since: time.Now().Add(-time.Hour), Until: time.Now(), Limit: 10, Direction: DirectionBackward,
	})
	if !errors.Is(err, ErrLokiUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestFilterByProjects(t *testing.T) {
	entries := []Entry{
		{Project: "prj_a", Message: "a"},
		{Project: "prj_b", Message: "b"},
		{Project: "", Message: "x"},
	}
	allowed := map[string]struct{}{"prj_a": {}}
	got := FilterByProjects(entries, allowed, false)
	if len(got) != 1 || got[0].Project != "prj_a" {
		t.Fatalf("got %+v", got)
	}
}
