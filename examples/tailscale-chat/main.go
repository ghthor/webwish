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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/logging"
	"github.com/ghthor/webtea"
	"github.com/ghthor/webtea/bubbles/chat"
	"github.com/ghthor/webtea/mpty"
	"github.com/ghthor/webtea/mpty/mptymsg"
	"github.com/ghthor/webtea/tshelper"
	"github.com/ghthor/webtea/tstea"
	"golang.org/x/sync/errgroup"
	"tailscale.com/client/tailscale/apitype"
)

var (
	sshPort  int    = 23234
	httpPort int    = 28080
	hostname string = "tailscale-chat"
	sqliteDb string = "msgs.db"
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
	flag.StringVar(&sqliteDb, "sqlite-db", "msgs.db", "filepath to sqlite database")

	flag.Parse()

	ctx, cancel := context.WithCancelCause(context.Background())
	rootCtx := ctx

	ctx, sigCancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer sigCancel()

	recorder, err := mptymsg.NewSqlite(ctx, sqliteDb)
	if err != nil {
		log.Fatal("could not open sqlite", "error", err)
	}
	defer recorder.Close()

	grp, grpCtx := errgroup.WithContext(ctx)
	mainprog := mpty.NewProgram(ctx, cancel, &chat.ServerModel{}, recorder)
	err = mainprog.StartIn(ctx, grp)
	if err != nil {
		log.Fatal("could not start main program", "error", err)
	}

	ts, err := tshelper.NewListeners(hostname, sshPort, httpPort)
	if err != nil {
		log.Fatal("tailscale %w", err)
	}

	s, err := wish.NewServer(
		// wish.WithAddress(net.JoinHostPort(host, port)),
		wish.WithHostKeyPath(".ssh/id_ed25519"),
		wish.WithMiddleware(
			tstea.WishMiddleware(ctx, ts.Client, newSshModel, mainprog.NewClientProgram()),
			logging.Middleware(),
		),
	)
	if err != nil {
		log.Fatal("Could not create SSH server", "error", err)
	}
	webtty := tstea.NewTeaTYFactory(
		ctx, ts.Client, newHttpModel, mainprog.NewClientProgram(),
	)

	tsIPv4, _, err := ts.WaitForTailscaleIP(ctx)
	if err != nil {
		log.Fatal("failed to wait for tailscale IP", "error", err)
	}
	log.Info("Starting SSH server", "addr", net.JoinHostPort(tsIPv4.String(), fmt.Sprint(sshPort)))
	log.Infof("Starting HTTP server http://%s:%d", tsIPv4.String(), httpPort)

	err = errors.Join(
		webtea.RunSSH(grpCtx, grp, cancel, ts.Ssh, s),
		webtea.RunHTTP(grpCtx, grp, cancel, ts.Http, webtty, hostname),
	)
	if err != nil {
		log.Fatal("failed to start webtea", "error", err)
	}

	<-ctx.Done()
	if err = context.Cause(rootCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("failed to start webtea", "error", err)
	}

	log.Info("Stopping SSH server")
	err = webtea.ShutdownSSH(s, 30*time.Second)
	if err != nil {
		log.Error("Could not stop server", "error", err)
	}

	if err = grp.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("error shutting down servers", "error", err)
	}
}

func newSshModel(ctx context.Context, pty ssh.Pty, sess mpty.Session, who *apitype.WhoIsResponse) mpty.ClientModel {
	info := mpty.NewClientInfoModelFromSsh(pty, sess, who)
	return &Model{
		ctx: ctx,

		ClientInfoModel: info,
		showInfo:        true,
	}
}

func newHttpModel(ctx context.Context, sess mpty.Session, who *apitype.WhoIsResponse) mpty.ClientModel {
	info := mpty.NewClientInfoModelFromWebtty(sess, who)
	return &Model{
		ctx: ctx,

		ClientInfoModel: info,
		showInfo:        true,
	}
}

type Model struct {
	ctx context.Context

	*mpty.ClientInfoModel
	showInfo bool

	chat *chat.Client

	b    strings.Builder
	cmds []tea.Cmd
}

var _ mpty.ClientModel = &Model{}

func (m *Model) Init() tea.Cmd {
	if m.cmds == nil {
		m.cmds = make([]tea.Cmd, 0, 2)
	}
	m.configureChat()

	return tea.Batch(
		m.ClientInfoModel.Init(),
		m.chat.Init(),
	)
}

func (m *Model) configureChat() {
	m.chat = chat.NewClient(m.ctx, m.ClientInfoModel, chat.Cmd{
		Use:   "info",
		Short: "Toggle client terminal info.",
		Run: func(cmd *chat.Cmd, args []string) tea.Cmd {
			m.showInfo = !m.showInfo
			m.setChatSize()
			return nil
		},
	})
}

func (m *Model) UpdateClient(msg tea.Msg) (mpty.ClientModel, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds = m.cmds[:0]
	)
	m.ClientInfoModel, cmd = m.ClientInfoModel.UpdateInfo(msg)
	cmds = append(cmds, cmd)

	switch msg.(type) {
	case tea.WindowSizeMsg:
		m.setChatSize()
	}

	m.chat, cmd = m.chat.UpdateChat(msg)
	cmds = append(cmds, cmd)

	m.cmds = cmds
	return m, tea.Batch(cmds...)
}

func (m *Model) View() string {
	b := &m.b
	b.Reset()

	// TODO: maybe make this an overlay?
	fmt.Fprint(b, m.ClientInfoModel.View())
	m.chat.ViewTo(b)

	return b.String()
}

func (m *Model) setChatSize() {
	if m.showInfo {
		m.chat.SetSize(m.Width, m.Height-m.ClientInfoModel.ViewHeight())
	} else {
		m.chat.SetSize(m.Width, m.Height)
	}
}

func (m *Model) Err() error {
	return m.chat.Err()
}
