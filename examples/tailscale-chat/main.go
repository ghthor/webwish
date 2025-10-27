package main

// An example Bubble Tea server. This will put ssh session into alt screen and
// continually print up to date terminal information. It uses tailscale for
// authentication enabling both an HTTP webapp serviced by gotty and an SSH app
// serviced by wish to use the same authentication system.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os/signal"
	"strconv"
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
	"github.com/ghthor/webwish/mpty"
	"github.com/ghthor/webwish/tshelper"
	"github.com/ghthor/webwish/tstea"
	"github.com/ghthor/webwish/unsafering"
	"github.com/golang-cz/ringbuf"
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
)

var (
	Bold       = lipgloss.NewStyle().Bold(true)
	None       = lipgloss.NewStyle()
	AlignRight = lipgloss.NewStyle().Align(lipgloss.Right)

	StyleSystem    = lipgloss.NewStyle().Faint(true)
	StyleSystemWho = StyleSystem.Align(lipgloss.Left)
	StyleSystemMsg = StyleSystem
)

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

	mainprog := mpty.NewProgram(ctx, cancel, &chatServer{})
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

	tsIPv4, _, err := ts.WaitForTailscaleIP(ctx)
	if err != nil {
		log.Fatal("failed to wait for tailscale IP", "error", err)
	}
	log.Info("Starting SSH server", "addr", net.JoinHostPort(tsIPv4.String(), fmt.Sprint(sshPort)))

	// TODO: print out complete http(s):// string
	log.Info("Starting HTTP server", "addr", net.JoinHostPort(tsIPv4.String(), fmt.Sprint(httpPort)))

	err = errors.Join(
		webwish.RunSSH(grpCtx, grp, cancel, ts.Ssh, s),
		webwish.RunHTTP(grpCtx, grp, cancel, ts.Http, tstea.NewTeaTYFactory(
			ctx, ts.Client, newHttpModel(), mainprog.NewClientProgram(),
		)),
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
	return func(ctx context.Context, pty ssh.Pty, sess tstea.Session, who *apitype.WhoIsResponse) mpty.ClientModel {
		return &model{
			ctx: ctx,

			infoModel: &infoModel{
				term:   pty.Term,
				width:  pty.Window.Width,
				height: pty.Window.Height,
				time:   time.Now(),

				sess: sess,
				who:  who,
			},

			table:    table.New(),
			chatRing: unsafering.NewRingBuffer[chatMsg](300),
		}
	}
}

func newHttpModel() tstea.NewHttpModel {
	return func(ctx context.Context, sess tstea.Session, who *apitype.WhoIsResponse) mpty.ClientModel {
		return &model{
			ctx: ctx,

			infoModel: &infoModel{
				term:   "xterm",
				width:  80,
				height: 40,
				time:   time.Now(),

				sess: sess,
				who:  who,
			},

			table:    table.New(),
			chatRing: unsafering.NewRingBuffer[chatMsg](300),
		}
	}
}

type timeMsg time.Time

type chatMsg struct {
	cliAt time.Time
	simAt time.Time
	who   string
	sess  string
	msg   string
}

func (m chatMsg) Id() string {
	return m.who + " " + m.sess
}

type chatServer struct {
	broadcaster *ringbuf.RingBuffer[tea.Msg]

	tick time.Time
}

func (m *chatServer) Init() tea.Cmd {
	return tea.Batch(func() tea.Msg { return time.Now() })
}

func (m *chatServer) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case *ringbuf.RingBuffer[tea.Msg]:
		m.broadcaster = msg

	case chatMsg:
		msg.simAt = time.Now()

		if m.broadcaster != nil {
			m.broadcaster.Write(msg)
			log.Debug("chat", "t", msg.simAt, "lag", msg.simAt.Sub(msg.cliAt), "who", msg.who, "sess", msg.sess, "msg", msg.msg)
		} else {
			log.Warn("dropped chat", "t", msg.simAt, "lag", msg.simAt.Sub(msg.cliAt), "who", msg.who, "sess", msg.sess, "msg", msg.msg)
		}

	case mpty.ClientConnectMsg:
		m.broadcaster.Write(chatMsg{
			simAt: m.tick,
			who:   systemNick,
			msg:   fmt.Sprintf("%s connected", msg),
		})
	case mpty.ClientDisconnectMsg:
		m.broadcaster.Write(chatMsg{
			simAt: m.tick,
			who:   systemNick,
			msg:   fmt.Sprintf("%s disconnected", msg),
		})

	case time.Time:
		m.tick = msg
	}

	return m, nil
}

func (m *chatServer) View() string {
	return ""
}

type model struct {
	b strings.Builder

	Send mpty.Input

	ctx context.Context

	cmds []tea.Cmd

	*infoModel

	cmdLine textinput.Model
	table   *table.Table
	view    viewport.Model

	chatRing *unsafering.RingBuffer[chatMsg]

	err error
}

var _ table.Data = &model{}
var _ mpty.ClientModel = &model{}

func (m *model) Err() error {
	return m.err
}

func (m *model) AtRaw(row int) chatMsg {
	msg, _ := m.chatRing.AtInWindow(row, m.chatRing.Len())
	return msg
}

const (
	COL_TS = iota
	COL_WHO
	COL_MSG
	COL_SZ
)

func (m *model) At(row, cell int) string {
	msg := m.AtRaw(row)
	switch cell {
	case COL_TS:
		return msg.simAt.Format(time.TimeOnly)
	case COL_WHO:
		id, _, _ := strings.Cut(msg.who, "@")
		return " " + id
	case COL_MSG:
		return " | " + msg.msg
	default:
	}

	return ""
}

func (m *model) Rows() int {
	return m.chatRing.Len()
}
func (m *model) Columns() int { return 3 }

func (m *model) SetTableOffset() {
	m.table.Offset(max(0, m.chatRing.Len()-m.ChatViewHeight()-1))
}

type infoModel struct {
	b strings.Builder

	term   string
	width  int
	height int
	time   time.Time

	sess tstea.Session
	who  *apitype.WhoIsResponse
}

func (m *infoModel) Id() mpty.ClientId {
	return mpty.ClientId(m.who.UserProfile.LoginName + " " + m.sess.RemoteAddr().String())
}

func (m *model) Init() tea.Cmd {
	if m.cmds == nil {
		m.cmds = make([]tea.Cmd, 0, 1)
	}
	m.cmdLine = textinput.New()
	m.cmdLine.Prompt = "> "
	m.cmdLine.Placeholder = "/ to open command mode"
	m.cmdLine.CharLimit = 0

	m.table = m.table.
		Data(m).Headers().
		Border(lipgloss.Border{}).
		Wrap(true).
		StyleFunc(func(row, col int) lipgloss.Style {
			msg := m.AtRaw(row)

			switch col {
			case COL_WHO:
				if msg.who == systemNick {
					return StyleSystemWho
				}

				return AlignRight
			case COL_MSG:
				if msg.who == systemNick {
					return StyleSystemMsg
				}
			default:
			}

			return None
		})

	m.view = viewport.New(m.width, m.ChatViewHeight())

	return nil
}

func (m *infoModel) Init() tea.Cmd {
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m.UpdateClient(msg)
}

func (m *model) UpdateClient(msg tea.Msg) (mpty.ClientModel, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds = m.cmds[:0]
	)

	m.infoModel, cmd = m.UpdateInfo(msg)
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
		case "q", "ctrl+c":
			return m, tea.Quit
		case "/":
			cmds = append(cmds, m.cmdLine.Focus())
		case "enter":
			cmds = append(cmds, m.CmdLineExecute())
		}

	case []tea.Msg:
		for _, msg := range msg {
			switch msg := msg.(type) {
			case time.Time:
				m.infoModel, cmd = m.UpdateInfo(msg)
				cmds = append(cmds, cmd)
			case chatMsg:
				m.chatRing.Push(msg)

			case mpty.ClientConnectMsg:
			case mpty.ClientDisconnectMsg:

			case error:
				m.err = msg
				log.Warn("client fatal", "error", msg, "who", m.who.UserProfile.LoginName, "sess", m.sess.RemoteAddr().String())
				return m, tea.Quit
			default:
				log.Warnf("unhandled broadcast message type: %T", msg)
			}
		}
		m.SetTableOffset()
	}

	m.cmdLine, cmd = m.cmdLine.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *infoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := m.UpdateInfo(msg)
	return m, cmd
}

func (m *infoModel) UpdateInfo(msg tea.Msg) (*infoModel, tea.Cmd) {
	switch msg := msg.(type) {

	case time.Time:
		m.time = msg

	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
	}
	return m, nil
}

func (m *model) View() string {
	b := &m.b
	b.Reset()

	fmt.Fprint(b, m.infoModel.View())

	t := m.table.Render()
	t = lipgloss.PlaceVertical(m.ChatViewHeight(), lipgloss.Bottom, t)
	m.view.SetContent(t)
	m.view.GotoBottom()
	fmt.Fprintln(b, m.view.View())

	fmt.Fprint(b, m.cmdLine.View())

	return b.String()
}

func (m *infoModel) View() string {
	b := &m.b
	b.Reset()
	if m.who != nil {
		fmt.Fprintf(b, "  who: %s\n", m.who.UserProfile.LoginName)
	}
	if m.sess != nil {
		fmt.Fprintf(b, "raddr: %s\n", m.sess.RemoteAddr().String())
	}
	fmt.Fprintf(b, " term: %s\n", m.term)
	fmt.Fprintf(b, " size: (%d,%d)\n", m.width, m.height)
	fmt.Fprintf(b, " time: %s\n", Bold.Render(m.time.Format(time.RFC1123)))

	return b.String()
}

func (m *infoModel) ChatViewHeight() int {
	// win H - info H - cmdline H
	return max(0, m.height-5-1)
}

func (m *model) ViewportResize() {
	m.view.Height = m.ChatViewHeight()
	m.view.Width = m.infoModel.width
}

func (m *model) CmdLineExecute() tea.Cmd {
	if !m.cmdLine.Focused() {
		return nil
	}

	defer func() {
		m.cmdLine.Reset()
		m.cmdLine.Blur()
	}()

	v := m.cmdLine.Value()
	cmd, rest, _ := strings.Cut(m.cmdLine.Value(), " ")
	if cmd == "" {
		return nil
	}

	switch cmd {
	case "/m":
		if rest != "" {
			return m.SendChatCmd(rest)
		}

	case "/count":
		i, err := strconv.Atoi(rest)
		if err != nil {
			return m.SendChatCmd(fmt.Sprintf("%s => %v", v, err))
		}

		return m.SendCountCmd(i)

	default:
	}

	return nil
}

func (m *model) SendChatCmd(msg string) tea.Cmd {
	var (
		who  = m.who.UserProfile.LoginName
		sess = m.sess.RemoteAddr().String()
		now  = time.Now()
		chat = chatMsg{
			cliAt: now,
			who:   who,
			sess:  sess,
			msg:   msg,
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

func (m *model) SendCountCmd(i int) tea.Cmd {
	var (
		who  = m.who.UserProfile.LoginName
		sess = m.sess.RemoteAddr().String()

		send = m.Send
	)
	if send == nil {
		// TODO: maybe buffer locally till we get a send channel?
		return nil
	}

	return func() tea.Msg {
		for v := range i {
			chat := chatMsg{
				cliAt: time.Now(),
				who:   who,
				sess:  sess,
				msg:   fmt.Sprint(v),
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
