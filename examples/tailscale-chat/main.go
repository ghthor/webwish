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
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

func main() {
	ctx, cancel := context.WithCancelCause(context.Background())
	rootCtx := ctx

	ctx, sigCancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer sigCancel()

	ts, err := tshelper.NewListeners("webwish", sshPort, httpPort)
	if err != nil {
		log.Fatal("tailscale %w", err)
	}

	s, err := wish.NewServer(
		// wish.WithAddress(net.JoinHostPort(host, port)),
		wish.WithHostKeyPath(".ssh/id_ed25519"),
		wish.WithMiddleware(
			tstea.WishMiddleware(ctx, ts.Client, newSshModel, newProg),
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

	grp, grpCtx := errgroup.WithContext(ctx)
	err = errors.Join(
		webwish.RunSSH(grpCtx, grp, cancel, ts.Ssh, s),
		webwish.RunHTTP(grpCtx, grp, cancel, ts.Http, tstea.NewTeaTYFactory(
			ctx, ts.Client, newHttpModel, newProg,
		)),
	)
	if err != nil {
		log.Fatal("failed to start webwish", "error", err)
	}

	<-ctx.Done()
	if err = context.Cause(rootCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("Could not start server", "error", err)
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

func newSshModel(ctx context.Context, pty ssh.Pty, sess tstea.Session, who *apitype.WhoIsResponse) tea.Model {
	return &model{
		ctx:    ctx,
		term:   pty.Term,
		width:  pty.Window.Width,
		height: pty.Window.Height,
		time:   time.Now(),

		sess: sess,
		who:  who,
	}
}

func newHttpModel(ctx context.Context, sess tstea.Session, who *apitype.WhoIsResponse) tea.Model {
	return &model{
		ctx:    ctx,
		term:   "xterm",
		width:  0,
		height: 0,
		time:   time.Now(),

		sess: sess,
		who:  who,
	}
}

func newProg(ctx context.Context, m tea.Model, opts ...tea.ProgramOption) *tea.Program {
	opts = append(opts,
		tea.WithContext(ctx),
		tea.WithoutSignalHandler(),
		tea.WithAltScreen(),
	)
	p := tea.NewProgram(m, opts...)
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

// Just a generic tea.Model to demo terminal information of ssh.
type model struct {
	b strings.Builder

	ctx context.Context

	term   string
	width  int
	height int
	time   time.Time

	sess tstea.Session
	who  *apitype.WhoIsResponse
}

type timeMsg time.Time

func (m *model) Init() tea.Cmd {
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case timeMsg:
		m.time = time.Time(msg)
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

var bold = lipgloss.NewStyle().Bold(true)

func (m *model) View() string {
	b := &m.b
	b.Reset()
	fmt.Fprintf(b, " arg0: %s\n", os.Args[0])
	fmt.Fprintf(b, " term: %s\n", m.term)
	fmt.Fprintf(b, " size: (%d,%d)\n", m.width, m.height)
	fmt.Fprintf(b, " time: %s\n", bold.Render(m.time.Format(time.RFC1123)))

	if m.sess != nil {
		fmt.Fprintf(b, "raddr: %s\n", m.sess.RemoteAddr().String())
	}
	if m.who != nil {
		fmt.Fprintf(b, "  who: %s\n", m.who.UserProfile.LoginName)
	}

	fmt.Fprintln(b, "\n[q]uit")

	return b.String()
}
