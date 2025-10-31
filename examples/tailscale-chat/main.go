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
	"maps"
	"net"
	"os"
	"os/signal"
	"slices"
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
	"github.com/golang-cz/ringbuf"
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

	log.Infof("Starting HTTP server http://%s:%d", tsIPv4.String(), httpPort)

	err = errors.Join(
		webwish.RunSSH(grpCtx, grp, cancel, ts.Ssh, s),
		webwish.RunHTTP(grpCtx, grp, cancel, ts.Http, tstea.NewTeaTYFactory(
			ctx, ts.Client, newHttpModel(), mainprog.NewClientProgram(),
		), hostname),
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
		return &model{
			ctx: ctx,

			ClientInfoModel: mpty.NewClientInfoModelFromSsh(pty, sess, who),

			table:    table.New(),
			chatView: unsafering.NewRingBuffer[chat.Msg](300),
		}
	}
}

func newHttpModel() tstea.NewHttpModel {
	return func(ctx context.Context, sess mpty.Session, who *apitype.WhoIsResponse) mpty.ClientModel {
		return &model{
			ctx: ctx,

			ClientInfoModel: mpty.NewClientInfoModelFromWebtty(sess, who),

			table:    table.New(),
			chatView: unsafering.NewRingBuffer[chat.Msg](300),
		}
	}
}

type timeMsg time.Time

type namesMsg struct {
	id    mpty.ClientId
	names []string
}

type (
	tetrisAddPlayerMsg mpty.ClientId
	tetrisRmPlayerMsg  mpty.ClientId

	tetrisView  *string
	tetrisInput struct {
		Id  mpty.ClientId
		Cmd tetris.Input
	}
)

type chatServer struct {
	cmds        []tea.Cmd
	broadcaster *ringbuf.RingBuffer[tea.Msg]

	tick time.Time

	names map[string]map[string]time.Time

	tetris         *tetris.Model
	tetrisPlayers  map[mpty.ClientId]struct{}
	tetrisInputs   map[mpty.ClientId]tetris.Input
	tetrisInputTs  map[mpty.ClientId]int64
	tetrisInputSum map[tetris.Input]int
}

func (m *chatServer) Init() tea.Cmd {
	if m.cmds == nil {
		m.cmds = make([]tea.Cmd, 0, 2)
	}
	if m.names == nil {
		m.names = make(map[string]map[string]time.Time, 10)
	}
	if m.tetrisPlayers == nil {
		m.tetrisPlayers = make(map[mpty.ClientId]struct{}, 10)
		m.tetrisInputs = make(map[mpty.ClientId]tetris.Input, 10)
		m.tetrisInputTs = make(map[mpty.ClientId]int64, 10)
		m.tetrisInputSum = make(map[tetris.Input]int)
	}
	return tea.Batch(func() tea.Msg { return time.Now() })
}

func (m *chatServer) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.cmds = m.cmds[:0]
	m.UpdateChat(msg)
	m.cmds = append(m.cmds, m.UpdateTetris(msg))
	return m, tea.Batch(m.cmds...)
}

func (m *chatServer) UpdateChat(msg tea.Msg) {
	switch msg := msg.(type) {
	case *ringbuf.RingBuffer[tea.Msg]:
		m.broadcaster = msg

	case chat.Msg:
		msg.ServerAt = time.Now()
		msg = msg.SetNick()

		if m.broadcaster != nil {
			m.broadcaster.Write(msg)
			log.Debug("chat", "t", msg.ServerAt, "lag", msg.ServerAt.Sub(msg.LocalAt), "who", msg.Who, "sess", msg.Sess, "msg", msg.Str)
		} else {
			log.Warn("dropped chat", "t", msg.ServerAt, "lag", msg.ServerAt.Sub(msg.LocalAt), "who", msg.Who, "sess", msg.Sess, "msg", msg.Str)
		}

	case namesMsg:
		msg.names = slices.Sorted(maps.Keys(m.names))
		for i := range msg.names {
			msg.names[i] = chat.NickFromWho(msg.names[i])
		}
		m.broadcaster.Write(msg)

	case mpty.ClientConnectMsg:
		who, sess, _ := strings.Cut(string(msg), " ")

		sessions, ok := m.names[who]
		if !ok {
			m.names[who] = map[string]time.Time{sess: m.tick}
		} else {
			sessions[sess] = m.tick
		}

		m.broadcaster.Write(chat.SysMsg(m.tick,
			fmt.Sprintf("%s connected", msg),
		))

	case mpty.ClientDisconnectMsg:
		who, sess, _ := strings.Cut(string(msg), " ")

		sessions, ok := m.names[who]
		if ok {
			delete(sessions, sess)
		}
		if len(sessions) == 0 {
			delete(m.names, who)
		}

		m.broadcaster.Write(chat.SysMsg(m.tick,
			fmt.Sprintf("%s disconnected", msg),
		))

	case time.Time:
		m.tick = msg
	}
}

func (m *chatServer) UpdateTetris(msg tea.Msg) tea.Cmd {
	tetrisMsg := msg

	switch msg := msg.(type) {
	case tetrisAddPlayerMsg:
		m.tetrisPlayers[mpty.ClientId(msg)] = struct{}{}

		var cmd tea.Cmd
		if m.tetris == nil {
			m.tetris = tetris.New()
			cmd = m.tetris.Init()
		}
		m.broadcaster.Write(m.tetrisView())
		return cmd

	case tetrisRmPlayerMsg:
		delete(m.tetrisPlayers, mpty.ClientId(msg))

		if len(m.tetrisPlayers) == 0 {
			m.broadcaster.Write(tetrisView(nil))
			m.tetris = nil
		} else {
			m.broadcaster.Write(m.tetrisView())
		}
		return nil

	case mpty.ClientDisconnectMsg:
		delete(m.tetrisPlayers, mpty.ClientId(msg))
		delete(m.tetrisInputs, mpty.ClientId(msg))
		delete(m.tetrisInputTs, mpty.ClientId(msg))

		if len(m.tetrisPlayers) == 0 {
			m.broadcaster.Write(tetrisView(nil))
			m.tetris = nil
		} else {
			m.broadcaster.Write(m.tetrisView())
		}
		return nil

	case tetrisInput:
		i := m.tetrisInput(msg)
		if i == tetris.InputNone {
			return nil
		}
		tetrisMsg = i
	}

	if m.tetris != nil {
		var (
			cmd      tea.Cmd
			modified bool
		)
		m.tetris, cmd, modified = m.tetris.UpdateTetrisShouldRender(tetrisMsg)
		if modified {
			m.broadcaster.Write(m.tetrisView())
		}
		return cmd
	}

	return nil
}

func (m *chatServer) tetrisInput(msg tetrisInput) tetris.Input {
	clear(m.tetrisInputSum)
	m.tetrisInputs[msg.Id] = msg.Cmd
	m.tetrisInputTs[msg.Id] = time.Now().UnixNano()

	half := len(m.tetrisInputs) / 2
	// half := len(m.tetrisPlayers)

	for _, input := range m.tetrisInputs {
		s := m.tetrisInputSum[input]
		s++
		if s >= half {
			clear(m.tetrisInputs)
			clear(m.tetrisInputTs)
			return input
		}
		m.tetrisInputSum[input] = s
		continue
	}

	return tetris.InputNone
}

func (m *chatServer) tetrisView() tetrisView {
	// TODO: players list
	// TODO: inputs list
	inputs := ""
	inputs = m.tetrisInputView()
	v := m.tetris.View()
	v = lipgloss.JoinHorizontal(lipgloss.Top, inputs, v)
	return tetrisView(&v)
}

func (m *chatServer) tetrisInputView() string {
	type pair struct {
		mpty.ClientId
		ts int64
	}
	ins := make([]pair, 0, len(m.tetrisInputTs))
	for k, v := range m.tetrisInputTs {
		ins = append(ins, pair{k, v})
	}
	slices.SortStableFunc(ins, func(a, b pair) int {
		switch {
		case a.ts < b.ts:
			return -1
		case a.ts > b.ts:
			return 1
		default:
			return 0
		}
	})

	maxH := m.tetris.Height()
	var b strings.Builder
	for i, pair := range ins {
		if i >= maxH {
			break
		}
		fmt.Fprintln(&b, string(tetris.InputRune[m.tetrisInputs[pair.ClientId]]))
	}
	return b.String()
}

func (m *chatServer) View() string {
	return ""
}

type model struct {
	*mpty.ClientInfoModel

	b strings.Builder

	Send mpty.Input

	ctx context.Context

	cmds []tea.Cmd

	cmdLine textinput.Model
	table   *table.Table
	view    viewport.Model

	chatView *unsafering.RingBuffer[chat.Msg]

	tetrisView    tetrisView
	tetrisEnabled bool

	overlay *overlay.Model

	quiet         bool
	showTimestamp bool

	err error
}

var _ table.Data = &model{}
var _ mpty.ClientModel = &model{}

func (m *model) Err() error {
	return m.err
}

func (m *model) AtRaw(row int) chat.Msg {
	msg, _ := m.chatView.AtInWindow(row, m.chatView.Len())
	return msg
}

const (
	COL_TS = iota
	COL_WHO
	COL_MSG
	COL_SZ
)

func (m *model) At(row, cell int) string {
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

func (m *model) Rows() int {
	return m.chatView.Len()
}
func (m *model) Columns() int {
	if m.showTimestamp {
		return 3
	} else {
		return 2
	}
}

func (m *model) SetTableOffset() {
	m.table.Offset(max(0, m.chatView.Len()-m.ChatViewHeight()-1))
}

func (m *model) Init() tea.Cmd {
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

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m.UpdateClient(msg)
}

func (m *model) UpdateClient(msg tea.Msg) (mpty.ClientModel, tea.Cmd) {
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
			if m.tetrisEnabled && m.cmdLine.Focused() {
				m.cmdLine.Blur()
			}
		case "/":
			if m.tetrisEnabled && !m.cmdLine.Focused() {
				cmds = append(cmds, m.cmdLine.Focus())
			}
		}

	case tetrisView:
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
			case namesMsg:
				if msg.id == m.Id() {
					m.chatView.Push(chat.SysMsg(m.Time,
						fmt.Sprintf("-> %d connected: %s", len(msg.names), strings.Join(msg.names, ", ")),
					))
				}
			case tetrisView:
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

func (m *model) UpdateTetris(msg tea.Msg) tea.Cmd {
	if !m.tetrisEnabled {
		return nil
	}

	if key, ok := msg.(tea.KeyMsg); ok && !m.cmdLine.Focused() {
		return sendMsgCmd(m.ctx, m.Send, tetrisInput{
			Id: m.Id(),
			// TODO: enable key remapping??
			Cmd: tetris.Input(key.String()),
		})
	}

	return nil

}

func (m *model) View() string {
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

func (m *model) ChatViewHeight() int {
	// win H - info H - cmdline H
	return max(0, m.Height-5-1)
}

func (m *model) ViewportResize() {
	m.view.Height = m.ChatViewHeight()
	m.view.Width = m.ClientInfoModel.Width
}

func (m *model) updateSuggestions(msg tea.Msg) {
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

func (m *model) CmdLineExecute() tea.Cmd {
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

func (m *model) SendChatCmd(msg string) tea.Cmd {
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

func (m *model) SendCountCmd(i int) tea.Cmd {
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
