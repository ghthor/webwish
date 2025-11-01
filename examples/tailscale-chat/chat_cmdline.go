package main

import (
	"fmt"
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

/*
	TODO

-> Available commands:
/away [REASON]             - Set away reason, or empty to unset.
/back                      - Clear away status.
/focus [USER ...]          - Only show messages from focused users, or $ to reset.
/ignore [USER]             - Hide messages from USER, /unignore USER to stop hiding.
/msg USER MESSAGE          - Send MESSAGE to USER.
/nick NAME                 - Rename yourself.
/reply MESSAGE             - Reply with MESSAGE to the previous private message.
/theme [colors|...]        - Set your color theme.
/whois USER                - Information about USER.
*/
func (m *Model) SetupCmdPalette() {
	cmds := make([]chat.Cmd, 0, 10)

	// help
	cmds = append(cmds, chat.Cmd{
		// TODO: auto generated help from the pallete
		Use: "help",
		Run: func(cmd *chat.Cmd, args []string) tea.Cmd {
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
		},
	})

	// exit
	cmds = append(cmds, chat.Cmd{
		Use:     "exit",
		Aliases: []string{"quit", "q"},
		Run: func(cmd *chat.Cmd, args []string) tea.Cmd {
			switch {
			case m.tetrisConnected:
				return m.exitTetrisCmd()
			default:
				return tea.Quit
			}
		},
	})

	// names
	cmds = append(cmds, chat.Cmd{
		Use: "names",
		Run: func(cmd *chat.Cmd, args []string) tea.Cmd {
			var (
				req  = chat.NamesReq{Requestor: m.Id()}
				send = m.Send
			)
			return func() tea.Msg {
				select {
				case <-m.ctx.Done():
				case send <- req:
				}
				return nil
			}
		},
	})

	// quiet
	cmds = append(cmds, chat.Cmd{
		Use: "quiet",
		Run: func(cmd *chat.Cmd, args []string) tea.Cmd {
			m.quiet = !m.quiet
			m.chatView.Push(chat.InfoMsg(m.Time, fmt.Sprintf("Quiet mode toggled %s", formatToggle(m.quiet))))
			return nil
		},
	})

	// timestamp
	cmds = append(cmds, chat.Cmd{
		// TODO: /timestamp [time|datetime] - Prefix messages with a timestamp. You can also provide the UTC offset: /timestamp time +5h45m
		Use: "timestamp",
		Run: func(cmd *chat.Cmd, args []string) tea.Cmd {
			m.showTimestamp = !m.showTimestamp
			m.chatView.Push(chat.InfoMsg(m.Time, fmt.Sprintf("Timestamp is toggled %s", formatToggle(m.showTimestamp))))
			return nil
		},
	})

	// debug_perf
	cmds = append(cmds, chat.Cmd{
		Use:    "debug_perf [INT]",
		Hidden: true,
		Run: func(cmd *chat.Cmd, args []string) tea.Cmd {
			if len(args) == 1 {
				return m.SendChatCmd("argument required: " + cmd.Use)
			}

			i, err := strconv.Atoi(args[1])
			if err != nil {
				return m.SendChatCmd(fmt.Sprintf("%s => %v: %s", m.cmdLine.Value(), err, cmd.Use))
			}
			return m.SendCountCmd(i)
		},
	})

	// tetris
	cmds = append(cmds, chat.Cmd{
		Use: "tetris",
		Run: func(cmd *chat.Cmd, args []string) tea.Cmd {
			argsStr := ""
			if len(args) > 1 {
				argsStr = strings.Join(args[1:], " ")
			}

			switch argsStr {
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
		},
	})

	p := chat.NewCmdPalette('/', cmds...)
	m.cmdPalette = p
}

func (m *Model) exitTetrisCmd() tea.Cmd {
	m.tetrisConnected = false
	m.cmdLine.Prompt = "> "
	m.cmdLine.Placeholder = ""
	if !m.cmdLine.Focused() {
		return m.cmdLine.Focus()
	}
	return sendMsgCmd(m.ctx, m.Send, tetris.MPDisconnectPlayerMsg(m.Id()))
}
