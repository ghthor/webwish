package chat

import (
	"sort"
	"strings"

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
	leader rune

	cmds    map[string]Cmd
	aliases map[string]Cmd

	suggestions []string
}

func NewCmdPalette(leader rune, cmds ...Cmd) CmdPalette {
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

func (p CmdPalette) Suggestions() []string {
	return p.suggestions
}
