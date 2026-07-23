package httpserver_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"forge.local/services/forge-autoscaler/internal/evaluate"
	"forge.local/services/forge-autoscaler/internal/health"
	httpserver "forge.local/services/forge-autoscaler/internal/http"
	"forge.local/services/forge-autoscaler/internal/metrics"
	"forge.local/services/forge-autoscaler/internal/policy"
)

func testServer(t *testing.T) (*httptest.Server, *policy.Store, *metrics.FakeSource) {
	t.Helper()
	dsn := os.Getenv("FORGE_AUTOSCALER_DB_URL")
	if dsn == "" {
		dsn = "postgres://forge:forge@127.0.0.1:5001/forge_autoscaler?sslmode=disable"
	}
	db, err := policy.Open(context.Background(), dsn, 4, true)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(db.Close)

	hub := policy.NewHub(db.Pool, 100)
	store := &policy.Store{Pool: db.Pool, Hub: hub}
	fake := metrics.NewFakeSource()
	ready := health.NewReadiness(db)
	ready.MarkReady()
	mux := http.NewServeMux()
	health.NewHandler(ready).Register(mux)
	(&httpserver.Routes{Store: store, Hub: hub}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, store, fake
}

func TestScalingPolicyRoundTripAndWatch(t *testing.T) {
	srv, _, _ := testServer(t)
	name := "rt-" + policy.FormatRV(time.Now().UnixNano())
	util := 65.0
	body := map[string]any{
		"metadata": map[string]string{"name": name},
		"spec": policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "invoice-api"},
			MinReplicas: 2,
			MaxReplicas: 20,
			Metrics:     []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
			Behavior: policy.Behavior{
				ScaleUp:   policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 4},
				ScaleDown: policy.ScaleBehavior{StabilizationWindowSeconds: 300, MaxReplicasPerMinute: 2},
			},
			Schedules: []policy.Schedule{},
		},
	}
	raw, _ := json.Marshal(body)

	watchDone := make(chan string, 1)
	go func() {
		resp, err := http.Get(srv.URL + "/v1/watch/scalingpolicies?since=0")
		if err != nil {
			watchDone <- "err:" + err.Error()
			return
		}
		defer resp.Body.Close()
		reader := bufio.NewReader(resp.Body)
		deadline := time.After(5 * time.Second)
		for {
			select {
			case <-deadline:
				watchDone <- "timeout"
				return
			default:
			}
			line, err := reader.ReadString('\n')
			if err != nil {
				watchDone <- "err:" + err.Error()
				return
			}
			if strings.HasPrefix(line, "event: ADDED") {
				watchDone <- "ADDED"
				return
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Post(
		srv.URL+"/v1/projects/invoice-platform/environments/production/scalingpolicies",
		"application/json", bytes.NewReader(raw),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status %d: %s", resp.StatusCode, b)
	}
	var created policy.Envelope
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Spec.TargetRef.Name != "invoice-api" || created.Spec.MinReplicas != 2 || created.Spec.MaxReplicas != 20 {
		t.Fatalf("spec mismatch: %+v", created.Spec)
	}
	if created.Spec.Behavior.ScaleDown.StabilizationWindowSeconds != 300 {
		t.Fatalf("behavior mismatch: %+v", created.Spec.Behavior)
	}
	if created.Spec.Schedules == nil {
		t.Fatal("schedules should be present (empty slice)")
	}

	getResp, err := http.Get(srv.URL + "/v1/projects/invoice-platform/environments/production/scalingpolicies/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != 200 {
		t.Fatalf("get status %d", getResp.StatusCode)
	}

	select {
	case msg := <-watchDone:
		if msg != "ADDED" {
			t.Fatalf("watch: %s", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch timeout")
	}
}

func TestEvaluateEndToEndWithFakeSource(t *testing.T) {
	srv, store, fake := testServer(t)
	name := "eval-" + policy.FormatRV(time.Now().UnixNano())
	util := 65.0
	spec := policy.ScalingPolicySpec{
		TargetRef:   policy.TargetRef{Kind: "Application", Name: "demo"},
		MinReplicas: 2,
		MaxReplicas: 10,
		Metrics:     []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
		Schedules:   []policy.Schedule{},
	}
	env, _, err := store.Create(context.Background(), "demo", "production", name, spec, "", "")
	if err != nil {
		t.Fatal(err)
	}
	fake.Push(spec.TargetRef, "cpu", 81.4)

	loop := &evaluate.Loop{Store: store, Source: fake, Interval: time.Hour}
	loop.Tick(context.Background())

	resp, err := http.Get(srv.URL + "/v1/projects/demo/environments/production/scalingpolicies/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got policy.Envelope
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Status.Recommendations) != 1 {
		t.Fatalf("expected 1 recommendation, got %d (created rv=%s)", len(got.Status.Recommendations), env.Metadata.ResourceVersion)
	}
	var able *policy.Condition
	for i := range got.Status.Conditions {
		if got.Status.Conditions[i].Type == "AbleToScale" {
			able = &got.Status.Conditions[i]
		}
	}
	if able == nil || able.Status != "True" {
		t.Fatalf("expected AbleToScale=True, got %+v", got.Status.Conditions)
	}
}
