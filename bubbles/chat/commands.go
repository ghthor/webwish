package chat

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"
)

type Cmd struct {
	// Use is a short use description. The first word will be used as the
	// command
	Use string

	// Aliases is a list of alternative names for the command
	Aliases []string

	// Short is a description that will be displayed in the help
	Short string

	Hidden bool

	// Run is the function that is executed for the command
	Run func(cmd *Cmd, args []string) tea.Cmd
}

type CmdPalette struct {
	leader string

	cmds    map[string]Cmd
	aliases map[string]Cmd

	showHidden bool

	suggestions []string
}

func NewCmdPalette(leader string, cmds ...Cmd) CmdPalette {
	p := CmdPalette{
		leader:      leader,
		cmds:        make(map[string]Cmd, len(cmds)),
		aliases:     make(map[string]Cmd),
		suggestions: make([]string, 0, len(cmds)),
	}

	for _, cmd := range cmds {
		key, _, _ := strings.Cut(cmd.Use, " ")
		p.cmds[key] = cmd

		if !cmd.Hidden {
			p.suggestions = append(p.suggestions, string(leader)+key)
		}

		for _, alias := range cmd.Aliases {
			p.aliases[alias] = cmd
		}
	}

	sort.Strings(p.suggestions)

	return p
}

func (p CmdPalette) Find(cmd string) *Cmd {
	if c, ok := p.cmds[cmd]; ok {
		return &c
	}

	if c, ok := p.aliases[cmd]; ok {
		return &c
	}

	return nil
}

func (p CmdPalette) Usage() string {
	var b strings.Builder

	fmt.Fprintln(&b, strings.TrimSpace(`
Type out a message and press <enter> or use a command

-> Available commands:
`))

	{
		cmds := slices.Sorted(maps.Keys(p.cmds))
		t := tabwriter.NewWriter(&b, 1, 1, 2, ' ', 0)
		for _, key := range cmds {
			if key == "help" {
				continue
			}

			cmd := p.cmds[key]
			if cmd.Hidden {
				continue
			}

			fmt.Fprintf(t, "%s%s\t- %s", p.leader, cmd.Use, cmd.Short)
			if len(cmd.Aliases) > 0 {
				fmt.Fprintf(t, " (aliases: %s)", strings.Join(cmd.Aliases, ", "))
			}
			fmt.Fprintln(t, "\t")
		}
		t.Flush()
	}

	if p.showHidden {
		fmt.Fprint(&b, `

-> Hidden commands:
`)
		cmds := slices.Sorted(maps.Keys(p.cmds))
		t := tabwriter.NewWriter(&b, 1, 1, 2, ' ', 0)
		for _, key := range cmds {
			if key == "help" {
				continue
			}

			cmd := p.cmds[key]
			if !cmd.Hidden {
				continue
			}

			fmt.Fprintf(t, "%s%s\t- %s", p.leader, cmd.Use, cmd.Short)
			if len(cmd.Aliases) > 0 {
				fmt.Fprintf(t, " (aliases: %s)", strings.Join(cmd.Aliases, ", "))
			}
			fmt.Fprintln(t, "\t")
		}
		t.Flush()
	}

	fmt.Fprint(&b, `
-> For input key mappings see:
  - https://github.com/charmbracelet/bubbles/blob/v0.21.0/textinput/textinput.go#L68
`)

	return b.String()
}

func (p CmdPalette) Suggestions() []string {
	return p.suggestions
}
