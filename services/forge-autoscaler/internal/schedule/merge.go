package schedule

import (
	"fmt"
	"strings"
	"time"
)

// Spec is a cron-based min/max override window (mirrors policy.Schedule fields).
type Spec struct {
	Name        string
	Cron        string
	TimeZone    string
	MinReplicas *int
	MaxReplicas *int
	EndTime     string
}

// Bounds is the effective replica floor/ceiling after schedule merge.
type Bounds struct {
	Min             int
	Max             int
	Active          []string
	Conflict        bool
	ConflictMessage string
}

// ActiveBounds merges active schedules at now.
// Active schedules contribute their min/max overrides. Conflicting schedules take
// the highest minReplicas and lowest maxReplicas that still satisfy min <= max;
// otherwise Conflict is true and the policy base min/max are returned.
func ActiveBounds(baseMin, baseMax int, schedules []Spec, now time.Time) Bounds {
	out := Bounds{Min: baseMin, Max: baseMax, Active: []string{}}
	if len(schedules) == 0 {
		return out
	}

	type active struct {
		name string
		min  *int
		max  *int
	}
	var actives []active
	for i, sch := range schedules {
		ok, err := IsActive(sch, now)
		if err != nil || !ok {
			continue
		}
		name := strings.TrimSpace(sch.Name)
		if name == "" {
			name = fmt.Sprintf("schedule-%d", i)
		}
		actives = append(actives, active{name: name, min: sch.MinReplicas, max: sch.MaxReplicas})
		out.Active = append(out.Active, name)
	}
	if len(actives) == 0 {
		return out
	}

	min := baseMin
	max := baseMax
	minRaised := false
	maxLowered := false
	for _, a := range actives {
		if a.min != nil {
			if !minRaised || *a.min > min {
				min = *a.min
				minRaised = true
			}
		}
		if a.max != nil {
			if !maxLowered || *a.max < max {
				max = *a.max
				maxLowered = true
			}
		}
	}
	if !minRaised {
		min = baseMin
	}
	if !maxLowered {
		max = baseMax
	}
	if min > max {
		out.Conflict = true
		out.ConflictMessage = fmt.Sprintf("active schedules require minReplicas=%d > maxReplicas=%d", min, max)
		out.Min = baseMin
		out.Max = baseMax
		return out
	}
	out.Min = min
	out.Max = max
	return out
}

// IsActive reports whether a schedule applies at now.
func IsActive(sch Spec, now time.Time) (bool, error) {
	cronExpr := strings.TrimSpace(sch.Cron)
	if cronExpr == "" {
		return false, fmt.Errorf("schedule cron is required")
	}
	if end := strings.TrimSpace(sch.EndTime); end != "" {
		endAt, err := time.Parse(time.RFC3339, end)
		if err != nil {
			return false, fmt.Errorf("invalid endTime: %w", err)
		}
		if !now.UTC().Before(endAt.UTC()) {
			return false, nil
		}
	}
	loc, err := LoadLocation(sch.TimeZone)
	if err != nil {
		return false, err
	}
	compiled, err := ParseCron(cronExpr)
	if err != nil {
		return false, err
	}
	return compiled.Matches(now.In(loc)), nil
}

// ValidateSchedule validates cron, timezone, and optional endTime at admission.
func ValidateSchedule(sch Spec) error {
	if strings.TrimSpace(sch.Cron) == "" {
		return fmt.Errorf("schedules[].cron is required")
	}
	if _, err := ParseCron(sch.Cron); err != nil {
		return err
	}
	if _, err := LoadLocation(sch.TimeZone); err != nil {
		return err
	}
	if end := strings.TrimSpace(sch.EndTime); end != "" {
		if _, err := time.Parse(time.RFC3339, end); err != nil {
			return fmt.Errorf("schedules[].endTime must be RFC3339: %w", err)
		}
	}
	if sch.MinReplicas == nil && sch.MaxReplicas == nil {
		return fmt.Errorf("schedules[] must set minReplicas and/or maxReplicas")
	}
	if sch.MinReplicas != nil && *sch.MinReplicas < 0 {
		return fmt.Errorf("schedules[].minReplicas must be >= 0")
	}
	if sch.MaxReplicas != nil && *sch.MaxReplicas < 1 {
		return fmt.Errorf("schedules[].maxReplicas must be >= 1")
	}
	if sch.MinReplicas != nil && sch.MaxReplicas != nil && *sch.MinReplicas > *sch.MaxReplicas {
		return fmt.Errorf("schedules[].minReplicas must be <= maxReplicas")
	}
	return nil
}
