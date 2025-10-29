package teamodel

import (
	tea "github.com/charmbracelet/bubbletea"
)

type String string

func (m String) Init() tea.Cmd {
	return nil
}

func (m String) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, nil
}

func (m String) View() string {
	return string(m)
}

type ReadonlyView interface {
	View() string
}

type Readonly struct {
	ReadonlyView
}

func (m Readonly) Init() tea.Cmd {
	return nil
}

func (m Readonly) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, nil
}
