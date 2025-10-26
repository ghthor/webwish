package tstea

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/cenkalti/backoff/v5"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/bubbletea"
	"github.com/creack/pty"
	"github.com/ghthor/gotty/v2/server"
	"github.com/ghthor/webwish/ctxhelp"
	"github.com/gorilla/websocket"
	"github.com/muesli/termenv"
	"golang.org/x/sync/errgroup"
	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
)

type Session interface {
	RemoteAddr() net.Addr
}

type NewSshModel func(context.Context, ssh.Pty, Session, *apitype.WhoIsResponse) tea.Model
type NewHttpModel func(context.Context, Session, *apitype.WhoIsResponse) tea.Model
type NewTeaProgram func(context.Context, tea.Model, ...tea.ProgramOption) *tea.Program

func WishMiddleware(ctx context.Context, lc *local.Client, newModel NewSshModel, newProg NewTeaProgram) wish.Middleware {
	teaHandler := func(s ssh.Session) *tea.Program {
		who, err := lc.WhoIs(s.Context(), s.RemoteAddr().String())
		if err != nil {
			wish.Fatalln(s, "tailscale WhoIs error: ", err)
			return nil
		}

		pty, _, active := s.Pty()
		if !active {
			wish.Fatalln(s, "no active terminal, skipping")
			return nil
		}
		var (
			progCtx, _ = ctxhelp.Join(ctx, s.Context())
			m          = newModel(progCtx, pty, s, who)
		)
		return newProg(progCtx, m, bubbletea.MakeOptions(s)...)
	}
	return bubbletea.MiddlewareWithProgramHandler(teaHandler, termenv.ANSI256)
}

type TeaTYFactory struct {
	ctx context.Context
	ts  *local.Client

	newModel NewHttpModel
	newProg  NewTeaProgram
}

func NewTeaTYFactory(ctx context.Context, ts *local.Client, newModel NewHttpModel, newProg NewTeaProgram) *TeaTYFactory {
	return &TeaTYFactory{
		ctx: ctx,
		ts:  ts,

		newModel: newModel,
		newProg:  newProg,
	}
}

var _ server.Factory = &TeaTYFactory{}

func (*TeaTYFactory) Name() string { return "TeaTYFactory" }

func (f *TeaTYFactory) New(ctx context.Context, params map[string][]string, conn *websocket.Conn) (server.Slave, error) {
	ctx, cancel := ctxhelp.Join(f.ctx, ctx)

	who, err := f.ts.WhoIs(ctx, conn.RemoteAddr().String())
	if err != nil {
		return nil, err
	}

	p, t, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to pty.Open(): %w", err)
	}

	m := f.newModel(ctx, conn, who)
	prog := f.newProg(ctx, m,
		tea.WithInput(t),
		tea.WithOutput(t),
	)

	grp, grpCtx := errgroup.WithContext(ctx)
	grp.Go(func() error {
		defer func() {
			t.Close()
			p.Close()
			conn.Close()
		}()

		_, err := prog.Run()
		if err != nil && !errors.Is(err, context.Canceled) {
			cancel(err)
			return err
		}

		return nil
	})

	return &TeaTYProgram{
		ctx: grpCtx,
		pty: p,
		tty: t,

		grp:     grp,
		program: prog,
	}, nil
}

type TeaTYProgram struct {
	ctx context.Context

	pty, tty *os.File

	grp     *errgroup.Group
	program *tea.Program
}

var _ server.Slave = &TeaTYProgram{}

func (t *TeaTYProgram) Read(p []byte) (n int, err error) {
	return t.pty.Read(p)
}

func (t *TeaTYProgram) Write(p []byte) (n int, err error) {
	return t.pty.Write(p)
}

func (t *TeaTYProgram) Close() error {
	t.tty.Close()
	t.pty.Close()
	t.program.Quit()
	return t.grp.Wait()
}

func (t *TeaTYProgram) WindowTitleVariables() map[string]any {
	return map[string]any{}
}

func (t *TeaTYProgram) ResizeTerminal(width, height int) error {
	exp := &backoff.ExponentialBackOff{
		InitialInterval:     10 * time.Millisecond,
		RandomizationFactor: 0.0,
		Multiplier:          1.1,
		MaxInterval:         500 * time.Millisecond,
	}
	_, err := backoff.Retry(t.ctx, func() (struct{}, error) {
		return struct{}{}, errors.Join(
			pty.Setsize(t.pty, &pty.Winsize{
				Cols: uint16(width),
				Rows: uint16(height),
			}),
			pty.Setsize(t.tty, &pty.Winsize{
				Cols: uint16(width),
				Rows: uint16(height),
			}),
		)
	},
		backoff.WithBackOff(exp),
		backoff.WithMaxElapsedTime(2*time.Second),
		backoff.WithNotify(func(err error, d time.Duration) {
			log.Warn("pty resize", "error", err, "retrying", d)
		}),
	)
	if err != nil {
		log.Warn("pty resize retry exhausted", "error", err)
		return err
	}
	t.program.Send(tea.WindowSizeMsg{
		Width:  width,
		Height: height,
	})
	return nil
}
