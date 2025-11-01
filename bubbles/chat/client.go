package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/log"
	"github.com/ghthor/webwish/bubbles/tetris"
	"github.com/ghthor/webwish/mpty"
	"github.com/ghthor/webwish/teamodel"
	"github.com/ghthor/webwish/unsafering"
	overlay "github.com/rmhubbert/bubbletea-overlay"
)

var (
	Bold       = lipgloss.NewStyle().Bold(true)
	None       = lipgloss.NewStyle()
	AlignRight = lipgloss.NewStyle().Align(lipgloss.Right)

	StyleSystem    = lipgloss.NewStyle().Faint(true)
	StyleSystemWho = StyleSystem.Align(lipgloss.Left)
	StyleSystemMsg = StyleSystem
)

func NewClient(ctx context.Context, info *mpty.ClientInfoModel) *Client {
	return &Client{
		ctx: ctx,

		ClientInfoModel: info,

		table:    table.New(),
		chatView: unsafering.New[Msg](300),
	}
}

type Client struct {
	*mpty.ClientInfoModel

	b strings.Builder

	Send mpty.Input

	ctx context.Context

	cmds []tea.Cmd

	cmdLine    textinput.Model
	cmdPalette CmdPalette

	table *table.Table
	view  viewport.Model

	chatView *unsafering.Buffer[Msg]

	tetrisView      tetris.MPView
	tetrisConnected bool

	overlay *overlay.Model

	quiet         bool
	showTimestamp bool

	err error
}

var _ table.Data = &Client{}
var _ mpty.ClientModel = &Client{}

func (m *Client) Err() error {
	return m.err
}

func (m *Client) AtRaw(row int) Msg {
	msg, _ := m.chatView.AtInWindow(row, m.chatView.Len())
	return msg
}

const (
	COL_TS = iota
	COL_WHO
	COL_MSG
	COL_SZ
)

func (m *Client) At(row, cell int) string {
	if !m.showTimestamp {
		cell++
	}

	msg := m.AtRaw(row)
	switch cell {
	case COL_TS:
		if msg.ServerAt.IsZero() {
			return ""
		}
		return msg.ServerAt.Format(time.TimeOnly)
	case COL_WHO:
		return " " + msg.Nick()
	case COL_MSG:
		return " | " + msg.Str
	default:
	}

	return ""
}

func (m *Client) Rows() int {
	return m.chatView.Len()
}
func (m *Client) Columns() int {
	if m.showTimestamp {
		return 3
	} else {
		return 2
	}
}

func (m *Client) SetTableOffset() {
	m.table.Offset(max(0, m.chatView.Len()-m.ChatViewHeight()-1))
}

func (m *Client) Init() tea.Cmd {
	if m.cmds == nil {
		m.cmds = make([]tea.Cmd, 0, 1)
	}

	m.SetupCmdPalette()

	// TODO: dynamic suggestions
	m.cmdLine = textinput.New()
	m.cmdLine.Prompt = "> "
	m.cmdLine.Placeholder = "/help"
	m.cmdLine.CharLimit = 0
	m.cmdLine.ShowSuggestions = true

	m.table = m.table.
		Headers().Data(m).
		Border(lipgloss.Border{}).
		Wrap(true).
		StyleFunc(func(row, col int) lipgloss.Style {
			msg := m.AtRaw(row)

			if !m.showTimestamp {
				col++
			}

			switch col {
			case COL_WHO:
				switch msg.Who {
				case SysNick, InfoNick, HelpNick:
					return StyleSystemWho
				}

				return AlignRight
			case COL_MSG:
				switch msg.Who {
				case SysNick, InfoNick, HelpNick:
					return StyleSystemMsg
				}
			default:
			}

			return None
		})

	m.view = viewport.New(m.Width, m.ChatViewHeight())

	m.overlay = overlay.New(nil, nil, overlay.Right, overlay.Center, -10, 0)

	return tea.Batch(m.cmdLine.Focus())
}

func (m *Client) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m.UpdateClient(msg)
}

func (m *Client) UpdateClient(msg tea.Msg) (mpty.ClientModel, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds = m.cmds[:0]
	)

	m.ClientInfoModel, cmd = m.UpdateInfo(msg)
	cmds = append(cmds, cmd)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.cmdLine.Width = msg.Width
		m.ViewportResize()
		m.SetTableOffset()

	case mpty.Input:
		m.Send = msg

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			cmds = append(cmds, m.CmdLineExecute())
			if m.tetrisConnected && m.cmdLine.Focused() {
				m.cmdLine.Blur()
			}
		case "/":
			if m.tetrisConnected && !m.cmdLine.Focused() {
				cmds = append(cmds, m.cmdLine.Focus())
			}
		}

	case tetris.MPView:
		m.tetrisView = msg

	case []tea.Msg:
		for _, msg := range msg {
			switch msg := msg.(type) {
			case time.Time:
				m.ClientInfoModel, cmd = m.UpdateInfo(msg)
				cmds = append(cmds, cmd)
			case Msg:
				if m.quiet && msg.Who == SysNick {
				} else {
					m.chatView.Push(msg)
				}
			case NamesReq:
				if msg.Requestor == m.Id() {
					m.chatView.Push(SysMsg(m.Time,
						fmt.Sprintf("-> %d connected: %s", len(msg.Names), strings.Join(msg.Names, ", ")),
					))
				}
			case tetris.MPView:
				m.tetrisView = msg

			case mpty.ClientConnectMsg:
			case mpty.ClientDisconnectMsg:

			case error:
				m.err = msg
				log.Warn("client fatal", "error", msg, "who", m.Who.UserProfile.LoginName, "sess", m.Sess.RemoteAddr().String())
				return m, tea.Quit
			default:
				log.Warnf("unhandled broadcast message type: %T", msg)
			}
		}
		m.SetTableOffset()
	}

	m.cmdLine, cmd = m.cmdLine.Update(msg)
	cmds = append(cmds, cmd)
	m.updateSuggestions(msg)

	cmds = append(cmds, m.UpdateTetris(msg))

	return m, tea.Batch(cmds...)
}

func (m *Client) UpdateTetris(msg tea.Msg) tea.Cmd {
	if !m.tetrisConnected {
		return nil
	}

	if key, ok := msg.(tea.KeyMsg); ok && !m.cmdLine.Focused() {
		return sendMsgCmd(m.ctx, m.Send, tetris.MPInput{
			Id: m.Id(),
			// TODO: enable key remapping??
			Cmd: tetris.Input(key.String()),
		})
	}

	return nil

}

func (m *Client) View() string {
	b := &m.b
	b.Reset()

	fmt.Fprint(b, m.ClientInfoModel.View())

	// TODO: guard with render bool
	t := m.table.Render()
	t = lipgloss.PlaceVertical(m.ChatViewHeight(), lipgloss.Bottom, t)
	m.view.SetContent(t)
	m.view.GotoBottom()
	v := m.view.View()

	if m.tetrisView != nil {
		v = lipgloss.Place(
			m.Width, m.ChatViewHeight(),
			lipgloss.Left, lipgloss.Bottom,
			v,
		)
		m.overlay.Foreground = teamodel.String(*m.tetrisView)
		m.overlay.Background = teamodel.String(v)
		fmt.Fprintln(b, m.overlay.View())
	} else {
		fmt.Fprintln(b, v)
	}

	fmt.Fprint(b, m.cmdLine.View())

	return b.String()
}

func (m *Client) ChatViewHeight() int {
	// win H - info H - cmdline H
	return max(0, m.Height-5-1)
}

func (m *Client) ViewportResize() {
	m.view.Height = m.ChatViewHeight()
	m.view.Width = m.ClientInfoModel.Width
}

func (m *Client) updateSuggestions(msg tea.Msg) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "/":
			if m.cmdLine.Value() == "/" {
				m.cmdLine.SetSuggestions(m.cmdPalette.Suggestions())
			}
		}
	}
}

func (m *Client) CmdLineExecute() tea.Cmd {
	if !m.cmdLine.Focused() {
		return nil
	}

	defer func() {
		m.cmdLine.Reset()
		m.cmdLine.SetSuggestions(nil)
	}()

	value := m.cmdLine.Value()

	if !strings.HasPrefix(value, "/") {
		return m.SendChatCmd(value)
	}

	argsStr := strings.TrimPrefix(value, "/")

	cmd, _, _ := strings.Cut(argsStr, " ")
	if cmd == "" {
		return nil
	}

	// TODO: maybe style these type of messages differently?
	m.chatView.Push(Msg{
		LocalAt: m.Time,
		Who:     m.Who.UserProfile.LoginName,
		Sess:    m.Sess.RemoteAddr().String(),
		Str:     value,
	})

	c := m.cmdPalette.Find(cmd)
	if c != nil {
		return c.Run(c, strings.Split(argsStr, " "))
	}

	return nil
}

func (m *Client) SendChatCmd(msg string) tea.Cmd {
	var (
		who  = m.Who.UserProfile.LoginName
		sess = m.Sess.RemoteAddr().String()
		now  = time.Now()
		chat = Msg{
			LocalAt: now,
			Who:     who,
			Sess:    sess,
			Str:     msg,
		}

		send = m.Send
	)
	if send == nil {
		// TODO: maybe buffer locally till we get a send channel?
		return nil
	}

	return func() tea.Msg {
		sendMsg(m.ctx, send, chat)
		return nil
	}
}

func (m *Client) SendCountCmd(i int) tea.Cmd {
	var (
		who  = m.Who.UserProfile.LoginName
		sess = m.Sess.RemoteAddr().String()

		send = m.Send
	)
	if send == nil {
		// TODO: maybe buffer locally till we get a send channel?
		return nil
	}

	return func() tea.Msg {
		for v := range i {
			chat := Msg{
				LocalAt: time.Now(),
				Who:     who,
				Sess:    sess,
				Str:     fmt.Sprint(v),
			}
			sendMsg(m.ctx, send, chat)
		}
		return nil
	}
}

func sendMsg(ctx context.Context, send mpty.Input, msg tea.Msg) {
	select {
	case <-ctx.Done():
	case send <- msg:
	}
}

func sendMsgCmd(ctx context.Context, send mpty.Input, msg tea.Msg) tea.Cmd {
	return func() tea.Msg {
		sendMsg(ctx, send, msg)
		return nil
	}
}
