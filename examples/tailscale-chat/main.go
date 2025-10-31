package main

// An example Bubble Tea chat server.
//
// It uses tailscale for authentication enabling both an HTTP webapp serviced
// by gotty and an SSH app serviced by wish to use the same authentication
// system.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/logging"
	"github.com/ghthor/webwish"
	"github.com/ghthor/webwish/bubbles/chat"
	"github.com/ghthor/webwish/bubbles/tetris"
	"github.com/ghthor/webwish/mpty"
	"github.com/ghthor/webwish/teamodel"
	"github.com/ghthor/webwish/tshelper"
	"github.com/ghthor/webwish/tstea"
	"github.com/ghthor/webwish/unsafering"
	overlay "github.com/rmhubbert/bubbletea-overlay"
	"golang.org/x/sync/errgroup"
	"tailscale.com/client/tailscale/apitype"
)

var (
	sshPort  int    = 23234
	httpPort int    = 28080
	hostname string = "tailscale-chat"
)

const (
	systemNick = "system"
	helpNick   = "help"
	infoNick   = "info"
)

var (
	Bold       = lipgloss.NewStyle().Bold(true)
	None       = lipgloss.NewStyle()
	AlignRight = lipgloss.NewStyle().Align(lipgloss.Right)

	StyleSystem    = lipgloss.NewStyle().Faint(true)
	StyleSystemWho = StyleSystem.Align(lipgloss.Left)
	StyleSystemMsg = StyleSystem
)

func init() {
	switch os.Getenv("LIPGLOSS_LOG_FORMAT") {
	case "json":
		log.SetFormatter(log.JSONFormatter)
	}
}

func main() {
	flag.IntVar(&sshPort, "ssh-port", 23234, "port for ssh listener")
	flag.IntVar(&httpPort, "http-port", 28080, "port for http listener")
	flag.StringVar(&hostname, "hostname", "tailscale-chat", "tailscale device hostname")

	flag.Parse()

	ctx, cancel := context.WithCancelCause(context.Background())
	rootCtx := ctx

	ctx, sigCancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer sigCancel()

	grp, grpCtx := errgroup.WithContext(ctx)

	mainprog := mpty.NewProgram(ctx, cancel, &chat.ServerModel{})
	select {
	case <-ctx.Done():
	case <-mainprog.RunIn(grp):
	}

	ts, err := tshelper.NewListeners(hostname, sshPort, httpPort)
	if err != nil {
		log.Fatal("tailscale %w", err)
	}

	s, err := wish.NewServer(
		// wish.WithAddress(net.JoinHostPort(host, port)),
		wish.WithHostKeyPath(".ssh/id_ed25519"),
		wish.WithMiddleware(
			tstea.WishMiddleware(ctx, ts.Client, newSshModel(), mainprog.NewClientProgram()),
			logging.Middleware(),
		),
	)
	if err != nil {
		log.Fatal("Could not create SSH server", "error", err)
	}
	webtty := tstea.NewTeaTYFactory(
		ctx, ts.Client, newHttpModel(), mainprog.NewClientProgram(),
	)

	tsIPv4, _, err := ts.WaitForTailscaleIP(ctx)
	if err != nil {
		log.Fatal("failed to wait for tailscale IP", "error", err)
	}
	log.Info("Starting SSH server", "addr", net.JoinHostPort(tsIPv4.String(), fmt.Sprint(sshPort)))
	log.Infof("Starting HTTP server http://%s:%d", tsIPv4.String(), httpPort)

	err = errors.Join(
		webwish.RunSSH(grpCtx, grp, cancel, ts.Ssh, s),
		webwish.RunHTTP(grpCtx, grp, cancel, ts.Http, webtty, hostname),
	)
	if err != nil {
		log.Fatal("failed to start webwish", "error", err)
	}

	<-ctx.Done()
	if err = context.Cause(rootCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("failed to start webwish", "error", err)
	}

	log.Info("Stopping SSH server")
	err = webwish.ShutdownSSH(s, 30*time.Second)
	if err != nil {
		log.Error("Could not stop server", "error", err)
	}

	if err = grp.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("error shutting down servers", "error", err)
	}
}

func newSshModel() tstea.NewSshModel {
	return func(ctx context.Context, pty ssh.Pty, sess mpty.Session, who *apitype.WhoIsResponse) mpty.ClientModel {
		return &Model{
			ctx: ctx,

			ClientInfoModel: mpty.NewClientInfoModelFromSsh(pty, sess, who),

			table:    table.New(),
			chatView: unsafering.NewRingBuffer[chat.Msg](300),
		}
	}
}

func newHttpModel() tstea.NewHttpModel {
	return func(ctx context.Context, sess mpty.Session, who *apitype.WhoIsResponse) mpty.ClientModel {
		return &Model{
			ctx: ctx,

			ClientInfoModel: mpty.NewClientInfoModelFromWebtty(sess, who),

			table:    table.New(),
			chatView: unsafering.NewRingBuffer[chat.Msg](300),
		}
	}
}

type Model struct {
	*mpty.ClientInfoModel

	b strings.Builder

	Send mpty.Input

	ctx context.Context

	cmds []tea.Cmd

	cmdLine textinput.Model
	table   *table.Table
	view    viewport.Model

	chatView *unsafering.RingBuffer[chat.Msg]

	tetrisView      tetris.MPView
	tetrisConnected bool

	overlay *overlay.Model

	quiet         bool
	showTimestamp bool

	err error
}

var _ table.Data = &Model{}
var _ mpty.ClientModel = &Model{}

func (m *Model) Err() error {
	return m.err
}

func (m *Model) AtRaw(row int) chat.Msg {
	msg, _ := m.chatView.AtInWindow(row, m.chatView.Len())
	return msg
}

const (
	COL_TS = iota
	COL_WHO
	COL_MSG
	COL_SZ
)

func (m *Model) At(row, cell int) string {
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

func (m *Model) Rows() int {
	return m.chatView.Len()
}
func (m *Model) Columns() int {
	if m.showTimestamp {
		return 3
	} else {
		return 2
	}
}

func (m *Model) SetTableOffset() {
	m.table.Offset(max(0, m.chatView.Len()-m.ChatViewHeight()-1))
}

func (m *Model) Init() tea.Cmd {
	if m.cmds == nil {
		m.cmds = make([]tea.Cmd, 0, 1)
	}

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
				case systemNick, infoNick, helpNick:
					return StyleSystemWho
				}

				return AlignRight
			case COL_MSG:
				switch msg.Who {
				case systemNick, infoNick, helpNick:
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

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m.UpdateClient(msg)
}

func (m *Model) UpdateClient(msg tea.Msg) (mpty.ClientModel, tea.Cmd) {
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
			case chat.Msg:
				if m.quiet && msg.Who == chat.SysNick {
				} else {
					m.chatView.Push(msg)
				}
			case chat.NamesReq:
				if msg.Requestor == m.Id() {
					m.chatView.Push(chat.SysMsg(m.Time,
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

func (m *Model) UpdateTetris(msg tea.Msg) tea.Cmd {
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

func (m *Model) View() string {
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

func (m *Model) ChatViewHeight() int {
	// win H - info H - cmdline H
	return max(0, m.Height-5-1)
}

func (m *Model) ViewportResize() {
	m.view.Height = m.ChatViewHeight()
	m.view.Width = m.ClientInfoModel.Width
}

func (m *Model) updateSuggestions(msg tea.Msg) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "/":
			if m.cmdLine.Value() == "/" {
				m.cmdLine.SetSuggestions(commandSuggestions(commands))
			}
		}
	}
}

func (m *Model) CmdLineExecute() tea.Cmd {
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

	cmd, rest, _ := strings.Cut(m.cmdLine.Value(), " ")
	if cmd == "" {
		return nil
	}

	// TODO: maybe style these type of messages differently?
	m.chatView.Push(chat.Msg{
		LocalAt: m.Time,
		Who:     m.Who.UserProfile.LoginName,
		Sess:    m.Sess.RemoteAddr().String(),
		Str:     value,
	})

	if c, ok := commands[cmd]; ok {
		return c.fn(m, cmd, rest)
	}

	return nil
}

func (m *Model) SendChatCmd(msg string) tea.Cmd {
	var (
		who  = m.Who.UserProfile.LoginName
		sess = m.Sess.RemoteAddr().String()
		now  = time.Now()
		chat = chat.Msg{
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

func (m *Model) SendCountCmd(i int) tea.Cmd {
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
			chat := chat.Msg{
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
