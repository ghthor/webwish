package main

// An example Bubble Tea server. This will put ssh session into alt screen and
// continually print up to date terminal information. It uses tailscale for
// authentication enabling both an HTTP webapp serviced by gotty and an SSH app
// serviced by wish to use the same authentication system.

import (
	"context"
	"errors"
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
	"github.com/golang-cz/ringbuf"
	"golang.org/x/sync/errgroup"
	"tailscale.com/client/tailscale/apitype"
)

const (
	sshPort  = 23234
	httpPort = 28080
)

var (
	Bold       = lipgloss.NewStyle().Bold(true)
	None       = lipgloss.NewStyle()
	AlignRight = lipgloss.NewStyle().Align(lipgloss.Right)
)

func main() {
	ctx, cancel := context.WithCancelCause(context.Background())
	rootCtx := ctx

	ctx, sigCancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer sigCancel()

	grp, grpCtx := errgroup.WithContext(ctx)

	mainprog := mpty.NewProgram(ctx, cancel, &simulation{})
	select {
	case <-ctx.Done():
	case <-mainprog.RunIn(grp):
	}

	ts, err := tshelper.NewListeners("webwish", sshPort, httpPort)
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
	return func(ctx context.Context, pty ssh.Pty, sess tstea.Session, who *apitype.WhoIsResponse) mpty.Model {
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

			table: table.New(),
		}
	}
}

func newHttpModel() tstea.NewHttpModel {
	return func(ctx context.Context, sess tstea.Session, who *apitype.WhoIsResponse) mpty.Model {
		return &model{
			ctx: ctx,

			infoModel: &infoModel{
				term:   "xterm",
				width:  0,
				height: 0,
				time:   time.Now(),

				sess: sess,
				who:  who,
			},

			table: table.New(),
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

type clientConnectedMsg struct {
	id string
	*tea.Program
}

type clientDisconnectedMsg string

type simulation struct {
	broadcaster *ringbuf.RingBuffer[tea.Msg]
}

func (m *simulation) Init() tea.Cmd {
	return nil
}

func (m *simulation) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

		// TODO: re-enable these log messages
	case clientConnectedMsg:
		// m.clients[msg.id] = msg.Program
		log.Info("connected", "id", msg.id)

	case clientDisconnectedMsg:
		// delete(m.clients, string(msg))
		log.Info("disconnected", "id", msg)
	}

	return m, nil
}

func (m *simulation) View() string {
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

	chat []chatMsg
}

func (m *model) At(row, cell int) string {
	msg := m.chat[row]
	switch cell {
	case 0:
		return msg.simAt.Format(time.TimeOnly)
	case 1:
		id, _, _ := strings.Cut(msg.who, "@")
		return " " + id
	case 2:
		return " | " + msg.msg
	default:
	}

	return ""
}

func (m *model) Rows() int {
	return len(m.chat)
}
func (m *model) Columns() int { return 3 }

type infoModel struct {
	b strings.Builder

	term   string
	width  int
	height int
	time   time.Time

	sess tstea.Session
	who  *apitype.WhoIsResponse
}

func (m *infoModel) Id() string {
	return m.who.UserProfile.LoginName + " " + m.sess.RemoteAddr().String()
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
			if col == 1 {
				return AlignRight
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
		m.table.Offset(max(0, len(m.chat)-m.ChatViewHeight()-1))

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "/":
			cmds = append(cmds, m.cmdLine.Focus())
		case "enter":
			cmds = append(cmds, m.CmdLineExecute())
		}

	case mpty.Input:
		m.Send = msg

	case []chatMsg: //TODO: nothing actually sends this anymore
		m.chat = append(m.chat, msg...)
		m.table.Offset(max(0, len(m.chat)-m.ChatViewHeight()-1))
	case []tea.Msg:
		for _, msg := range msg {
			// TODO: do something with errors
			if chat, ok := msg.(chatMsg); ok {
				// TODO: switch this over to a ringbuffer
				m.chat = append(m.chat, chat)
			} else {
				log.Warn("ringbuffer read", "error", msg)
			}
		}
		m.table.Offset(max(0, len(m.chat)-m.ChatViewHeight()-1))
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
	case timeMsg:
		m.time = time.Time(msg)
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
		select {
		case <-m.ctx.Done():
		case send <- chat:
		}
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
			select {
			case <-m.ctx.Done():
			case send <- chat:
			}
		}
		return nil
	}
}
