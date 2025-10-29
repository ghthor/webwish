package blokfall

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ghthor/webtea/mpty"
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

	blokfall *Model

	players map[mpty.ClientId]int
}

func (m *MPModel) Init() tea.Cmd {
	if m.players == nil {
		m.players = make(map[mpty.ClientId]int, 10)
	}

	return nil
}

func (m *MPModel) UpdateBlokFall(msg tea.Msg) tea.Cmd {
	var (
		cmd       tea.Cmd
		cmds      []tea.Cmd
		blokfallMsg = msg
	)

	switch msg := msg.(type) {
	case *ringbuf.RingBuffer[tea.Msg]:
		m.broadcaster = msg

	case MPConnectPlayerMsg:
		if _, ok := m.players[mpty.ClientId(msg)]; ok {
			break
		}

		if m.blokfall == nil {
			m.blokfall = New()
			cmds = append(cmds, m.blokfall.Init())
		}

		m.players[mpty.ClientId(msg)], cmd = m.blokfall.InsertNewPiece()
		cmds = append(cmds, cmd)

		// TODO: system connected to blokfall
		m.broadcaster.Write(m.blokfallView())
		return tea.Batch(cmds...)

	case MPDisconnectPlayerMsg:
		// TODO: system disconnected from blokfall
		m.removePlayer(mpty.ClientId(msg))
	case mpty.ClientDisconnectMsg:
		// TODO: system disconnected from blokfall
		m.removePlayer(mpty.ClientId(msg))

	case MPInput:
		piece := m.players[msg.Id]
		blokfallMsg = MultiPieceInput{
			msg.Cmd,
			piece,
		}
	}

	if m.blokfall != nil {
		var (
			cmd      tea.Cmd
			modified bool
		)
		m.blokfall, cmd, modified = m.blokfall.UpdateBlokFallShouldRender(blokfallMsg)
		if modified {
			m.broadcaster.Write(m.blokfallView())
		}
		return cmd
	}

	return nil
}

func (m *MPModel) removePlayer(id mpty.ClientId) {
	if piece, ok := m.players[id]; ok {
		delete(m.players, id)
		m.blokfall.RemovePiece(piece)
	}

	if len(m.players) == 0 {
		m.broadcaster.Write(MPView(nil))
		m.blokfall = nil
	} else {
		m.broadcaster.Write(m.blokfallView())
	}
}

func (m *MPModel) blokfallView() MPView {
	// TODO: players list
	inputs := ""
	v := m.blokfall.View()
	v = lipgloss.JoinHorizontal(lipgloss.Top, inputs, v)
	return MPView(&v)
}
