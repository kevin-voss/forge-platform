package interactive

import "testing"

func TestDetectWithTTY(t *testing.T) {
	tests := []struct {
		name     string
		noInput  bool
		stdinTTY bool
		env      map[string]string
		want     bool
	}{
		{name: "interactive terminal", stdinTTY: true},
		{name: "no input flag", noInput: true, stdinTTY: true, want: true},
		{name: "piped stdin", stdinTTY: false, want: true},
		{name: "no input environment", stdinTTY: true, env: map[string]string{"FORGE_NO_INPUT": "1"}, want: true},
		{name: "continuous integration", stdinTTY: true, env: map[string]string{"CI": "1"}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guard := DetectWithTTY(test.noInput, test.stdinTTY, func(key string) string {
				return test.env[key]
			})
			if got := guard.NonInteractive(); got != test.want {
				t.Fatalf("NonInteractive() = %v, want %v", got, test.want)
			}
		})
	}
}
