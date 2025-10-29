// mpty provides primitives for creating multiplayer bubbletea applications.
package mpty

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/ghthor/webtea/mpty/mptymsg"
	"github.com/golang-cz/ringbuf"
	"golang.org/x/sync/errgroup"
)

const (
	broadcastRingSz    = 10000
	broadcastLookback  = 7000
	broadcaseMaxBehind = 9000
)

type Input chan<- tea.Msg

type ClientId string

type ClientModel interface {
	tea.Model

	UpdateClient(tea.Msg) (ClientModel, tea.Cmd)

	Id() ClientId
	Err() error
}

type Recorder interface {
	Save(mptymsg.Recordable) (mptymsg.Recordable, error)
	Read(int) ([]mptymsg.Recordable, error)
}

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

type (
	ClientConnectMsg    ClientId
	ClientDisconnectMsg ClientId

	subReq struct {
		ctx  context.Context
		id   ClientId
		resp chan<- subResp
	}
	subResp struct {
		initialMsgs []mptymsg.Recordable
		subscriber  *ringbuf.Subscriber[tea.Msg]
	}
)

type Main struct {
	broadcaster *ringbuf.RingBuffer[tea.Msg]
	recorder    Recorder
	started     chan struct{}
	cmds        []tea.Cmd

	tea.Model
}

func (m *Main) Init() tea.Cmd {
	close(m.started)
	if m.cmds == nil {
		m.cmds = make([]tea.Cmd, 0, 1)
	}
	return tea.Batch(
		func() tea.Msg {
			return m.broadcaster
		},
		tea.Every(time.Second, func(t time.Time) tea.Msg { return t }),
		m.Model.Init(),
	)
}

func (m *Main) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds = m.cmds[:0]
	)

	switch rec := msg.(type) {
	case mptymsg.Recordable:
		var err error
		rec, err = m.recorder.Save(rec)
		if err != nil {
			log.Warn("message recording", "error", err)
		} else {
			msg = rec
		}
	}

	switch msg := msg.(type) {
	case subReq:
		// TODO: configurable default read len
		init, err := m.recorder.Read(100)
		if err != nil {
			log.Warn("failed to load recorded messages", "error", err)
		}

		sub := m.broadcaster.Subscribe(msg.ctx, &ringbuf.SubscribeOpts{
			Name:        string(msg.id),
			StartBehind: 0,
			MaxBehind:   broadcaseMaxBehind,
		})
		return m, func() tea.Msg {
			select {
			case <-msg.ctx.Done():
			case msg.resp <- subResp{
				initialMsgs: init,
				subscriber:  sub,
			}:
			}
			return nil
		}

	case ClientConnectMsg:
		log.Info("connected", "id", msg)
		m.broadcaster.Write(msg)

	case ClientDisconnectMsg:
		log.Info("disconnected", "id", msg)
		m.broadcaster.Write(msg)

	case time.Time:
		// These ticks are important for periodically waking any subscribers
		// that may need to exit but are completely caught up and sitting on
		// the wake condition. Becuase of this race, if the subscriber is
		// waiting and the broadcast channel is quiet the tea.Program can never
		// exit. These ticks ensure that any tea.Program will get to exit when
		// it has a running command that is stuck on a subscriber holding the
		// ringbuffer mutex
		m.broadcaster.Write(msg)
		cmds = append(cmds, tea.Every(time.Second, func(t time.Time) tea.Msg { return t }))
	}

	m.Model, cmd = m.Model.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func NewProgram(ctx context.Context, cancel context.CancelCauseFunc, m tea.Model, r Recorder) Program {
	broadcaster := ringbuf.New[tea.Msg](broadcastRingSz)
	started := make(chan struct{})

	p := tea.NewProgram(
		&Main{
			broadcaster: broadcaster,
			recorder:    r,
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

func (p Program) StartIn(ctx context.Context, grp *errgroup.Group) error {
	grp.Go(func() error {
		_, serr := p.Program.Run()
		if serr != nil && !errors.Is(serr, context.Canceled) {
			p.cancel(serr)
			return serr
		}

		// Send one last pulse on the the ringbuffer unblock any subscribers
		p.broadcast.Write(ctx.Err())

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

	select {
	case <-ctx.Done():
		return p.ctx.Err()
	case <-p.started:
		return nil
	}
}

type NewClientProgram func(context.Context, ClientModel, ...tea.ProgramOption) *tea.Program

type ClientMain struct {
	Input
	ClientModel

	initialMsgs []mptymsg.Recordable
	subscriber  *ringbuf.Subscriber[tea.Msg]
	msgs        []tea.Msg

	// The tea.Program does not have safe way to wait for it to exit until
	// AFTER it has started running. So to schedule disconnect messages when
	// the program exits, we have to wait till the model Init() func is called
	// and return a tea.Cmd to wait on it
	program *tea.Program
}

func (m *ClientMain) Init() tea.Cmd {
	if m.msgs == nil {
		m.msgs = make([]tea.Msg, 0, 100)
	}

	id := m.Id()

	return tea.Sequence(
		m.ClientModel.Init(),
		func() tea.Msg {
			return m.Input
		},
		func() tea.Msg {
			// TODO: these bare ch sends could leak, but I'm pretty sure only
			// when the Main program is exitting so the whole process would be
			// about to exit
			m.Input <- ClientConnectMsg(id)
			return tea.Cmd(func() tea.Msg {
				m.program.Wait()
				m.Input <- ClientDisconnectMsg(id)
				return nil
			})
		},
		func() tea.Msg {
			msgs := m.initialMsgs
			m.initialMsgs = nil
			return msgs
		},
		m.ReadMsgsCmd(),
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

	m.ClientModel, cmd = m.ClientModel.UpdateClient(msg)
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
	return func(ctx context.Context, m ClientModel, opts ...tea.ProgramOption) *tea.Program {
		opts = append(opts,
			tea.WithContext(ctx),
			tea.WithoutSignalHandler(),
			tea.WithAltScreen(),
		)

		respCh := make(chan subResp, 1)
		select {
		case <-ctx.Done():
			return nil
		case p.Send <- subReq{ctx, m.Id(), respCh}:
		}

		var resp subResp
		select {
		case <-ctx.Done():
			return nil
		case resp = <-respCh:
		}

		main := &ClientMain{p.Send, m, resp.initialMsgs, resp.subscriber, nil, nil}
		p := tea.NewProgram(main, opts...)
		main.program = p
		return p
	}

}
