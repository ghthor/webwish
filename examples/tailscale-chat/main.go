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
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/logging"
	"github.com/ghthor/webwish"
	"github.com/ghthor/webwish/bubbles/chat"
	"github.com/ghthor/webwish/mpty"
	"github.com/ghthor/webwish/tshelper"
	"github.com/ghthor/webwish/tstea"
	"golang.org/x/sync/errgroup"
	"tailscale.com/client/tailscale/apitype"
)

var (
	sshPort  int    = 23234
	httpPort int    = 28080
	hostname string = "tailscale-chat"
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
	err := mainprog.StartIn(ctx, grp)
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

func newSshModel(ctx context.Context, pty ssh.Pty, sess mpty.Session, who *apitype.WhoIsResponse) mpty.ClientModel {
	info := mpty.NewClientInfoModelFromSsh(pty, sess, who)
	return &Model{
		ctx: ctx,

		ClientInfoModel: info,
		chat:            chat.NewClient(ctx, info),
	}
}

func newHttpModel(ctx context.Context, sess mpty.Session, who *apitype.WhoIsResponse) mpty.ClientModel {
	info := mpty.NewClientInfoModelFromWebtty(sess, who)
	return &Model{
		ctx: ctx,

		ClientInfoModel: info,
		chat:            chat.NewClient(ctx, info),
	}
}

type Model struct {
	ctx context.Context

	*mpty.ClientInfoModel

	chat *chat.Client

	cmds []tea.Cmd
}

var _ mpty.ClientModel = &Model{}

func (m *Model) Init() tea.Cmd {
	if m.cmds == nil {
		m.cmds = make([]tea.Cmd, 0, 2)
	}
	return tea.Batch(
		m.ClientInfoModel.Init(),
		m.chat.Init(),
	)
}

func (m *Model) UpdateClient(msg tea.Msg) (mpty.ClientModel, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds = m.cmds[:0]
	)
	m.ClientInfoModel, cmd = m.ClientInfoModel.UpdateInfo(msg)
	cmds = append(cmds, cmd)

	m.chat, cmd = m.chat.UpdateChat(msg)
	cmds = append(cmds, cmd)

	m.cmds = cmds
	return m, tea.Batch(cmds...)
}

func (m *Model) View() string {
	return m.chat.View()
}

func (m *Model) Err() error {
	return m.chat.Err()
}
