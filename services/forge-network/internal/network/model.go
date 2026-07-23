package network

import "time"

// Network is the cluster-scoped Network kind (generic resource envelope).
type Network struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   ResourceMetadata `json:"metadata"`
	Spec       NetworkSpec      `json:"spec"`
	Status     NetworkStatus    `json:"status"`
}

// ResourceMetadata is the epic-20 metadata envelope.
type ResourceMetadata struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Generation        int    `json:"generation"`
	ResourceVersion   string `json:"resourceVersion"`
	CreationTimestamp string `json:"creationTimestamp,omitempty"`
}

// NetworkSpec is the desired cluster address plan.
type NetworkSpec struct {
	ClusterCIDR      string  `json:"clusterCidr"`
	NodePrefixLength int     `json:"nodePrefixLength"`
	IPv6CIDR         *string `json:"ipv6Cidr"`
}

// NetworkStatus is observed state.
type NetworkStatus struct {
	Phase              string      `json:"phase"`
	ObservedGeneration int         `json:"observedGeneration"`
	Conditions         []Condition `json:"conditions,omitempty"`
}

// Condition is a status condition.
type Condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// NodeLease is a per-node /24 (or nodePrefix) block.
type NodeLease struct {
	NodeID  string `json:"node_id"`
	CIDR    string `json:"cidr"`
	Gateway string `json:"gateway"`
}

// WorkloadLease is a single address inside a node block.
type WorkloadLease struct {
	WorkloadID string `json:"workload_id"`
	NodeID     string `json:"node_id,omitempty"`
	Address    string `json:"address"`
}

// NetworkRow is the persistence row for a Network.
type NetworkRow struct {
	ID               string
	Name             string
	ClusterCIDR      string
	NodePrefixLength int
	IPv6CIDR         *string
	Generation       int
	ResourceVersion  int64
	Phase            string
	ConditionReason  *string
	ConditionMessage *string
	CreatedAt        time.Time
}

// ToEnvelope maps a row to the API envelope.
func (r NetworkRow) ToEnvelope() Network {
	n := Network{
		APIVersion: "network.forge.local/v1",
		Kind:       "Network",
		Metadata: ResourceMetadata{
			ID:                r.ID,
			Name:              r.Name,
			Generation:        r.Generation,
			ResourceVersion:   formatRV(r.ResourceVersion),
			CreationTimestamp: r.CreatedAt.UTC().Format(time.RFC3339),
		},
		Spec: NetworkSpec{
			ClusterCIDR:      r.ClusterCIDR,
			NodePrefixLength: r.NodePrefixLength,
			IPv6CIDR:         r.IPv6CIDR,
		},
		Status: NetworkStatus{
			Phase:              r.Phase,
			ObservedGeneration: r.Generation,
		},
	}
	if r.ConditionReason != nil && *r.ConditionReason != "" {
		msg := ""
		if r.ConditionMessage != nil {
			msg = *r.ConditionMessage
		}
		status := "True"
		if r.Phase == "Ready" {
			status = "False"
		}
		n.Status.Conditions = []Condition{{
			Type:    "Ready",
			Status:  status,
			Reason:  *r.ConditionReason,
			Message: msg,
		}}
		if r.Phase == "Failed" {
			n.Status.Conditions = []Condition{{
				Type:    "Ready",
				Status:  "False",
				Reason:  *r.ConditionReason,
				Message: msg,
			}}
		}
	}
	return n
}

func formatRV(v int64) string {
	return itoA(v)
}

func itoA(v int64) string {
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
