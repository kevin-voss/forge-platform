package model

import "time"

// Service is the epic-20 envelope for a Discovery Service resource.
type Service struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   ResourceMetadata `json:"metadata"`
	Spec       ServiceSpec      `json:"spec"`
	Status     ServiceStatus    `json:"status"`
}

// ServiceSpec holds ports and DNS aliases.
type ServiceSpec struct {
	Ports   []ServicePort `json:"ports"`
	Aliases []string      `json:"aliases"`
}

// ServicePort is a named listening port on the service.
type ServicePort struct {
	Name     string `json:"name,omitempty"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"`
}

// ServiceStatus is controller-owned status (mirrored resourceVersion).
type ServiceStatus struct {
	Phase              string `json:"phase,omitempty"`
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
}

// NewService builds a Service envelope with forge.dev/v1 defaults.
func NewService(meta ResourceMetadata, spec ServiceSpec) Service {
	if meta.Labels == nil {
		meta.Labels = map[string]string{}
	}
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	if spec.Ports == nil {
		spec.Ports = []ServicePort{}
	}
	if spec.Aliases == nil {
		spec.Aliases = []string{}
	}
	return Service{
		APIVersion: "forge.dev/v1",
		Kind:       "Service",
		Metadata:   meta,
		Spec:       spec,
		Status:     ServiceStatus{},
	}
}

// ResourceMetadata mirrors Control's epic-20 metadata wire shape.
type ResourceMetadata struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Organization    string            `json:"organization,omitempty"`
	Project         string            `json:"project,omitempty"`
	Environment     string            `json:"environment,omitempty"`
	Generation      int64             `json:"generation"`
	ResourceVersion string            `json:"resourceVersion"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
	CreatedAt       *time.Time        `json:"createdAt,omitempty"`
	UpdatedAt       *time.Time        `json:"updatedAt,omitempty"`
}
