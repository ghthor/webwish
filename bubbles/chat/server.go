package chat

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/ghthor/webtea/bubbles/blokfall"
	"github.com/ghthor/webtea/mpty"
	"github.com/golang-cz/ringbuf"
)

type NamesReq struct {
	Requestor mpty.ClientId
	Names     []string
}

type WhoisReq struct {
	Requestor mpty.ClientId
	User      string
	Results   []string
}

type ServerModel struct {
	cmds        []tea.Cmd
	broadcaster *ringbuf.RingBuffer[tea.Msg]

	tick time.Time

	names map[string]map[string]time.Time

	blokfall *blokfall.MPModel
}

func (m *ServerModel) Init() tea.Cmd {
	if m.cmds == nil {
		m.cmds = make([]tea.Cmd, 0, 2)
	}
	if m.names == nil {
		m.names = make(map[string]map[string]time.Time, 10)
	}
	if m.blokfall == nil {
		m.blokfall = &blokfall.MPModel{}
	}
	return tea.Batch(
		func() tea.Msg { return time.Now() },
		m.blokfall.Init(),
	)
}

func (m *ServerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.cmds = m.cmds[:0]
	m.UpdateChat(msg)
	m.cmds = append(m.cmds, m.UpdateBlokFall(msg))
	return m, tea.Batch(m.cmds...)
}

func (m *ServerModel) UpdateChat(msg tea.Msg) {
	switch msg := msg.(type) {
	case *ringbuf.RingBuffer[tea.Msg]:
		m.broadcaster = msg

	case Msg:
		lag := time.Since(msg.At)
		if m.broadcaster != nil {
			m.broadcaster.Write(msg)
			log.Debug("chat", "t", msg.At, "lag", lag, "who", msg.Who, "sess", msg.Sess, "msg", msg.Str)
		} else {
			log.Warn("dropped chat", "t", msg.At, "lag", lag, "who", msg.Who, "sess", msg.Sess, "msg", msg.Str)
		}

	case NamesReq:
		msg.Names = slices.Sorted(maps.Keys(m.names))
		for i := range msg.Names {
			msg.Names[i] = NickFromWho(msg.Names[i])
		}
		m.broadcaster.Write(msg)

	case WhoisReq:
		m.broadcaster.Write(m.whoisReq(msg))

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

func (m *ServerModel) UpdateBlokFall(msg tea.Msg) tea.Cmd {
	cmd := m.blokfall.UpdateBlokFall(msg)
	return cmd
}

func (m *ServerModel) View() string {
	return ""
}

func FormatTimeAsAge(t time.Time, now time.Time) string {
	age := now.Sub(t)
	s, m := age.Seconds(), age.Minutes()
	switch {
	case s < 1:
		return "0 s"
	case m < 1:
		return fmt.Sprintf("%.f s", s)
	case m/60 < 1:
		return fmt.Sprintf("%.f m", m)
	case m/60 < 24:
		return fmt.Sprintf("%.f h", m/60)
	case m/60/24 < 7:
		return fmt.Sprintf("%.f d", m/60/24)
	default:
		return fmt.Sprintf("%.f w", m/60/24/7)
	}
}

func (m *ServerModel) whoisReq(r WhoisReq) WhoisReq {
	sessions, ok := m.names[r.User]
	if ok {
		for sess := range sessions {
			r.Results = append(r.Results, fmt.Sprintf("%s %s", r.User, sess))
		}
		return r
	}
	for who, sessions := range m.names {
		if strings.HasPrefix(who, r.User) {
			for sess, since := range sessions {
				r.Results = append(r.Results, fmt.Sprintf("%s %s (%s)", who, sess, FormatTimeAsAge(since, m.tick)))
			}
		}
	}
	return r
}
