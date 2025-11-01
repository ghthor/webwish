package mpty

import (
	"fmt"
	"net"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/ssh"
	"tailscale.com/client/tailscale/apitype"
)

var (
	Bold = lipgloss.NewStyle().Bold(true)
)

type Session interface {
	RemoteAddr() net.Addr
}

type ClientInfoModel struct {
	b strings.Builder

	Term   string
	Width  int
	Height int
	Time   time.Time

	Sess Session
	Who  *apitype.WhoIsResponse
}

func NewClientInfoModelFromSsh(pty ssh.Pty, sess Session, who *apitype.WhoIsResponse) *ClientInfoModel {
	return &ClientInfoModel{
		Term:   pty.Term,
		Width:  pty.Window.Width,
		Height: pty.Window.Height,
		Time:   time.Now(),

		Sess: sess,
		Who:  who,
	}
}

func NewClientInfoModelFromWebtty(sess Session, who *apitype.WhoIsResponse) *ClientInfoModel {
	return &ClientInfoModel{
		Term:   "webtty",
		Width:  80,
		Height: 40,
		Time:   time.Now(),

		Sess: sess,
		Who:  who,
	}
}

func (m *ClientInfoModel) Id() ClientId {
	return ClientId(m.Who.UserProfile.LoginName + " " + m.Sess.RemoteAddr().String())
}

func (m *ClientInfoModel) Init() tea.Cmd {
	return nil
}

func (m *ClientInfoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := m.UpdateInfo(msg)
	return m, cmd
}

func (m *ClientInfoModel) UpdateInfo(msg tea.Msg) (*ClientInfoModel, tea.Cmd) {
	switch msg := msg.(type) {

	case time.Time:
		m.Time = msg

	case tea.WindowSizeMsg:
		m.Height = msg.Height
		m.Width = msg.Width
	}
	return m, nil
}

func (m *ClientInfoModel) View() string {
	b := &m.b
	b.Reset()
	fmt.Fprintf(b, "  who: %s\n", m.Who.UserProfile.LoginName)
	fmt.Fprintf(b, "raddr: %s\n", m.Sess.RemoteAddr().String())
	fmt.Fprintf(b, " term: %s\n", m.Term)
	fmt.Fprintf(b, " size: (%d,%d)\n", m.Width, m.Height)
	fmt.Fprintf(b, " time: %s\n", Bold.Render(m.Time.Format(time.RFC1123)))

	return b.String()
}

func (m *ClientInfoModel) ViewHeight() int {
	return 5
}
