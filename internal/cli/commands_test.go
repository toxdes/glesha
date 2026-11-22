package cli

import (
	"slices"
	"testing"
)

func TestCommandTableIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range commands {
		t.Run(c.name, func(t *testing.T) {
			if seen[c.name] {
				t.Fatal("duplicate command")
			}
			seen[c.name] = true
			if c.help.summary == "" {
				t.Error("missing summary")
			}
			accepted := map[string]bool{}
			for _, name := range c.options {
				o, ok := optionIndex[name]
				switch {
				case !ok:
					t.Errorf("unknown option %q", name)
				case o.name != name:
					t.Errorf("option %q is an alias; list %q", name, o.name)
				case o.group == groupGlobal:
					t.Errorf("global option %q is accepted everywhere; do not list it", name)
				case accepted[name]:
					t.Errorf("duplicate option %q", name)
				}
				accepted[name] = true
			}
			for name := range c.overrides {
				if !accepted[name] && !globalOption(name) {
					t.Errorf("override for option %q, which %s does not accept", name, c.name)
				}
			}
			for _, a := range c.actions {
				for _, name := range a.options {
					if !slices.Contains(c.options, name) {
						t.Errorf("action %s lists option %q, which %s does not accept", a.name, name, c.name)
					}
				}
			}
		})
	}
	spellings := 0
	for _, o := range options {
		spellings += 1 + len(o.aliases)
	}
	if len(optionIndex) != spellings {
		t.Errorf("option names or aliases collide: %d spellings, %d distinct", spellings, len(optionIndex))
	}
}
