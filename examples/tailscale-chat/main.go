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
	"github.com/ghthor/webwish/tshelper"
	"github.com/ghthor/webwish/tstea"
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

	sim := tea.NewProgram(&simulation{},
		tea.WithContext(grpCtx),
		tea.WithoutSignals(),
		tea.WithoutRenderer(),
		tea.WithInput(nil),
	)

	grp.Go(func() error {
		_, serr := sim.Run()
		if serr != nil && !errors.Is(serr, context.Canceled) {
			cancel(serr)
			return serr
		}
		return nil
	})

	ts, err := tshelper.NewListeners("webwish", sshPort, httpPort)
	if err != nil {
		log.Fatal("tailscale %w", err)
	}

	s, err := wish.NewServer(
		// wish.WithAddress(net.JoinHostPort(host, port)),
		wish.WithHostKeyPath(".ssh/id_ed25519"),
		wish.WithMiddleware(
			tstea.WishMiddleware(ctx, ts.Client, newSshModel(sim), newProg(sim)),
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
			ctx, ts.Client, newHttpModel(sim), newProg(sim),
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

func newSshModel(sim *tea.Program) tstea.NewSshModel {
	return func(ctx context.Context, pty ssh.Pty, sess tstea.Session, who *apitype.WhoIsResponse) tea.Model {
		return &model{
			ctx: ctx,
			sim: sim,

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

func newHttpModel(sim *tea.Program) tstea.NewHttpModel {
	return func(ctx context.Context, sess tstea.Session, who *apitype.WhoIsResponse) tea.Model {
		return &model{
			ctx: ctx,
			sim: sim,

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

type Client interface {
	Id() string
}

// clientInitModel handles initialization logic related to registering with the
// central simulation. This is required as we can't use Program.Wait() till
// AFTER the tea.Program has started, but that will be happening asynchronously
// from the creation point. So we use a Model here to ensure we don't spinoff
// the disconnection logic till AFTER the program has been started with Run()
type clientInitModel struct {
	sim    *tea.Program
	id     string
	client *tea.Program
	model  tea.Model
}

func (m *clientInitModel) Init() tea.Cmd {
	return tea.Batch(func() tea.Msg {
		msg := clientConnectedMsg{
			id:      m.id,
			Program: m.client,
		}
		m.sim.Send(msg)
		return msg
	}, m.model.Init())
}

func (m *clientInitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case clientConnectedMsg:
		go func() {
			m.client.Wait()
			m.sim.Send(clientDisconnectedMsg(m.id))
		}()
		return m.model, nil
	}
	return m, nil
}

func (m clientInitModel) View() string {
	return ""
}

func newProg(sim *tea.Program) tstea.NewTeaProgram {
	return func(ctx context.Context, m tea.Model, opts ...tea.ProgramOption) *tea.Program {
		opts = append(opts,
			tea.WithContext(ctx),
			tea.WithoutSignalHandler(),
			tea.WithAltScreen(),
		)

		init := &clientInitModel{sim: sim}
		if client, ok := m.(Client); ok {
			init.id = client.Id()
			init.model = m
			m = init
		}

		p := tea.NewProgram(m, opts...)
		init.client = p

		// TODO: move this to the simulation program
		go func() {
			done := ctx.Done()
			for {
				select {
				case <-done:
					return
				case <-time.After(1 * time.Second):
					p.Send(timeMsg(time.Now()))
				}
			}
		}()
		return p
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
	msgs []chatMsg

	clients map[string]*tea.Program
}

func (m *simulation) Init() tea.Cmd {
	if m.clients == nil {
		m.clients = make(map[string]*tea.Program, 10)
	}
	return nil
}

func (m *simulation) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case chatMsg:
		msg.simAt = time.Now()
		m.msgs = append(m.msgs, msg)
		// TODO: ticker to prune msgs array

		for _, p := range m.clients {
			p.Send(msg)
		}
		log.Info("chat", "t", msg.simAt, "lag", msg.simAt.Sub(msg.cliAt), "who", msg.who, "sess", msg.sess, "msg", msg.msg)

	case clientConnectedMsg:
		m.clients[msg.id] = msg.Program
		log.Info("connected", "id", msg.id)
		chat := make([]chatMsg, 0, max(10, len(m.msgs)))
		chat = append(chat, m.msgs...)
		msg.Program.Send(chat)

	case clientDisconnectedMsg:
		delete(m.clients, string(msg))
		log.Info("disconnected", "id", msg)
	}

	return m, nil
}

func (m *simulation) View() string {
	return ""
}

type model struct {
	b strings.Builder

	sim *tea.Program

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
	// return min(len(m.chat), m.ChatViewHeight())
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

	case []chatMsg:
		m.chat = m.chat[:0]
		m.chat = append(m.chat, msg...)
		m.table.Offset(max(0, len(m.chat)-m.ChatViewHeight()-1))
	case chatMsg:
		m.chat = append(m.chat, msg)
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
	)

	return func() tea.Msg {
		m.sim.Send(chatMsg{
			cliAt: now,
			who:   who,
			sess:  sess,
			msg:   msg,
		})
		return nil
	}
}

func (m *model) SendCountCmd(i int) tea.Cmd {
	var (
		who  = m.who.UserProfile.LoginName
		sess = m.sess.RemoteAddr().String()
	)

	return func() tea.Msg {
		for v := range i {
			m.sim.Send(chatMsg{
				cliAt: time.Now(),
				who:   who,
				sess:  sess,
				msg:   fmt.Sprint(v),
			})
		}
		return nil
	}
}
