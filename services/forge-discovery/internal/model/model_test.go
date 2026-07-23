package model

import (
	"encoding/json"
	"testing"
)

func TestServiceEnvelopeRoundTrip(t *testing.T) {
	svc := NewService(ResourceMetadata{
		ID:              "svc_01TEST",
		Name:            "demo-echo",
		Project:         "demo",
		Environment:     "local",
		Generation:      1,
		ResourceVersion: "1",
	}, ServiceSpec{
		Ports:   []ServicePort{{Name: "http", Port: 8080, Protocol: "http"}},
		Aliases: []string{"echo"},
	})

	raw, err := json.Marshal(svc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Service
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.APIVersion != "forge.dev/v1" || back.Kind != "Service" {
		t.Fatalf("envelope = %s/%s", back.APIVersion, back.Kind)
	}
	if back.Metadata.Name != "demo-echo" || back.Metadata.Project != "demo" {
		t.Fatalf("metadata = %+v", back.Metadata)
	}
	if len(back.Spec.Ports) != 1 || back.Spec.Ports[0].Port != 8080 {
		t.Fatalf("ports = %+v", back.Spec.Ports)
	}
	if len(back.Spec.Aliases) != 1 || back.Spec.Aliases[0] != "echo" {
		t.Fatalf("aliases = %+v", back.Spec.Aliases)
	}
}

func TestEndpointEnvelopeRoundTrip(t *testing.T) {
	ep := NewEndpoint(ResourceMetadata{
		ID:              "replica-1",
		Name:            "replica-1",
		Project:         "demo",
		Environment:     "local",
		Generation:      1,
		ResourceVersion: "1",
	}, EndpointSpec{
		Service:  "demo-echo",
		NodeID:   "node-local",
		Address:  EndpointAddress{IP: "10.0.0.2", Port: 8080},
		Protocol: "http",
		Revision: "rev-1",
	})

	raw, err := json.Marshal(ep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Endpoint
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Kind != "Endpoint" || back.Status.Phase != EndpointPhasePending {
		t.Fatalf("kind/phase = %s/%s", back.Kind, back.Status.Phase)
	}
	if back.Spec.Address.IP != "10.0.0.2" || back.Spec.NodeID != "node-local" {
		t.Fatalf("spec = %+v", back.Spec)
	}
}
