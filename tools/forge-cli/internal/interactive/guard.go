// Package interactive detects when Forge must not solicit terminal input.
package interactive

import (
	"os"
)

// Guard records whether the current invocation must run without prompts.
type Guard struct {
	nonInteractive bool
}

// Detect applies Forge's non-interactive policy. CI and FORGE_NO_INPUT need
// only be set; --no-input explicitly forces the same behavior.
func Detect(noInput bool, stdin *os.File, getenv func(string) string) Guard {
	return DetectWithTTY(noInput, isTTY(stdin), getenv)
}

// DetectWithTTY is Detect with an injectable terminal state for tests.
func DetectWithTTY(noInput, stdinIsTTY bool, getenv func(string) string) Guard {
	return Guard{
		nonInteractive: noInput ||
			!stdinIsTTY ||
			getenv("FORGE_NO_INPUT") != "" ||
			getenv("CI") != "",
	}
}

// NonInteractive reports whether commands must fail instead of prompting.
func (g Guard) NonInteractive() bool {
	return g.nonInteractive
}

func isTTY(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
