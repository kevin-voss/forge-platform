package main

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

func newLogger(serviceName, level string, sensitive []string) *slog.Logger {
	var min slog.Level
	switch strings.ToLower(level) {
	case "debug":
		min = slog.LevelDebug
	case "warn":
		min = slog.LevelWarn
	case "error":
		min = slog.LevelError
	default:
		min = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: min,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			a = replaceLogAttr(groups, a)
			return maskAttr(a, sensitive)
		},
	})
	return slog.New(handler).With("service", serviceName)
}

func replaceLogAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.TimeKey:
		return slog.String("timestamp", a.Value.Time().UTC().Format(time.RFC3339))
	case slog.MessageKey:
		return slog.String("message", a.Value.String())
	case slog.LevelKey:
		level, ok := a.Value.Any().(slog.Level)
		if !ok {
			return slog.String("level", "info")
		}
		switch {
		case level < slog.LevelInfo:
			return slog.String("level", "debug")
		case level < slog.LevelWarn:
			return slog.String("level", "info")
		case level < slog.LevelError:
			return slog.String("level", "warn")
		default:
			return slog.String("level", "error")
		}
	default:
		return a
	}
}

func maskAttr(a slog.Attr, sensitive []string) slog.Attr {
	if len(sensitive) == 0 {
		return a
	}
	switch a.Value.Kind() {
	case slog.KindString:
		masked := maskString(a.Value.String(), sensitive)
		if masked != a.Value.String() {
			return slog.String(a.Key, masked)
		}
	case slog.KindAny:
		if s, ok := a.Value.Any().(string); ok {
			masked := maskString(s, sensitive)
			if masked != s {
				return slog.String(a.Key, masked)
			}
		}
	}
	return a
}

func maskString(s string, sensitive []string) string {
	out := s
	for _, secret := range sensitive {
		if secret == "" {
			continue
		}
		if strings.Contains(out, secret) {
			out = strings.ReplaceAll(out, secret, "***")
		}
	}
	return out
}

// MaskLogLine replaces known secret substrings in a log line (for tests / deploy asserts).
func MaskLogLine(line string, sensitive []string) string {
	return maskString(line, sensitive)
}
