package station

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type BackMsg struct{}

type tab struct {
	name  string
	phase int
}

var tabs = []tab{
	{name: "Connectors", phase: 3},
	{name: "Sessions", phase: 3},
	{name: "Reservations", phase: 3},
	{name: "OCPP", phase: 4},
	{name: "Profiles", phase: 5},
	{name: "Auth", phase: 5},
	{name: "Firmware+Diag", phase: 5},
	{name: "Config", phase: 5},
	{name: "Events", phase: 6},
	{name: "Logs", phase: 6},
}

// Model is the station-scoped tab container populated by later phases.
type Model struct {
	stationID string
	active    int
	width     int
	height    int
}

func New(stationID string) Model { return Model{stationID: stationID} }

func (m Model) Init() tea.Cmd { return nil }

func (m *Model) SetSize(width, height int) {
	m.width = max(0, width)
	m.height = max(0, height)
}

func (m Model) StationID() string { return m.stationID }

func (m Model) ActiveTab() int { return m.active }

func (m Model) Help() []string { return []string{"[/] switch tab", "1-9/0 select", "esc fleet"} }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "esc":
		return m, func() tea.Msg { return BackMsg{} }
	case "[":
		m.active = (m.active - 1 + len(tabs)) % len(tabs)
	case "]":
		m.active = (m.active + 1) % len(tabs)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		m.active = int(key.Runes[0] - '1')
	case "0":
		m.active = 9
	}
	return m, nil
}

func (m Model) View() string {
	var labels []string
	for i, item := range tabs {
		style := lipgloss.NewStyle().Padding(0, 1)
		if i == m.active {
			style = style.Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62"))
		} else {
			style = style.Foreground(lipgloss.Color("245"))
		}
		labels = append(labels, style.Render(fmt.Sprintf("%d %s", (i+1)%10, item.name)))
	}
	placeholder := fmt.Sprintf("%s for %s is coming in phase %d.", tabs[m.active].name, m.stationID, tabs[m.active].phase)
	return strings.Join(labels, " ") + "\n\n" + lipgloss.NewStyle().Faint(true).Render(placeholder)
}
