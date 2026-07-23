package api

import (
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"forge.local/services/forge-network/internal/httperr"
	"forge.local/services/forge-network/internal/network"
	"forge.local/services/forge-network/internal/policy"
)

// PolicyMetrics holds Prometheus-style counters for /metrics.
type PolicyMetrics struct {
	DeniedTotal atomic.Int64
	// Optional shared drift / DNS metrics (22.06); when nil, those series stay at 0.
	Drift *network.DriftMetrics
}

// PolicyRulesHandler serves compiled per-node rules and deny reporting.
type PolicyRulesHandler struct {
	Store    *policy.Store
	Compiler *policy.PolicyCompiler
	Metrics  *PolicyMetrics
	Log      *slog.Logger
}

// Register mounts rules + metrics + placement helper routes.
func (h *PolicyRulesHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/nodes/{node_id}/network-policy-rules", h.getRules)
	mux.HandleFunc("POST /v1/nodes/{node_id}/network-policy-denied", h.reportDenied)
	mux.HandleFunc("PUT /v1/workload-placements/{workload_id}", h.upsertPlacement)
	mux.HandleFunc("GET /metrics", h.metrics)
}

func (h *PolicyRulesHandler) getRules(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("node_id")
	in, gen, err := h.Store.LoadCompileInput(r.Context())
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	rs := h.Compiler.CompileForNode(nodeID, gen, in)
	writeJSON(w, http.StatusOK, rs)
}

type denyReport struct {
	FromWorkload string `json:"from_workload"`
	ToWorkload   string `json:"to_workload"`
	Port         int    `json:"port"`
	Protocol     string `json:"protocol,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

func (h *PolicyRulesHandler) reportDenied(w http.ResponseWriter, r *http.Request) {
	var req denyReport
	if err := decodeJSON(r, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if h.Metrics != nil {
		h.Metrics.DeniedTotal.Add(1)
	}
	if h.Log != nil {
		h.Log.Info("network policy denied",
			"event", "network.policy.denied",
			"node_id", r.PathValue("node_id"),
			"from_workload", req.FromWorkload,
			"to_workload", req.ToWorkload,
			"port", req.Port,
			"protocol", req.Protocol,
			"reason", req.Reason,
		)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "recorded",
		"event":  "network.policy.denied",
	})
}

func (h *PolicyRulesHandler) upsertPlacement(w http.ResponseWriter, r *http.Request) {
	var p policy.WorkloadPlacement
	if err := decodeJSON(r, &p); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	p.WorkloadID = r.PathValue("workload_id")
	if err := h.Store.UpsertPlacement(r.Context(), p); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *PolicyRulesHandler) metrics(w http.ResponseWriter, r *http.Request) {
	denied := int64(0)
	var drift *network.DriftMetrics
	if h.Metrics != nil {
		denied = h.Metrics.DeniedTotal.Load()
		drift = h.Metrics.Drift
	}
	gen := int64(0)
	if h.Store != nil {
		gen, _ = h.Store.Generation(r.Context())
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	var b strings.Builder
	b.WriteString("# HELP forge_network_policy_denied_total Connections denied by NetworkPolicy enforcement\n")
	b.WriteString("# TYPE forge_network_policy_denied_total counter\n")
	b.WriteString("forge_network_policy_denied_total ")
	b.WriteString(formatInt(denied))
	b.WriteByte('\n')
	b.WriteString("# HELP forge_network_policy_rules_generation Current compiled rule-set generation\n")
	b.WriteString("# TYPE forge_network_policy_rules_generation gauge\n")
	b.WriteString("forge_network_policy_rules_generation ")
	b.WriteString(formatInt(gen))
	b.WriteByte('\n')
	routeDrift, dnsOK, dnsErr, dnsNX := int64(0), int64(0), int64(0), int64(0)
	if drift != nil {
		routeDrift = drift.RouteDriftTotal.Load()
		dnsOK = drift.DNSResolutionOK.Load()
		dnsErr = drift.DNSResolutionError.Load()
		dnsNX = drift.DNSResolutionNXDom.Load()
	}
	b.WriteString("# HELP forge_network_route_drift_total Discovery/Network/route drift events\n")
	b.WriteString("# TYPE forge_network_route_drift_total counter\n")
	b.WriteString("forge_network_route_drift_total ")
	b.WriteString(formatInt(routeDrift))
	b.WriteByte('\n')
	b.WriteString("# HELP forge_network_dns_resolution_total Overlay DNS resolution observations\n")
	b.WriteString("# TYPE forge_network_dns_resolution_total counter\n")
	b.WriteString(`forge_network_dns_resolution_total{result="ok"} `)
	b.WriteString(formatInt(dnsOK))
	b.WriteByte('\n')
	b.WriteString(`forge_network_dns_resolution_total{result="error"} `)
	b.WriteString(formatInt(dnsErr))
	b.WriteByte('\n')
	b.WriteString(`forge_network_dns_resolution_total{result="nxdomain"} `)
	b.WriteString(formatInt(dnsNX))
	b.WriteByte('\n')
	_, _ = w.Write([]byte(b.String()))
}

func formatInt(v int64) string {
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
