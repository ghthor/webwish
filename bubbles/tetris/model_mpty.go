package tetris

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ghthor/webwish/mpty"
	"github.com/golang-cz/ringbuf"
)

type (
	MPConnectPlayerMsg    mpty.ClientId
	MPDisconnectPlayerMsg mpty.ClientId

	MPView  *string
	MPInput struct {
		Id  mpty.ClientId
		Cmd Input
	}
)

type MPModel struct {
	broadcaster *ringbuf.RingBuffer[tea.Msg]

	tetris *Model

	players  map[mpty.ClientId]struct{}
	inputs   map[mpty.ClientId]Input
	inputTs  map[mpty.ClientId]int64
	inputSum map[Input]int
}

func (m *MPModel) Init() tea.Cmd {
	if m.players == nil {
		m.players = make(map[mpty.ClientId]struct{}, 10)
		m.inputs = make(map[mpty.ClientId]Input, 10)
		m.inputTs = make(map[mpty.ClientId]int64, 10)
		m.inputSum = make(map[Input]int)
	}

	return nil
}

func (m *MPModel) UpdateTetris(msg tea.Msg) tea.Cmd {
	tetrisMsg := msg

	switch msg := msg.(type) {
	case *ringbuf.RingBuffer[tea.Msg]:
		m.broadcaster = msg

	case MPConnectPlayerMsg:
		m.players[mpty.ClientId(msg)] = struct{}{}

		var cmd tea.Cmd
		if m.tetris == nil {
			m.tetris = New()
			cmd = m.tetris.Init()
		}
		m.broadcaster.Write(m.tetrisView())
		return cmd

	case MPDisconnectPlayerMsg:
		delete(m.players, mpty.ClientId(msg))
		delete(m.inputs, mpty.ClientId(msg))
		delete(m.inputTs, mpty.ClientId(msg))

		if len(m.players) == 0 {
			m.broadcaster.Write(MPView(nil))
			m.tetris = nil
		} else {
			m.broadcaster.Write(m.tetrisView())
		}
		return nil

	case mpty.ClientDisconnectMsg:
		delete(m.players, mpty.ClientId(msg))
		delete(m.inputs, mpty.ClientId(msg))
		delete(m.inputTs, mpty.ClientId(msg))

		if len(m.players) == 0 {
			m.broadcaster.Write(MPView(nil))
			m.tetris = nil
		} else {
			m.broadcaster.Write(m.tetrisView())
		}
		return nil

	case MPInput:
		i := m.tetrisInput(msg)
		if i == InputNone {
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

func (m *MPModel) tetrisInput(msg MPInput) Input {
	clear(m.inputSum)
	m.inputs[msg.Id] = msg.Cmd
	m.inputTs[msg.Id] = time.Now().UnixNano()

	half := len(m.inputs) / 2
	// half := len(m.tetrisPlayers)

	for _, input := range m.inputs {
		s := m.inputSum[input]
		s++
		if s >= half {
			clear(m.inputs)
			clear(m.inputTs)
			return input
		}
		m.inputSum[input] = s
		continue
	}

	return InputNone
}

func (m *MPModel) tetrisView() MPView {
	// TODO: players list
	// TODO: inputs list
	inputs := ""
	inputs = m.tetrisInputView()
	v := m.tetris.View()
	v = lipgloss.JoinHorizontal(lipgloss.Top, inputs, v)
	return MPView(&v)
}

func (m *MPModel) tetrisInputView() string {
	type pair struct {
		mpty.ClientId
		ts int64
	}
	ins := make([]pair, 0, len(m.inputTs))
	for k, v := range m.inputTs {
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
		fmt.Fprintln(&b, string(InputRune[m.inputs[pair.ClientId]]))
	}
	return b.String()
}
