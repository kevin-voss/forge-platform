package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseCron validates a standard 5-field cron expression (minute hour dom month dow).
// Supported: '*', numbers, lists (1,2,3), ranges (1-5), steps (*/5 or 1-10/2),
// and day-of-week names (SUN–SAT / MON–FRI).
func ParseCron(expr string) (Cron, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return Cron{}, fmt.Errorf("invalid cron %q: expected 5 fields", expr)
	}
	minute, err := parseField(fields[0], 0, 59, nil)
	if err != nil {
		return Cron{}, fmt.Errorf("cron minute: %w", err)
	}
	hour, err := parseField(fields[1], 0, 23, nil)
	if err != nil {
		return Cron{}, fmt.Errorf("cron hour: %w", err)
	}
	dom, err := parseField(fields[2], 1, 31, nil)
	if err != nil {
		return Cron{}, fmt.Errorf("cron day-of-month: %w", err)
	}
	month, err := parseField(fields[3], 1, 12, monthNames)
	if err != nil {
		return Cron{}, fmt.Errorf("cron month: %w", err)
	}
	dow, err := parseField(fields[4], 0, 6, dowNames)
	if err != nil {
		return Cron{}, fmt.Errorf("cron day-of-week: %w", err)
	}
	return Cron{minute: minute, hour: hour, dom: dom, month: month, dow: dow, raw: expr}, nil
}

// Cron is a compiled 5-field expression.
type Cron struct {
	minute, hour, dom, month, dow fieldSet
	raw                           string
}

// Matches reports whether t (already in the desired location) matches the expression.
func (c Cron) Matches(t time.Time) bool {
	if !c.minute.has(t.Minute()) || !c.hour.has(t.Hour()) || !c.month.has(int(t.Month())) {
		return false
	}
	domMatch := c.dom.has(t.Day())
	// Go Sunday=0 … Saturday=6 matches our dow field.
	dowMatch := c.dow.has(int(t.Weekday()))
	domStar := c.dom.all
	dowStar := c.dow.all
	switch {
	case domStar && dowStar:
		return true
	case !domStar && dowStar:
		return domMatch
	case domStar && !dowStar:
		return dowMatch
	default:
		// Both constrained: either may match (standard cron OR semantics).
		return domMatch || dowMatch
	}
}

type fieldSet struct {
	all  bool
	vals map[int]struct{}
}

func (f fieldSet) has(v int) bool {
	if f.all {
		return true
	}
	_, ok := f.vals[v]
	return ok
}

var monthNames = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
}

var dowNames = map[string]int{
	"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

func parseField(raw string, min, max int, names map[string]int) (fieldSet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fieldSet{}, fmt.Errorf("empty field")
	}
	if raw == "*" {
		return fieldSet{all: true}, nil
	}
	out := fieldSet{vals: map[int]struct{}{}}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fieldSet{}, fmt.Errorf("empty list element")
		}
		step := 1
		base := part
		if i := strings.IndexByte(part, '/'); i >= 0 {
			base = part[:i]
			s, err := strconv.Atoi(part[i+1:])
			if err != nil || s < 1 {
				return fieldSet{}, fmt.Errorf("invalid step in %q", part)
			}
			step = s
		}
		var start, end int
		switch {
		case base == "*":
			start, end = min, max
		case strings.Contains(base, "-"):
			bounds := strings.Split(base, "-")
			if len(bounds) != 2 {
				return fieldSet{}, fmt.Errorf("invalid range %q", base)
			}
			var err error
			start, err = parseToken(bounds[0], min, max, names)
			if err != nil {
				return fieldSet{}, err
			}
			end, err = parseToken(bounds[1], min, max, names)
			if err != nil {
				return fieldSet{}, err
			}
			if end < start {
				return fieldSet{}, fmt.Errorf("range start > end in %q", base)
			}
		default:
			v, err := parseToken(base, min, max, names)
			if err != nil {
				return fieldSet{}, err
			}
			start, end = v, v
		}
		for v := start; v <= end; v += step {
			out.vals[v] = struct{}{}
		}
	}
	if len(out.vals) == 0 {
		return fieldSet{}, fmt.Errorf("field matches nothing: %q", raw)
	}
	return out, nil
}

func parseToken(tok string, min, max int, names map[string]int) (int, error) {
	tok = strings.TrimSpace(tok)
	if names != nil {
		if v, ok := names[strings.ToUpper(tok)]; ok {
			return v, nil
		}
	}
	n, err := strconv.Atoi(tok)
	if err != nil {
		return 0, fmt.Errorf("invalid token %q", tok)
	}
	// Allow Sunday as 7 synonym for 0.
	if names != nil && min == 0 && max == 6 && n == 7 {
		n = 0
	}
	if n < min || n > max {
		return 0, fmt.Errorf("value %d out of range [%d,%d]", n, min, max)
	}
	return n, nil
}

// LoadLocation validates a timezone name (empty → UTC).
func LoadLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "UTC") || name == "Z" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid timeZone %q: %w", name, err)
	}
	return loc, nil
}
