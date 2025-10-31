package main

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ghthor/webwish/bubbles/chat"
	"github.com/ghthor/webwish/bubbles/tetris"
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
		if !m.tetrisConnected {
			m.cmdLine.Placeholder = ""
			m.chatView.Push(chat.HelpMsg(m.Time, strings.TrimLeftFunc(`
Type out a message and press <enter> or use a command

-> Available commands:
/names                     - List users who are connected.
/quiet                     - Toggle system announcements.
/timestamp                 - Toggle chat timestamps
/tetris                    - Start/Join chat plays tetris
/exit                      - Exit the chat (aliases: /quit, /q) Ctrl+c will also quit

-> For input key mappings see:
  - https://github.com/charmbracelet/bubbles/blob/v0.21.0/textinput/textinput.go#L68
`, unicode.IsSpace)))
		} else if m.tetrisConnected {
			m.chatView.Push(chat.HelpMsg(m.Time, strings.TrimLeftFunc(`
Input is queued until >50% of players have chosen/voted for the same input

    [ d ]  [ f ]       [ j ]  [ k ]
   ←move    move→      ↶ CCW   CW ↷

             [__ space __]
             ⤓ hard drop ⤓

-> Available commands:
/exit                      - Exit tetris

`, unicode.IsSpace)))
		}
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
		m.chatView.Push(chat.InfoMsg(m.Time, fmt.Sprintf("Quiet mode toggled %s", formatToggle(m.quiet))))
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
		m.chatView.Push(chat.InfoMsg(m.Time, fmt.Sprintf("Timestamp is toggled %s", formatToggle(m.showTimestamp))))
		return nil
	}),
	"/tetris": mkCommand(func(m *model, _, args string) tea.Cmd {
		switch args {
		case "":
			if m.tetrisConnected {
				return nil
			}

			m.tetrisConnected = true
			m.cmdLine.Prompt = "tetris> "
			m.cmdLine.Placeholder = "/ to open command line"
			m.cmdLine.Blur()
			return sendMsgCmd(m.ctx, m.Send, tetris.MPConnectPlayerMsg(m.Id()))
		case "stop":
			return m.exitTetrisCmd()
		default:
		}
		return nil
	}),

	"/exit": exitCommand,
	"/quit": exitCommand,
}

func (m *model) exitTetrisCmd() tea.Cmd {
	m.tetrisConnected = false
	m.cmdLine.Prompt = "> "
	m.cmdLine.Placeholder = ""
	if !m.cmdLine.Focused() {
		return m.cmdLine.Focus()
	}
	return sendMsgCmd(m.ctx, m.Send, tetris.MPDisconnectPlayerMsg(m.Id()))
}

var exitCommand = mkCommand(func(m *model, cmd, _ string) tea.Cmd {
	switch {
	case m.tetrisConnected:
		return m.exitTetrisCmd()
	default:
		return tea.Quit
	}
})

func commandSuggestions(cmds map[string]command) []string {
	return slices.Sorted(maps.Keys(cmds))
}
