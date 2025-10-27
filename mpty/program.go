// mpty provides primitives for creating multiplayer bubbletea applications.
package mpty

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/golang-cz/ringbuf"
	"golang.org/x/sync/errgroup"
)

type Input chan<- tea.Msg

type Program struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	started chan struct{}

	// TODO: implement our own minimal tea compatible event loop since this
	// program does not interact with a PTY/TTY at any point
	*tea.Program

	// Send is for a non-blocking many-to-one for clients to communicate with
	// the mpty Program since the tea.Program.Send() API is unfortunately
	// blocking
	Send Input
	recv <-chan tea.Msg

	broadcast *ringbuf.RingBuffer[tea.Msg]
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
	broadcaster *ringbuf.RingBuffer[tea.Msg]
	started     chan struct{}

	tea.Model
}

func (m Main) Init() tea.Cmd {
	close(m.started)
	return tea.Batch(
		func() tea.Msg {
			return m.broadcaster
		},
		m.Model.Init(),
	)
}

func NewProgram(ctx context.Context, cancel context.CancelCauseFunc, m tea.Model) Program {
	broadcaster := ringbuf.New[tea.Msg](10000)
	started := make(chan struct{})

	p := tea.NewProgram(
		Main{
			broadcaster: broadcaster,
			started:     started,
			Model:       m,
		},
		tea.WithContext(ctx),
		tea.WithoutSignals(),
		tea.WithoutRenderer(),
		tea.WithInput(nil),
	)

	input := make(chan tea.Msg)

	return Program{
		ctx:     ctx,
		cancel:  cancel,
		Program: p,
		started: started,
		Send:    input,
		recv:    input,

		broadcast: broadcaster,
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
	// Start a many to one input reader and wrap the unfortunate blocking Send() API
	// provided by tea.Program
	grp.Go(func() error {
		done := p.ctx.Done()
		recv := p.recv
		for {
			select {
			case <-done:
				return nil
			case m := <-recv:
				p.Program.Send(m)
			}
		}
	})
	return p.started
}

type NewClientProgram func(context.Context, tea.Model, ...tea.ProgramOption) *tea.Program

type ClientMain struct {
	Input
	tea.Model

	subscriber *ringbuf.Subscriber[tea.Msg]
	msgs       []tea.Msg
}

func (m *ClientMain) Init() tea.Cmd {
	if m.msgs == nil {
		m.msgs = make([]tea.Msg, 0, 100)
	}

	return tea.Batch(
		func() tea.Msg {
			return m.Input
		},
		m.ReadMsgsCmd(),
		m.Model.Init(),
	)
}

func (m *ClientMain) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)
	switch msg := msg.(type) {
	case tea.Cmd:
		return m, msg

	case []tea.Msg:
		cmds = append(cmds, m.ReadMsgsCmd())
	}

	m.Model, cmd = m.Model.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m *ClientMain) ReadMsgsCmd() tea.Cmd {
	read := m.subscriber
	m.msgs = m.msgs[:0]

	return func() tea.Msg {
		start := time.Now()
		for {
			if len(m.msgs) > 0 {
				// Do a non-blocking check for available messages before blocking on Next
				if !read.Skip(func(tea.Msg) bool { return false }) {
					return m.msgs
				}
				// deadline of 50ms before sending batch
				if time.Since(start) > 50*time.Millisecond {
					return m.msgs
				}
			}

			msg, err := read.Next()
			if err != nil {
				m.msgs = append(m.msgs, err)
				return m.msgs
			}
			m.msgs = append(m.msgs, msg)

		}
	}
}

func (p Program) NewClientProgram() NewClientProgram {
	return func(ctx context.Context, m tea.Model, opts ...tea.ProgramOption) *tea.Program {
		opts = append(opts,
			tea.WithContext(ctx),
			tea.WithoutSignalHandler(),
			tea.WithAltScreen(),
		)
		sub := p.broadcast.Subscribe(ctx, &ringbuf.SubscribeOpts{
			Name:        "TODO",
			StartBehind: 100,
			MaxBehind:   1000,
		})

		return tea.NewProgram(&ClientMain{p.Send, m, sub, nil}, opts...)
	}

}
