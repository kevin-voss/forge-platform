package controlmirror

import (
	"encoding/json"
	"testing"
)

func TestServiceKindPayloadMatchesContract(t *testing.T) {
	raw, err := json.Marshal(ServiceKindPayload())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	assertEq(t, m["apiVersion"], "forge.dev/v1")
	assertEq(t, m["kind"], "Service")
	assertEq(t, m["plural"], "services")
	assertEq(t, m["scope"], "namespaced")
	assertEq(t, m["controller"], "forge-discovery")
	assertEq(t, m["schemaVersion"], float64(1))
}

func TestEndpointKindPayloadMatchesContract(t *testing.T) {
	raw, err := json.Marshal(EndpointKindPayload())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	assertEq(t, m["apiVersion"], "forge.dev/v1")
	assertEq(t, m["kind"], "Endpoint")
	assertEq(t, m["plural"], "endpoints")
	assertEq(t, m["scope"], "namespaced")
	assertEq(t, m["controller"], "forge-discovery")
	assertEq(t, m["schemaVersion"], float64(1))
}

func assertEq(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
