package model

import "testing"

func TestStateVersions(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  StateVersion
		valid bool
	}{
		{"legacy", "2", StateVersionLegacy, true},
		{"portable", "3", StateVersionPortable, true},
		{"future", "4", 0, false},
		{"missing", "", 0, false},
		{"invalid", "portable", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := ParseStateVersion(test.input)
			if (err == nil) != test.valid {
				t.Fatalf("version %q: %v", test.input, err)
			}
			if test.valid && (value != test.want || value.String() != test.input) {
				t.Fatalf("unexpected version %v", value)
			}
		})
	}
}
