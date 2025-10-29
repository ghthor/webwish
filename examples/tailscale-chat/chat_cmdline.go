package main

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

func formatToggle(b bool) string {
	if b {
		return "ON"
	}

	return "OFF"
}

type commandFn func(m *model, cmd, rest string) tea.Cmd

type command struct {
	fn commandFn
}

func mkCommand(f commandFn) command {
	return command{f}
}

/*
	TODO

-> Available commands:
/away [REASON]             - Set away reason, or empty to unset.
/back                      - Clear away status.
/exit                      - Exit the chat.
/focus [USER ...]          - Only show messages from focused users, or $ to reset.
/ignore [USER]             - Hide messages from USER, /unignore USER to stop hiding.
/msg USER MESSAGE          - Send MESSAGE to USER.
/nick NAME                 - Rename yourself.
/reply MESSAGE             - Reply with MESSAGE to the previous private message.
/theme [colors|...]        - Set your color theme.
/whois USER                - Information about USER.
*/
var commands = map[string]command{
	"/help": mkCommand(func(m *model, _, _ string) tea.Cmd {
		m.cmdLine.Placeholder = ""
		m.chatView.Push(chatMsg{
			cliAt: m.time,
			who:   helpNick,
			msg: strings.TrimLeftFunc(`
Type out a message and press <enter> or use a command

-> Available commands:
/names                     - List users who are connected.
/quiet                     - Toggle system announcements.
/timestamp                 - Toggle chat timestamps
/exit                      - Exit the chat (aliases: /quit, /q) Ctrl+c will also quit

-> For input key mappings see:
  - https://github.com/charmbracelet/bubbles/blob/v0.21.0/textinput/textinput.go#L68
`, unicode.IsSpace),
		})
		return nil
	}),

	"/names": mkCommand(func(m *model, cmd, _ string) tea.Cmd {
		var (
			req  = namesMsg{id: m.Id()}
			send = m.Send
		)
		return func() tea.Msg {
			select {
			case <-m.ctx.Done():
			case send <- req:
			}
			return nil
		}
	}),

	"/quiet": mkCommand(func(m *model, cmd, _ string) tea.Cmd {
		m.quiet = !m.quiet
		m.chatView.Push(chatMsg{
			cliAt: m.time,
			who:   infoNick,
			msg:   fmt.Sprintf("Quiet mode toggled %s", formatToggle(m.quiet)),
		})
		return nil
	}),

	"/debug_perf": mkCommand(func(m *model, cmd, args string) tea.Cmd {
		i, err := strconv.Atoi(args)
		if err != nil {
			return m.SendChatCmd(fmt.Sprintf("%s => %v", m.cmdLine.Value(), err))
		}
		return m.SendCountCmd(i)
	}),

	// TODO: /timestamp [time|datetime] - Prefix messages with a timestamp. You can also provide the UTC offset: /timestamp time +5h45m
	"/timestamp": mkCommand(func(m *model, _, _ string) tea.Cmd {
		m.showTimestamp = !m.showTimestamp
		m.chatView.Push(chatMsg{
			cliAt: m.time,
			who:   infoNick,
			msg:   fmt.Sprintf("Timestamp is toggled %s", formatToggle(m.showTimestamp)),
		})
		return nil
	}),

	"/exit": mkCommand(func(m *model, cmd, _ string) tea.Cmd {
		return tea.Quit
	}),
	"/quit": mkCommand(func(m *model, cmd, _ string) tea.Cmd {
		return tea.Quit
	}),
}

func commandSuggestions(cmds map[string]command) []string {
	return slices.Sorted(maps.Keys(cmds))
}
