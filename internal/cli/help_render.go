package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// shownOption is an option as one command's help presents it.
type shownOption struct {
	name, label, usage, group string
}

// helpOptions returns the options of c, or of one of its actions, in display order.
func helpOptions(c *command, action *helpAction) []shownOption {
	var shown []shownOption
	for _, name := range c.options {
		if action == nil || slices.Contains(action.options, name) {
			shown = append(shown, c.show(optionIndex[name]))
		}
	}
	for i := range options {
		if options[i].group == groupGlobal {
			shown = append(shown, c.show(&options[i]))
		}
	}
	return shown
}

// show applies the command's overrides to o.
func (c *command) show(o *option) shownOption {
	h := c.overrides[o.name]
	var spellings []string
	for _, name := range append(slices.Clone(o.aliases), o.name) {
		if len(name) == 1 {
			spellings = append(spellings, "-"+name)
		} else {
			spellings = append(spellings, "--"+name)
		}
	}
	label := strings.Join(spellings, ", ")
	if value := cmp.Or(h.value, o.value); value != "" {
		label += " " + value
	}
	return shownOption{name: o.name, label: label, usage: cmp.Or(h.usage, o.usage), group: cmp.Or(h.group, o.group)}
}

func (c *command) action(name string) *helpAction {
	for i := range c.actions {
		if c.actions[i].name == name {
			return &c.actions[i]
		}
	}
	return nil
}

const (
	helpWidth  = 79
	helpColumn = 28
)

func helpEntry(label, description string) string {
	first := "  " + label
	if len(first)+2 > helpColumn {
		return first + "\n" + wrapHelp(description, strings.Repeat(" ", helpColumn), helpWidth)
	}
	first += strings.Repeat(" ", helpColumn-len(first))
	return wrapHanging(description, first, strings.Repeat(" ", helpColumn), helpWidth)
}

func CommandUsage(name string) string { return CommandActionUsage(name, "") }

func CommandActionUsage(name, action string) string {
	c, ok := lookupCommand(name)
	if !ok {
		c = &command{name: name}
	}
	synopsis, description, examples := c.help.synopsis, c.help.description, c.help.examples
	if name == "state" && action == "" {
		action = "migrate"
	}
	selected := c.action(action)
	if selected != nil {
		name += " " + action
		synopsis = []string{selected.arguments}
		description = selected.description
		examples = nil
		for _, example := range c.help.examples {
			if strings.HasPrefix(example, "glesha "+name) {
				examples = append(examples, example)
			}
		}
		if len(examples) == 0 {
			example := "glesha " + name
			if selected.arguments != "" {
				example += " " + strings.Trim(selected.arguments, "[]")
			}
			examples = []string{example}
		}
	}
	if len(synopsis) == 0 {
		synopsis = []string{""}
	}
	var out strings.Builder
	for i, line := range synopsis {
		prefix := "usage: "
		if i > 0 {
			prefix = "       "
		}
		out.WriteString(wrapHanging(strings.TrimSpace("glesha "+name+" "+line)+" [options]", prefix, "         ", helpWidth) + "\n")
	}
	for _, paragraph := range description {
		out.WriteString("\n" + wrapHelp(paragraph, "", helpWidth) + "\n")
	}
	if selected == nil && len(c.actions) > 0 {
		out.WriteString("\nActions:\n")
		for _, entry := range c.actions {
			out.WriteString(helpEntry(strings.TrimSpace(entry.name+" "+entry.arguments), entry.summary) + "\n")
		}
	}
	shown := helpOptions(c, selected)
	for _, group := range helpGroupOrder {
		var lines strings.Builder
		for _, o := range shown {
			if o.group == group {
				lines.WriteString(helpEntry(o.label, o.usage) + "\n")
			}
		}
		if lines.Len() == 0 {
			continue
		}
		fmt.Fprintf(&out, "\n%s:\n%s", group, lines.String())
		if group == groupRemote {
			out.WriteString("\n" + wrapHelp(remoteSelectionNote, "  ", helpWidth) + "\n")
		}
	}
	out.WriteString("\nExamples:\n")
	for _, example := range examples {
		out.WriteString(wrapExample(example) + "\n")
	}
	return out.String()
}

const remoteSelectionNote = "REMOTE... takes a comma-separated list and may be repeated. If --from or --to is given, no other remote is accessed."

func knownHelpAction(command, action string) bool {
	c, ok := lookupCommand(command)
	return ok && c.action(action) != nil
}
