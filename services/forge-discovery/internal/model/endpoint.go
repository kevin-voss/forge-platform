package model

// Endpoint is the epic-20 envelope for a Discovery Endpoint resource.
type Endpoint struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   ResourceMetadata `json:"metadata"`
	Spec       EndpointSpec     `json:"spec"`
	Status     EndpointStatus   `json:"status"`
}

// EndpointSpec describes where a replica listens.
type EndpointSpec struct {
	Service  string          `json:"service"`
	NodeID   string          `json:"nodeId"`
	Address  EndpointAddress `json:"address"`
	Protocol string          `json:"protocol"`
	Revision string          `json:"revision,omitempty"`
}

// EndpointAddress is the replica listen address.
type EndpointAddress struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

// EndpointStatus is lease/readiness state owned by Discovery.
type EndpointStatus struct {
	Phase              string `json:"phase"`
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
}

// Endpoint phases used by Discovery.
const (
	EndpointPhasePending     = "Pending"
	EndpointPhaseReady       = "Ready"
	EndpointPhaseUnready     = "Unready"
	EndpointPhaseTerminating = "Terminating"
)

// NewEndpoint builds an Endpoint envelope with forge.dev/v1 defaults.
func NewEndpoint(meta ResourceMetadata, spec EndpointSpec) Endpoint {
	if meta.Labels == nil {
		meta.Labels = map[string]string{}
	}
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	if spec.Protocol == "" {
		spec.Protocol = "http"
	}
	return Endpoint{
		APIVersion: "forge.dev/v1",
		Kind:       "Endpoint",
		Metadata:   meta,
		Spec:       spec,
		Status: EndpointStatus{
			Phase: EndpointPhasePending,
		},
	}
}
