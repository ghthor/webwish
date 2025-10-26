// mpty provides primitives for creating multiplayer bubbletea applications.
package mpty

import (
	"context"
	"errors"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sync/errgroup"
)

type Program struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	started chan struct{}

	// TODO: implement our own minimal tea compatible event loop since this
	// program does not interact with a TTY at any point
	*tea.Program
}

type Client struct {
	*tea.Program
}

type ClientId string

type ClientConnMsg struct {
	Id     ClientId
	Client *Client
}

type ClientDisconnectMsg struct {
	Id ClientId
}

type Main struct {
	started chan struct{}
	tea.Model
}

func (m Main) Init() tea.Cmd {
	close(m.started)

	// TODO: add cmd to inject a broadcast message bus
	return tea.Batch(m.Model.Init())
}

func NewProgram(ctx context.Context, cancel context.CancelCauseFunc, m tea.Model) Program {
	p := tea.NewProgram(
		Main{
			started: make(chan struct{}),
			Model:   m,
		},
		tea.WithContext(ctx),
		tea.WithoutSignals(),
		tea.WithoutRenderer(),
		tea.WithInput(nil),
	)

	return Program{
		ctx:     ctx,
		cancel:  cancel,
		Program: p,
	}
}

func (p Program) RunIn(grp *errgroup.Group) (started chan struct{}) {
	grp.Go(func() error {
		_, serr := p.Program.Run()
		if serr != nil && !errors.Is(serr, context.Canceled) {
			p.cancel(serr)
			return serr
		}
		return nil
	})
	return p.started
}

func (p Program) NewClient(model tea.Model, opts ...tea.ProgramOption) *tea.Program {
	// TODO: maybe perform a validation that the Program was started before any calls to NewClient start happening

	return nil
}
