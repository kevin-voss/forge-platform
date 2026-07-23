package logs

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

type cursorPayload struct {
	T string `json:"t"` // RFC3339Nano
	N string `json:"n"` // nanosecond string for stable ordering
}

func encodeCursor(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	raw, _ := json.Marshal(cursorPayload{
		T: t.UTC().Format(time.RFC3339Nano),
		N: fmt.Sprintf("%d", t.UnixNano()),
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cursor encoding")
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return time.Time{}, fmt.Errorf("invalid cursor payload")
	}
	if p.N != "" {
		var ns int64
		if _, err := fmt.Sscanf(p.N, "%d", &ns); err == nil && ns > 0 {
			return time.Unix(0, ns).UTC(), nil
		}
	}
	t, err := time.Parse(time.RFC3339Nano, p.T)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cursor time")
	}
	return t.UTC(), nil
}
