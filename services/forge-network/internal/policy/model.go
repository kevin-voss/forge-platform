package policy

import (
	"encoding/json"
	"time"
)

// Default policy values for an environment.
const (
	DefaultAllowWithin = "allow-within-environment"
	DefaultDenyAll     = "deny-all"
)

// PolicySpec is the NetworkPolicy desired state.
type PolicySpec struct {
	Target  PolicyTarget  `json:"target"`
	Ingress []IngressRule `json:"ingress,omitempty"`
	Egress  []EgressRule  `json:"egress,omitempty"`
}

// PolicyTarget selects the application the policy applies to.
type PolicyTarget struct {
	Application string `json:"application"`
}

// IngressRule allows traffic from a named service.
type IngressRule struct {
	From  PeerRef `json:"from"`
	Ports []Port  `json:"ports,omitempty"`
}

// EgressRule allows traffic to a named database or queue.
type EgressRule struct {
	To    PeerRef `json:"to"`
	Ports []Port  `json:"ports,omitempty"`
}

// PeerRef names a peer by service, database, or queue.
type PeerRef struct {
	Service  string `json:"service,omitempty"`
	Database string `json:"database,omitempty"`
	Queue    string `json:"queue,omitempty"`
}

// Port is an L4 port+protocol.
type Port struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

// NetworkPolicyEnvelope is the epic-20 style API envelope.
type NetworkPolicyEnvelope struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Metadata   PolicyMetadata      `json:"metadata"`
	Spec       PolicySpec          `json:"spec"`
	Status     NetworkPolicyStatus `json:"status"`
}

// PolicyMetadata carries identity and concurrency fields.
type PolicyMetadata struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Organization      string `json:"organization"`
	Project           string `json:"project"`
	Environment       string `json:"environment"`
	Generation        int    `json:"generation"`
	ResourceVersion   string `json:"resourceVersion"`
	CreationTimestamp string `json:"creationTimestamp,omitempty"`
}

// NetworkPolicyStatus is observed enforcement state.
type NetworkPolicyStatus struct {
	Phase              string      `json:"phase"`
	ObservedGeneration int         `json:"observedGeneration"`
	Conditions         []Condition `json:"conditions,omitempty"`
}

// Condition is a status condition.
type Condition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// PolicyRow is the persistence row.
type PolicyRow struct {
	ID                string
	Organization      string
	Project           string
	Environment       string
	Name              string
	TargetApplication string
	Spec              PolicySpec
	Generation        int
	ResourceVersion   int64
	Phase             string
	ConditionType     *string
	ConditionStatus   *string
	ConditionReason   *string
	ConditionMessage  *string
	CreatedAt         time.Time
}

// ToEnvelope maps a row to the API envelope.
func (r PolicyRow) ToEnvelope() NetworkPolicyEnvelope {
	env := NetworkPolicyEnvelope{
		APIVersion: "forge.dev/v1",
		Kind:       "NetworkPolicy",
		Metadata: PolicyMetadata{
			ID:                r.ID,
			Name:              r.Name,
			Organization:      r.Organization,
			Project:           r.Project,
			Environment:       r.Environment,
			Generation:        r.Generation,
			ResourceVersion:   formatRV(r.ResourceVersion),
			CreationTimestamp: r.CreatedAt.UTC().Format(time.RFC3339),
		},
		Spec: r.Spec,
		Status: NetworkPolicyStatus{
			Phase:              r.Phase,
			ObservedGeneration: r.Generation,
		},
	}
	if r.ConditionType != nil && *r.ConditionType != "" {
		status := "True"
		if r.ConditionStatus != nil {
			status = *r.ConditionStatus
		}
		reason, message := "", ""
		if r.ConditionReason != nil {
			reason = *r.ConditionReason
		}
		if r.ConditionMessage != nil {
			message = *r.ConditionMessage
		}
		env.Status.Conditions = []Condition{{
			Type:    *r.ConditionType,
			Status:  status,
			Reason:  reason,
			Message: message,
		}}
	}
	return env
}

// EnvironmentDefaults is the per-environment default policy.
type EnvironmentDefaults struct {
	Organization  string `json:"organization"`
	Project       string `json:"project"`
	Environment   string `json:"environment"`
	DefaultPolicy string `json:"defaultPolicy"`
	Generation    int    `json:"generation"`
}

// WorkloadPlacement is a scheduler placement + identity used for compile.
type WorkloadPlacement struct {
	WorkloadID   string  `json:"workload_id"`
	Organization string  `json:"organization"`
	Project      string  `json:"project"`
	Environment  string  `json:"environment"`
	NodeID       string  `json:"node_id"`
	Application  *string `json:"application,omitempty"`
	Service      *string `json:"service,omitempty"`
	Database     *string `json:"database,omitempty"`
	Queue        *string `json:"queue,omitempty"`
	Address      string  `json:"address,omitempty"` // filled from lease at compile time
}

// CompiledRule is one nftables-ready rule for a node.
type CompiledRule struct {
	WorkloadID string `json:"workload_id"`
	Direction  string `json:"direction"` // ingress|egress
	FromCIDR   string `json:"from_cidr,omitempty"`
	ToCIDR     string `json:"to_cidr,omitempty"`
	Port       *int   `json:"port,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Action     string `json:"action"` // allow|deny
	Reason     string `json:"reason,omitempty"`
}

// NodeRuleSet is GET /v1/nodes/{id}/network-policy-rules.
type NodeRuleSet struct {
	NodeID     string         `json:"node_id"`
	Generation int64          `json:"generation"`
	Rules      []CompiledRule `json:"rules"`
}

// CompileInput is the world snapshot fed to PolicyCompiler.
type CompileInput struct {
	Policies   []PolicyRow
	Defaults   map[envKey]string // organization/project/environment → default
	Placements []WorkloadPlacement
	ClusterDef string // FORGE_NETWORK_POLICY_DEFAULT
}

type envKey struct {
	Org, Project, Env string
}

func formatRV(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func marshalSpec(spec PolicySpec) ([]byte, error) {
	return json.Marshal(spec)
}

func unmarshalSpec(raw []byte) (PolicySpec, error) {
	var spec PolicySpec
	err := json.Unmarshal(raw, &spec)
	return spec, err
}
