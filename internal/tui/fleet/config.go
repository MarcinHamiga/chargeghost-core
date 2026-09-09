package fleet

import (
	"encoding/json"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/chargeghost/engine/internal/api"
	"github.com/chargeghost/engine/internal/tui/form"
)

type configLoadedMsg struct {
	response api.FleetConfigResponse
	err      error
}

type configView struct {
	viewport viewport.Model
	loading  bool
	err      string
}

func newConfigView() configView { return configView{viewport: viewport.New(0, 0)} }

func (v *configView) setSize(width, height int) {
	v.viewport.Width = max(0, width)
	v.viewport.Height = max(0, height-1)
}

func (v *configView) load(cli FleetClient) tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		response, err := cli.FleetConfig()
		return configLoadedMsg{response: response, err: err}
	}
}

func (m Model) updateConfig(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case configLoadedMsg:
		m.config.loading = false
		if msg.err != nil {
			m.config.err = msg.err.Error()
			return m, toastCmd(ToastMsg{Kind: toastError, Err: msg.err})
		}
		m.config.err = ""
		data, err := json.MarshalIndent(msg.response.Config, "", "  ")
		if err != nil {
			m.config.err = err.Error()
			return m, toastCmd(ToastMsg{Kind: toastError, Err: err})
		}
		m.config.viewport.SetContent(string(data))
		m.config.viewport.GotoTop()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.screen = Dashboard
			return m, nil
		case "s":
			return m, form.Open(form.NewConfirm("Save fleet config", "Persist the current in-memory fleet configuration to disk?", actionSaveConfig))
		}
	}
	var cmd tea.Cmd
	m.config.viewport, cmd = m.config.viewport.Update(msg)
	return m, cmd
}

func (v configView) view() string {
	if v.loading {
		return "Loading fleet configuration..."
	}
	if v.err != "" {
		return "Unable to load fleet configuration: " + v.err
	}
	return v.viewport.View()
}
