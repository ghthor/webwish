package chat

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/ghthor/webwish/bubbles/tetris"
	"github.com/ghthor/webwish/mpty"
	"github.com/golang-cz/ringbuf"
)

type NamesReq struct {
	Requestor mpty.ClientId
	Names     []string
}

type ServerModel struct {
	cmds        []tea.Cmd
	broadcaster *ringbuf.RingBuffer[tea.Msg]

	tick time.Time

	names map[string]map[string]time.Time

	tetris *tetris.MPModel
}

func (m *ServerModel) Init() tea.Cmd {
	if m.cmds == nil {
		m.cmds = make([]tea.Cmd, 0, 2)
	}
	if m.names == nil {
		m.names = make(map[string]map[string]time.Time, 10)
	}
	if m.tetris == nil {
		m.tetris = &tetris.MPModel{}
	}
	return tea.Batch(
		func() tea.Msg { return time.Now() },
		m.tetris.Init(),
	)
}

func (m *ServerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.cmds = m.cmds[:0]
	m.UpdateChat(msg)
	m.cmds = append(m.cmds, m.UpdateTetris(msg))
	return m, tea.Batch(m.cmds...)
}

func (m *ServerModel) UpdateChat(msg tea.Msg) {
	switch msg := msg.(type) {
	case *ringbuf.RingBuffer[tea.Msg]:
		m.broadcaster = msg

	case Msg:
		msg.ServerAt = time.Now()
		msg = msg.SetNick()

		if m.broadcaster != nil {
			m.broadcaster.Write(msg)
			log.Debug("chat", "t", msg.ServerAt, "lag", msg.ServerAt.Sub(msg.LocalAt), "who", msg.Who, "sess", msg.Sess, "msg", msg.Str)
		} else {
			log.Warn("dropped chat", "t", msg.ServerAt, "lag", msg.ServerAt.Sub(msg.LocalAt), "who", msg.Who, "sess", msg.Sess, "msg", msg.Str)
		}

	case NamesReq:
		msg.Names = slices.Sorted(maps.Keys(m.names))
		for i := range msg.Names {
			msg.Names[i] = NickFromWho(msg.Names[i])
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

		m.broadcaster.Write(SysMsg(m.tick,
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

		m.broadcaster.Write(SysMsg(m.tick,
			fmt.Sprintf("%s disconnected", msg),
		))

	case time.Time:
		m.tick = msg
	}
}

func (m *ServerModel) UpdateTetris(msg tea.Msg) tea.Cmd {
	cmd := m.tetris.UpdateTetris(msg)
	return cmd
}

func (m *ServerModel) View() string {
	return ""
}
