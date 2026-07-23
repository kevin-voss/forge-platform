package openapi_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPISkeletonPaths(t *testing.T) {
	root, ok := repoRoot()
	if !ok {
		t.Skip("openapi contract file not available in this build context")
	}
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "openapi", "forge-events.openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("openapi yaml parse: %v", err)
	}
	if doc["openapi"] == nil {
		t.Fatal("missing openapi version")
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("missing paths")
	}
	for _, p := range []string{
		"/health/live", "/health/ready", "/",
		"/v1/events", "/v1/consume", "/v1/consumers", "/v1/ack", "/v1/nak",
		"/v1/processed",
		"/v1/dlq", "/v1/dlq/{dlq_id}", "/v1/dlq/{dlq_id}:redeliver",
		"/v1/schemas", "/v1/schemas/{subject}",
	} {
		if paths[p] == nil {
			t.Fatalf("openapi missing path %s", p)
		}
	}
	identity, ok := paths["/"].(map[string]any)
	if !ok {
		t.Fatal("openapi / is not an object")
	}
	get, ok := identity["get"].(map[string]any)
	if !ok {
		t.Fatal("openapi / missing get")
	}
	if get["operationId"] != "getIdentity" {
		t.Fatalf("operationId = %v, want getIdentity", get["operationId"])
	}

	eventsPath, ok := paths["/v1/events"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/events is not an object")
	}
	postEvents, ok := eventsPath["post"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/events missing post")
	}
	if postEvents["operationId"] != "publishEvent" {
		t.Fatalf("publish operationId = %v", postEvents["operationId"])
	}

	consumePath, ok := paths["/v1/consume"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/consume is not an object")
	}
	postConsume, ok := consumePath["post"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/consume missing post")
	}
	if postConsume["operationId"] != "consumeEvents" {
		t.Fatalf("consume operationId = %v", postConsume["operationId"])
	}

	for _, item := range []struct {
		path string
		op   string
	}{
		{"/v1/consumers", "createConsumer"},
		{"/v1/ack", "ackEvent"},
		{"/v1/nak", "nakEvent"},
	} {
		p, ok := paths[item.path].(map[string]any)
		if !ok {
			t.Fatalf("openapi %s is not an object", item.path)
		}
		post, ok := p["post"].(map[string]any)
		if !ok {
			t.Fatalf("openapi %s missing post", item.path)
		}
		if post["operationId"] != item.op {
			t.Fatalf("%s operationId = %v, want %s", item.path, post["operationId"], item.op)
		}
	}

	components, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatal("missing components")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("missing schemas")
	}
	for _, name := range []string{
		"Envelope", "PublishRequest", "PublishResponse",
		"ConsumeRequest", "ConsumeResponse", "DeliveredMessage", "ErrorEnvelope",
		"CreateConsumerRequest", "ConsumerInfo", "AckRequest", "NakRequest",
		"MarkProcessedRequest", "ProcessedStatus",
		"DLQEntry", "DLQDetail", "DLQRedeliverResponse",
		"SchemaSubjectInfo", "SchemaSubjectDetail", "SchemaValidationError", "SchemaViolation",
	} {
		if schemas[name] == nil {
			t.Fatalf("openapi missing schema %s", name)
		}
	}

	componentsFull, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatal("missing components for securitySchemes")
	}
	sec, ok := componentsFull["securitySchemes"].(map[string]any)
	if !ok || sec["bearerAuth"] == nil {
		t.Fatal("openapi missing bearerAuth security scheme")
	}

	eventsPathParams, ok := paths["/v1/events"].(map[string]any)
	if !ok {
		t.Fatal("/v1/events missing")
	}
	postEv, ok := eventsPathParams["post"].(map[string]any)
	if !ok {
		t.Fatal("/v1/events post missing")
	}
	params, ok := postEv["parameters"].([]any)
	if !ok || len(params) == 0 {
		t.Fatal("/v1/events must document Idempotency-Key parameter")
	}
	foundIdem := false
	for _, p := range params {
		pm, _ := p.(map[string]any)
		if pm["name"] == "Idempotency-Key" {
			foundIdem = true
		}
	}
	if !foundIdem {
		t.Fatal("Idempotency-Key parameter missing on /v1/events")
	}

	eventsPath422, ok := paths["/v1/events"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/events missing for 422 check")
	}
	post422, ok := eventsPath422["post"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/events missing post")
	}
	responses, ok := post422["responses"].(map[string]any)
	if !ok || responses["422"] == nil {
		t.Fatal("openapi /v1/events missing 422 response")
	}

	dlqEntry, ok := schemas["DLQEntry"].(map[string]any)
	if !ok {
		t.Fatal("DLQEntry not an object")
	}
	dlqProps, ok := dlqEntry["properties"].(map[string]any)
	if !ok {
		t.Fatal("DLQEntry missing properties")
	}
	for _, field := range []string{
		"dlq_id", "event_id", "original_subject", "consumer",
		"delivery_count", "last_error", "first_failed_at",
	} {
		if dlqProps[field] == nil {
			t.Fatalf("DLQEntry missing property %s", field)
		}
	}

	redeliverPath, ok := paths["/v1/dlq/{dlq_id}:redeliver"].(map[string]any)
	if !ok {
		t.Fatal("openapi redeliver path missing")
	}
	postRedeliver, ok := redeliverPath["post"].(map[string]any)
	if !ok || postRedeliver["operationId"] != "redeliverDLQ" {
		t.Fatalf("redeliver operationId = %v", postRedeliver["operationId"])
	}

	envelope, ok := schemas["Envelope"].(map[string]any)
	if !ok {
		t.Fatal("Envelope schema not an object")
	}
	props, ok := envelope["properties"].(map[string]any)
	if !ok {
		t.Fatal("Envelope missing properties")
	}
	for _, field := range []string{"id", "subject", "time", "source", "data"} {
		if props[field] == nil {
			t.Fatalf("Envelope missing property %s", field)
		}
	}
	required, ok := envelope["required"].([]any)
	if !ok {
		t.Fatal("Envelope missing required")
	}
	for _, field := range []string{"id", "subject", "time", "data"} {
		if !containsAny(required, field) {
			t.Fatalf("Envelope required missing %s", field)
		}
	}

	delivered, ok := schemas["DeliveredMessage"].(map[string]any)
	if !ok {
		t.Fatal("DeliveredMessage not an object")
	}
	dprops, ok := delivered["properties"].(map[string]any)
	if !ok {
		t.Fatal("DeliveredMessage missing properties")
	}
	for _, field := range []string{"event_id", "subject", "time", "data", "ack_token", "delivery_count"} {
		if dprops[field] == nil {
			t.Fatalf("DeliveredMessage missing property %s", field)
		}
	}
	dreq, ok := delivered["required"].([]any)
	if !ok {
		t.Fatal("DeliveredMessage missing required")
	}
	if !containsAny(dreq, "delivery_count") {
		t.Fatal("DeliveredMessage required missing delivery_count")
	}

	consumeReq, ok := schemas["ConsumeRequest"].(map[string]any)
	if !ok {
		t.Fatal("ConsumeRequest not an object")
	}
	creq, ok := consumeReq["required"].([]any)
	if !ok || !containsAny(creq, "consumer") {
		t.Fatal("ConsumeRequest must require consumer")
	}
}

func containsAny(items []any, want string) bool {
	for _, v := range items {
		if v == want {
			return true
		}
	}
	return false
}

func repoRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "contracts", "openapi", "forge-events.openapi.yaml")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
