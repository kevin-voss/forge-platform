package natsx

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// StreamSpec describes a platform JetStream stream.
type StreamSpec struct {
	Name       string
	Subjects   []string
	Duplicates time.Duration // JetStream publish dedup window (msg-id)
}

// SpecForName returns the default subject pattern for a family stream name.
func SpecForName(name string) StreamSpec {
	return SpecForNameWithDedup(name, 0)
}

// SpecForNameWithDedup returns a family stream spec with an optional dedup window.
func SpecForNameWithDedup(name string, duplicates time.Duration) StreamSpec {
	return StreamSpec{
		Name:       name,
		Subjects:   []string{name + ".>"},
		Duplicates: duplicates,
	}
}

// SpecsForNames builds stream specs for the configured stream list.
func SpecsForNames(names []string) []StreamSpec {
	return SpecsForNamesWithDedup(names, 0)
}

// SpecsForNamesWithDedup builds family stream specs with a shared dedup window.
func SpecsForNamesWithDedup(names []string, duplicates time.Duration) []StreamSpec {
	out := make([]StreamSpec, 0, len(names))
	for _, name := range names {
		out = append(out, SpecForNameWithDedup(name, duplicates))
	}
	return out
}

// DLQStreamName returns the JetStream stream name for a family's dead-letter queue.
func DLQStreamName(family string) string {
	return "dlq_" + family
}

// DLQSpecForFamily returns the DLQ stream spec for a source family
// (subjects dlq.<family>.>).
func DLQSpecForFamily(family string) StreamSpec {
	return StreamSpec{
		Name:     DLQStreamName(family),
		Subjects: []string{"dlq." + family + ".>"},
	}
}

// DLQSpecsForFamilies builds DLQ stream specs for each source family.
func DLQSpecsForFamilies(families []string) []StreamSpec {
	out := make([]StreamSpec, 0, len(families))
	for _, f := range families {
		out = append(out, DLQSpecForFamily(f))
	}
	return out
}

// BootstrapSpecs returns platform streams plus optional DLQ streams.
func BootstrapSpecs(families []string, dlqEnabled bool) []StreamSpec {
	return BootstrapSpecsWithDedup(families, dlqEnabled, 0)
}

// BootstrapSpecsWithDedup returns platform (+ optional DLQ) streams with a dedup window
// applied to source family streams (not DLQ streams).
func BootstrapSpecsWithDedup(families []string, dlqEnabled bool, duplicates time.Duration) []StreamSpec {
	out := SpecsForNamesWithDedup(families, duplicates)
	if dlqEnabled {
		out = append(out, DLQSpecsForFamilies(families)...)
	}
	return out
}

// StreamNames returns the JetStream stream names that must be present when ready.
func StreamNames(families []string, dlqEnabled bool) []string {
	out := append([]string(nil), families...)
	if dlqEnabled {
		for _, f := range families {
			out = append(out, DLQStreamName(f))
		}
	}
	return out
}

// BootstrapStreams idempotently ensures each stream exists with compatible subjects.
// A pre-existing stream with a matching subject set is accepted; create-if-absent otherwise.
func BootstrapStreams(js nats.JetStreamContext, specs []StreamSpec, log *slog.Logger) error {
	if js == nil {
		return fmt.Errorf("jetstream context is nil")
	}
	if log == nil {
		log = slog.Default()
	}
	for _, spec := range specs {
		if err := ensureStream(js, spec, log); err != nil {
			return err
		}
	}
	return nil
}

func ensureStream(js nats.JetStreamContext, spec StreamSpec, log *slog.Logger) error {
	info, err := js.StreamInfo(spec.Name)
	if err == nil {
		if !subjectsCompatible(info.Config.Subjects, spec.Subjects) {
			return fmt.Errorf("stream %q exists with incompatible subjects %v (want %v)",
				spec.Name, info.Config.Subjects, spec.Subjects)
		}
		if err := ensureDuplicates(js, info, spec.Duplicates, log); err != nil {
			return err
		}
		log.Info("stream already present",
			"stream", spec.Name,
			"subjects", strings.Join(info.Config.Subjects, ","),
			"duplicates_s", int(spec.Duplicates.Seconds()),
		)
		return nil
	}
	if err != nats.ErrStreamNotFound {
		return fmt.Errorf("stream info %q: %w", spec.Name, err)
	}

	cfg := &nats.StreamConfig{
		Name:      spec.Name,
		Subjects:  spec.Subjects,
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}
	if spec.Duplicates > 0 {
		cfg.Duplicates = spec.Duplicates
	}
	_, err = js.AddStream(cfg)
	if err != nil {
		// Race: another process created the stream between info and add.
		if err == nats.ErrStreamNameAlreadyInUse {
			info, infoErr := js.StreamInfo(spec.Name)
			if infoErr != nil {
				return fmt.Errorf("stream %q already in use but info failed: %w", spec.Name, infoErr)
			}
			if !subjectsCompatible(info.Config.Subjects, spec.Subjects) {
				return fmt.Errorf("stream %q exists with incompatible subjects %v (want %v)",
					spec.Name, info.Config.Subjects, spec.Subjects)
			}
			if err := ensureDuplicates(js, info, spec.Duplicates, log); err != nil {
				return err
			}
			log.Info("stream already present",
				"stream", spec.Name,
				"subjects", strings.Join(info.Config.Subjects, ","),
			)
			return nil
		}
		return fmt.Errorf("create stream %q: %w", spec.Name, err)
	}
	log.Info("stream created",
		"stream", spec.Name,
		"subjects", strings.Join(spec.Subjects, ","),
		"duplicates_s", int(spec.Duplicates.Seconds()),
	)
	return nil
}

func ensureDuplicates(js nats.JetStreamContext, info *nats.StreamInfo, want time.Duration, log *slog.Logger) error {
	if info == nil || want <= 0 {
		return nil
	}
	if info.Config.Duplicates == want {
		return nil
	}
	cfg := info.Config
	cfg.Duplicates = want
	if _, err := js.UpdateStream(&cfg); err != nil {
		return fmt.Errorf("update stream %q duplicates: %w", info.Config.Name, err)
	}
	log.Info("stream duplicates window updated",
		"stream", info.Config.Name,
		"duplicates_s", int(want.Seconds()),
	)
	return nil
}

// StreamsPresent reports whether every named stream exists.
func StreamsPresent(js nats.JetStreamContext, names []string) error {
	if js == nil {
		return fmt.Errorf("jetstream context is nil")
	}
	for _, name := range names {
		if _, err := js.StreamInfo(name); err != nil {
			return fmt.Errorf("stream %q: %w", name, err)
		}
	}
	return nil
}

func subjectsCompatible(have, want []string) bool {
	if len(have) != len(want) {
		return false
	}
	set := make(map[string]struct{}, len(have))
	for _, s := range have {
		set[s] = struct{}{}
	}
	for _, s := range want {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}
