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

// Schedule is a cron-like override (populated in later steps).
type Schedule struct {
	Name        string `json:"name,omitempty"`
	Cron        string `json:"cron,omitempty"`
	TimeZone    string `json:"timeZone,omitempty"`
	MinReplicas *int   `json:"minReplicas,omitempty"`
	MaxReplicas *int   `json:"maxReplicas,omitempty"`
}

// ScalingPolicySpec is the desired ScalingPolicy configuration.
type ScalingPolicySpec struct {
	TargetRef   TargetRef    `json:"targetRef"`
	MinReplicas int          `json:"minReplicas"`
	MaxReplicas int          `json:"maxReplicas"`
	Metrics     []MetricSpec `json:"metrics"`
	Behavior    Behavior     `json:"behavior"`
	Schedules   []Schedule   `json:"schedules"`
}

// Recommendation records one evaluation observation (no actuation in 24.01).
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

// ScalingPolicyStatus is observed autoscaler state.
type ScalingPolicyStatus struct {
	Phase              string           `json:"phase"`
	ObservedGeneration int              `json:"observedGeneration"`
	CurrentReplicas    int              `json:"currentReplicas"`
	DesiredReplicas    int              `json:"desiredReplicas"`
	LastScaleTime      string           `json:"lastScaleTime,omitempty"`
	LastRecommendation *Recommendation  `json:"lastRecommendation,omitempty"`
	Recommendations    []Recommendation `json:"recommendations"`
	Conditions         []Condition      `json:"conditions"`
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
	return status, nil
}
