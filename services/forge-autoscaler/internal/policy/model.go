package policy

import (
	"encoding/json"
	"strconv"
	"time"
)

const (
	APIVersion = "forge.dev/v1"
	Kind       = "ScalingPolicy"

	MaxRecommendations = 10
	MaxAuditEntries    = 20

	// Metric outage fallback modes (24.05).
	OutageHold  = "hold"
	OutageFloor = "floor"
	OutageFixed = "fixed"
)

// TargetRef identifies the workload a ScalingPolicy scales.
type TargetRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// MetricSpec configures one metric signal on a ScalingPolicy.
type MetricSpec struct {
	Type                     string   `json:"type"`
	TargetAverageUtilization *float64 `json:"targetAverageUtilization,omitempty"`
	TargetValue              *float64 `json:"targetValue,omitempty"`
	Query                    string   `json:"query,omitempty"`
	Queue                    string   `json:"queue,omitempty"` // queueDepth / worker signals (24.04+)
}

// ScaleBehavior bounds scale-up or scale-down rate.
type ScaleBehavior struct {
	StabilizationWindowSeconds int `json:"stabilizationWindowSeconds"`
	MaxReplicasPerMinute       int `json:"maxReplicasPerMinute"`
}

// Behavior groups scale-up and scale-down settings.
type Behavior struct {
	ScaleUp   ScaleBehavior `json:"scaleUp"`
	ScaleDown ScaleBehavior `json:"scaleDown"`
}

// Schedule is a cron-based min/max override window (24.05).
type Schedule struct {
	Name        string `json:"name,omitempty"`
	Cron        string `json:"cron,omitempty"`
	TimeZone    string `json:"timeZone,omitempty"`
	MinReplicas *int   `json:"minReplicas,omitempty"`
	MaxReplicas *int   `json:"maxReplicas,omitempty"`
	EndTime     string `json:"endTime,omitempty"` // optional RFC3339 absolute end
}

// MetricOutageFallback configures behaviour when all actuable metrics fail (24.05).
type MetricOutageFallback struct {
	Mode          string `json:"mode"` // hold | floor | fixed
	FixedReplicas *int   `json:"fixedReplicas,omitempty"`
}

// DeploymentFreeze configures an operator freeze window (24.05).
// Scale-down is also blocked while the target workload reports an active rollout.
type DeploymentFreeze struct {
	Enabled bool   `json:"enabled,omitempty"`
	Until   string `json:"until,omitempty"` // optional RFC3339 end of freeze window
}

// ScalingPolicySpec is the desired ScalingPolicy configuration.
type ScalingPolicySpec struct {
	TargetRef            TargetRef             `json:"targetRef"`
	MinReplicas          int                   `json:"minReplicas"`
	MaxReplicas          int                   `json:"maxReplicas"`
	Metrics              []MetricSpec          `json:"metrics"`
	Behavior             Behavior              `json:"behavior"`
	Schedules            []Schedule            `json:"schedules"`
	MetricOutageFallback *MetricOutageFallback `json:"metricOutageFallback,omitempty"`
	DeploymentFreeze     *DeploymentFreeze     `json:"deploymentFreeze,omitempty"`
}

// Recommendation records one evaluation observation.
type Recommendation struct {
	MetricType          string   `json:"metricType"`
	MetricValue         *float64 `json:"metricValue"`
	TargetValue         *float64 `json:"targetValue"`
	RecommendedReplicas *int     `json:"recommendedReplicas"`
	Reason              string   `json:"reason"`
	ComputedAt          string   `json:"computedAt"`
}

// Condition is a status condition.
type Condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// ManualOverride is a temporary operator force of desired replicas (24.05).
type ManualOverride struct {
	Replicas  int    `json:"replicas"`
	Reason    string `json:"reason"`
	ExpiresAt string `json:"expiresAt"`
	CreatedAt string `json:"createdAt,omitempty"`
	CreatedBy string `json:"createdBy,omitempty"`
}

// AuditEntry records an override or schedule activation for inspectability.
type AuditEntry struct {
	Type      string `json:"type"`
	At        string `json:"at"`
	Message   string `json:"message"`
	Actor     string `json:"actor,omitempty"`
	Schedule  string `json:"schedule,omitempty"`
	Replicas  *int   `json:"replicas,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// ScalingPolicyStatus is observed autoscaler state.
type ScalingPolicyStatus struct {
	Phase                string           `json:"phase"`
	ObservedGeneration   int              `json:"observedGeneration"`
	CurrentReplicas      int              `json:"currentReplicas"`
	DesiredReplicas      int              `json:"desiredReplicas"`
	LastScaleTime        string           `json:"lastScaleTime,omitempty"`
	LastRecommendation   *Recommendation  `json:"lastRecommendation,omitempty"`
	Recommendations      []Recommendation `json:"recommendations"`
	Conditions           []Condition      `json:"conditions"`
	ManualOverride       *ManualOverride  `json:"manualOverride,omitempty"`
	ActiveSchedules      []string         `json:"activeSchedules,omitempty"`
	EffectiveMinReplicas *int             `json:"effectiveMinReplicas,omitempty"`
	EffectiveMaxReplicas *int             `json:"effectiveMaxReplicas,omitempty"`
	MetricOutageMode     string           `json:"metricOutageMode,omitempty"`
	DeploymentFrozen     bool             `json:"deploymentFrozen,omitempty"`
	Audit                []AuditEntry     `json:"audit,omitempty"`
}

// Metadata carries identity and concurrency fields.
type Metadata struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Project           string `json:"project"`
	Environment       string `json:"environment"`
	Generation        int    `json:"generation"`
	ResourceVersion   string `json:"resourceVersion"`
	CreationTimestamp string `json:"creationTimestamp,omitempty"`
}

// Envelope is the epic-20 style API envelope for ScalingPolicy.
type Envelope struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Metadata   Metadata            `json:"metadata"`
	Spec       ScalingPolicySpec   `json:"spec"`
	Status     ScalingPolicyStatus `json:"status"`
}

// Row is the persistence row.
type Row struct {
	ID              string
	Name            string
	Project         string
	Environment     string
	Generation      int
	ResourceVersion int64
	Spec            ScalingPolicySpec
	Status          ScalingPolicyStatus
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ToEnvelope maps a row to the API envelope.
func (r Row) ToEnvelope() Envelope {
	status := r.Status
	if status.Recommendations == nil {
		status.Recommendations = []Recommendation{}
	}
	if status.Conditions == nil {
		status.Conditions = []Condition{}
	}
	if status.ActiveSchedules == nil {
		status.ActiveSchedules = []string{}
	}
	if status.Audit == nil {
		status.Audit = []AuditEntry{}
	}
	return Envelope{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata: Metadata{
			ID:                r.ID,
			Name:              r.Name,
			Project:           r.Project,
			Environment:       r.Environment,
			Generation:        r.Generation,
			ResourceVersion:   FormatRV(r.ResourceVersion),
			CreationTimestamp: r.CreatedAt.UTC().Format(time.RFC3339),
		},
		Spec:   r.Spec,
		Status: status,
	}
}

// AppendRecommendation adds a recommendation and caps the ring buffer.
func AppendRecommendation(status *ScalingPolicyStatus, rec Recommendation) {
	status.Recommendations = append(status.Recommendations, rec)
	if len(status.Recommendations) > MaxRecommendations {
		status.Recommendations = status.Recommendations[len(status.Recommendations)-MaxRecommendations:]
	}
}

// AppendAudit adds an audit entry and caps the ring buffer.
func AppendAudit(status *ScalingPolicyStatus, entry AuditEntry) {
	if entry.At == "" {
		entry.At = time.Now().UTC().Format(time.RFC3339)
	}
	status.Audit = append(status.Audit, entry)
	if len(status.Audit) > MaxAuditEntries {
		status.Audit = status.Audit[len(status.Audit)-MaxAuditEntries:]
	}
}

// SetCondition upserts a condition by type.
func SetCondition(status *ScalingPolicyStatus, cond Condition) {
	if cond.LastTransitionTime == "" {
		cond.LastTransitionTime = time.Now().UTC().Format(time.RFC3339)
	}
	for i, existing := range status.Conditions {
		if existing.Type == cond.Type {
			if existing.Status == cond.Status && existing.Reason == cond.Reason {
				cond.LastTransitionTime = existing.LastTransitionTime
			}
			status.Conditions[i] = cond
			return
		}
	}
	status.Conditions = append(status.Conditions, cond)
}

// DefaultStatus returns the initial status for a newly created policy.
func DefaultStatus(generation int) ScalingPolicyStatus {
	now := time.Now().UTC().Format(time.RFC3339)
	return ScalingPolicyStatus{
		Phase:              "Pending",
		ObservedGeneration: generation,
		CurrentReplicas:    0,
		DesiredReplicas:    0,
		Recommendations:    []Recommendation{},
		ActiveSchedules:    []string{},
		Audit:              []AuditEntry{},
		Conditions: []Condition{
			{
				Type:               "AbleToScale",
				Status:             "True",
				Reason:             "ReadyForScaling",
				LastTransitionTime: now,
			},
			{
				Type:               "ScalingActive",
				Status:             "False",
				Reason:             "AwaitingFirstEvaluation",
				LastTransitionTime: now,
			},
			{
				Type:               "ScheduleConflict",
				Status:             "False",
				Reason:             "NoConflict",
				LastTransitionTime: now,
			},
		},
	}
}

// FormatRV formats a resource version as a decimal string.
func FormatRV(v int64) string {
	return strconv.FormatInt(v, 10)
}

// ParseRV parses a resource version string.
func ParseRV(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func marshalSpec(spec ScalingPolicySpec) ([]byte, error) {
	if spec.Metrics == nil {
		spec.Metrics = []MetricSpec{}
	}
	if spec.Schedules == nil {
		spec.Schedules = []Schedule{}
	}
	return json.Marshal(spec)
}

func unmarshalSpec(raw []byte) (ScalingPolicySpec, error) {
	var spec ScalingPolicySpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return ScalingPolicySpec{}, err
	}
	if spec.Metrics == nil {
		spec.Metrics = []MetricSpec{}
	}
	if spec.Schedules == nil {
		spec.Schedules = []Schedule{}
	}
	return spec, nil
}

func marshalStatus(status ScalingPolicyStatus) ([]byte, error) {
	if status.Recommendations == nil {
		status.Recommendations = []Recommendation{}
	}
	if status.Conditions == nil {
		status.Conditions = []Condition{}
	}
	if status.ActiveSchedules == nil {
		status.ActiveSchedules = []string{}
	}
	if status.Audit == nil {
		status.Audit = []AuditEntry{}
	}
	return json.Marshal(status)
}

func unmarshalStatus(raw []byte) (ScalingPolicyStatus, error) {
	var status ScalingPolicyStatus
	if len(raw) == 0 || string(raw) == "{}" {
		return DefaultStatus(0), nil
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return ScalingPolicyStatus{}, err
	}
	if status.Recommendations == nil {
		status.Recommendations = []Recommendation{}
	}
	if status.Conditions == nil {
		status.Conditions = []Condition{}
	}
	if status.ActiveSchedules == nil {
		status.ActiveSchedules = []string{}
	}
	if status.Audit == nil {
		status.Audit = []AuditEntry{}
	}
	return status, nil
}
