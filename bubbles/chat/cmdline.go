package chat

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ghthor/webtea/bubbles/blokfall"
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
*/
func (m *Client) SetupCmdPalette(additionalCmds ...Cmd) {
	cmds := make([]Cmd, 0, 10)

	// TODO: make help configurable so that blokfall is like a plugin or something
	// help
	cmds = append(cmds, Cmd{
		Use: "help",
		Run: func(cmd *Cmd, args []string) tea.Cmd {
			if !m.blokfallConnected {
				m.cmdLine.Placeholder = ""
				m.chatData.Push(HelpMsg(m.info.Time, m.cmdPalette.Usage()))
			} else if m.blokfallConnected {
				m.chatData.Push(HelpMsg(m.info.Time, strings.TrimLeftFunc(`
Each player controls a single piece. They don't collide till they are locked
into the board enabling pieces to be combined.

    [ d ]  [ f ]   [ g ]     [ j ]  [ k ]
   ←move    move→  soft↓     ↶ CCW   CW ↷

             [__ space __]
             ⤓ hard drop ⤓

-> Available commands:
/exit                      - Exit blokfall
/blokfall reset              - Reset blokfall board
/blokfall debug              - Toggle debugging mode
/blokfall level <INT>        - Set current games level (speed)

`, unicode.IsSpace)))
			}
			return nil
		},
	})

	// exit
	cmds = append(cmds, Cmd{
		Use:     "exit",
		Short:   "Exit the chat, ctrl+c will also exit",
		Aliases: []string{"quit", "q"},
		Run: func(cmd *Cmd, args []string) tea.Cmd {
			switch {
			case m.blokfallConnected:
				return m.exitBlokFallCmd()
			default:
				return tea.Quit
			}
		},
	})

	// names
	cmds = append(cmds, Cmd{
		Use:   "names",
		Short: "List users who are connected.",
		Run: func(cmd *Cmd, args []string) tea.Cmd {
			var (
				req  = NamesReq{Requestor: m.Id()}
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

	// whois
	cmds = append(cmds, Cmd{
		Use:   "whois <USER>",
		Short: "Infomation about USER",
		Run: func(cmd *Cmd, args []string) tea.Cmd {
			if len(args) == 1 {
				m.PrintInfoMsg("argument required: " + cmd.Use)
				return nil
			}

			var (
				req  = WhoisReq{Requestor: m.Id(), User: args[1]}
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
	cmds = append(cmds, Cmd{
		Use:   "quiet",
		Short: "Toggle system announcements.",
		Run: func(cmd *Cmd, args []string) tea.Cmd {
			m.quiet = !m.quiet
			m.chatData.Push(InfoMsg(m.info.Time, fmt.Sprintf("Quiet mode toggled %s", formatToggle(m.quiet))))
			return nil
		},
	})

	// timestamp
	cmds = append(cmds, Cmd{
		// TODO: /timestamp [time|datetime] - Prefix messages with a timestamp. You can also provide the UTC offset: /timestamp time +5h45m
		Use:   "timestamp",
		Short: "Toggle chat timestamps.",
		Run: func(cmd *Cmd, args []string) tea.Cmd {
			m.showTimestamp = !m.showTimestamp
			m.chatData.Push(InfoMsg(m.info.Time, fmt.Sprintf("Timestamp is toggled %s", formatToggle(m.showTimestamp))))
			return nil
		},
	})

	// debug_perf
	cmds = append(cmds, Cmd{
		Use:    "debug_perf <INT>",
		Short:  "send <INT> count of messages in a loop",
		Hidden: true,
		Run: func(cmd *Cmd, args []string) tea.Cmd {
			if len(args) == 1 {
				m.PrintInfoMsg("argument required: " + cmd.Use)
				return nil
			}

			i, err := strconv.Atoi(args[1])
			if err != nil {
				m.PrintInfoMsg(fmt.Sprintf("%s => %v: %s", m.cmdLine.Value(), err, cmd.Use))
				return nil
			}
			return m.sendCountCmd(i)
		},
	})

	cmds = append(cmds, Cmd{
		Use:    "debug",
		Short:  "Toggle debugging mode.",
		Hidden: true,
		Run: func(cmd *Cmd, args []string) tea.Cmd {
			m.debug = !m.debug
			m.cmdPalette.showHidden = m.debug
			m.chatData.Push(InfoMsg(m.info.Time, fmt.Sprintf("Debug is toggled %s", formatToggle(m.debug))))
			return nil
		},
	})

	// blokfall
	cmds = append(cmds, Cmd{
		Use:   "blokfall [exit|reset|debug]",
		Short: "Start/Join multiplayer blokfall.",
		Run: func(cmd *Cmd, args []string) tea.Cmd {
			args1 := ""
			if len(args) > 1 {
				args1 = args[1]
			}

			switch args1 {
			case "":
				if m.blokfallConnected {
					return nil
				}

				m.blokfallConnected = true
				m.cmdLine.Prompt = "blokfall> "
				m.cmdLine.Placeholder = "/ to open command line"
				m.cmdLine.Blur()
				return sendMsgCmd(m.ctx, m.Send, blokfall.MPConnectPlayerMsg(m.Id()))
			case "reset":
				return sendMsgCmd(m.ctx, m.Send, blokfall.GameResetMsg(0))
			case "level":
				lvStr := ""
				if len(args) > 2 {
					lvStr = args[2]
				}
				lv, err := strconv.Atoi(lvStr)
				if err != nil {
					m.PrintErrMsg(err)
					return nil
				}
				return sendMsgCmd(m.ctx, m.Send, blokfall.SetLevelMsg(lv))

			case "debug":
				return sendMsgCmd(m.ctx, m.Send, blokfall.ToggleDebugMsg(0))
			case "exit":
				return m.exitBlokFallCmd()
			default:
			}
			return nil
		},
	})

	cmds = append(cmds, additionalCmds...)

	p := NewCmdPalette("/", cmds...)
	m.cmdPalette = p
}

func (m *Client) exitBlokFallCmd() tea.Cmd {
	m.blokfallConnected = false
	m.cmdLine.Prompt = "> "
	m.cmdLine.Placeholder = ""
	if !m.cmdLine.Focused() {
		return m.cmdLine.Focus()
	}
	return sendMsgCmd(m.ctx, m.Send, blokfall.MPDisconnectPlayerMsg(m.Id()))
}
