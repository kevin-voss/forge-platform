// Package correlation defines the Forge observability correlation contract
// constants used by forge-observe and (by import or copy) other platform services.
//
// Normative human documentation:
// docs/contracts/observability-correlation.md
package correlation

// HTTP headers for cross-service correlation.
const (
	// HeaderTraceparent is the W3C Trace Context header.
	HeaderTraceparent = "traceparent"
	// HeaderRequestID is the Forge edge/request correlation header.
	HeaderRequestID = "X-Forge-Request-ID"
)

// OTEL resource attribute keys (also used as structured log field names where noted).
const (
	AttrProject    = "forge.project"
	AttrDeployment = "forge.deployment"
	AttrService    = "forge.service"
	AttrNode       = "forge.node"
)

// Structured log / query field names for trace and request correlation.
const (
	LogTraceID   = "trace_id"
	LogSpanID    = "span_id"
	LogRequestID = "request_id"
)

// RequiredHeaders lists normative inbound/outbound correlation headers.
var RequiredHeaders = []string{
	HeaderTraceparent,
	HeaderRequestID,
}

// RequiredResourceAttributes lists normative resource attributes attached to
// spans and (as log fields) structured logs.
var RequiredResourceAttributes = []string{
	AttrProject,
	AttrDeployment,
	AttrService,
	AttrNode,
}

// RequiredLogFields lists normative structured-log correlation fields.
var RequiredLogFields = []string{
	LogTraceID,
	LogSpanID,
	LogRequestID,
	AttrProject,
	AttrDeployment,
	AttrService,
	AttrNode,
}
