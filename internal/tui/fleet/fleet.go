package fleet

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/chargeghost/engine/internal/api"
	"github.com/chargeghost/engine/internal/client"
	"github.com/chargeghost/engine/internal/config"
	"github.com/chargeghost/engine/internal/tui/form"
)

// FleetClient is the REST surface used by the phase 2 fleet views.
type FleetClient interface {
	FleetStatus() (api.FleetStatusResponse, error)
	FleetConfig() (api.FleetConfigResponse, error)
	SaveFleetConfig() (api.Response, error)
	Operations() ([]api.Operation, error)
	Operation(string) (api.Operation, error)
	CreateStation(api.CreateStationRequest) (api.OperationResponse, error)
	StartStation(string) (api.OperationResponse, error)
	StopStation(string) (api.OperationResponse, error)
	RestartStation(string) (api.OperationResponse, error)
	EnableStation(string) (api.OperationResponse, error)
	DisableStation(string) (api.OperationResponse, error)
	ReloadStation(string) (api.Response, error)
	PersistStation(string) (api.Response, error)
	DeleteStation(string, client.DeleteStationOptions) (api.Response, error)
}

type Screen int

const (
	Dashboard Screen = iota
	Config
	Operations
)

const (
	actionStart        = "fleet.start"
	actionStop         = "fleet.stop"
	actionRestart      = "fleet.restart"
	actionReload       = "fleet.reload"
	actionEnable       = "fleet.enable"
	actionDisable      = "fleet.disable"
	actionDelete       = "fleet.delete"
	actionCreate       = "fleet.create"
	actionPersist      = "fleet.persist"
	actionSaveConfig   = "fleet.config.save"
	toastSuccess       = "success"
	toastError         = "error"
	toastInfo          = "info"
	defaultTableHeight = 8
)

// TickMsg merges live station snapshots without changing inventory order.
type TickMsg map[string]client.Tick

// FleetTickMsg replaces the complete set of live station snapshots.
type FleetTickMsg map[string]client.Tick

// EventMsg notifies the fleet model of an inventory or operation event.
type EventMsg client.Event

// OpenStationMsg asks the root app to enter a station container.
type OpenStationMsg struct{ StationID string }

// ToastMsg asks the root app to show user-visible feedback.
type ToastMsg struct {
	Kind    string
	Message string
	Err     error
}

type statusLoadedMsg struct {
	status api.FleetStatusResponse
	err    error
}

type mutationResultMsg struct {
	message     string
	operationID string
	err         error
}

// Model owns dashboard inventory and the fleet config/operations subviews.
type Model struct {
	cli       FleetClient
	table     table.Model
	width     int
	height    int
	screen    Screen
	stations  []api.StationSnapshot
	ticks     map[string]client.Tick
	defaultID string
	pendingID string
	config    configView
	ops       operationsView
}

func New(cli FleetClient) Model {
	columns := []table.Column{
		{Title: "Station", Width: 20},
		{Title: "Lifecycle", Width: 13},
		{Title: "OCPP", Width: 10},
		{Title: "Connectors", Width: 16},
		{Title: "Sessions", Width: 9},
		{Title: "Uptime", Width: 12},
	}
	t := table.New(table.WithColumns(columns), table.WithFocused(true), table.WithHeight(defaultTableHeight))
	styles := table.DefaultStyles()
	styles.Header = styles.Header.Bold(true).Foreground(lipgloss.Color("87")).BorderStyle(lipgloss.NormalBorder()).BorderBottom(true)
	styles.Selected = styles.Selected.Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62")).Bold(true)
	t.SetStyles(styles)
	return Model{
		cli: cli, table: t, ticks: make(map[string]client.Tick),
		config: newConfigView(), ops: newOperationsView(),
	}
}

func (m Model) Init() tea.Cmd { return m.Refresh() }

func (m Model) Refresh() tea.Cmd {
	if m.cli == nil {
		return nil
	}
	return func() tea.Msg {
		status, err := m.cli.FleetStatus()
		return statusLoadedMsg{status: status, err: err}
	}
}

func (m *Model) SetSize(width, height int) {
	m.width = max(0, width)
	m.height = max(0, height)
	m.table.SetWidth(m.width)
	m.table.SetHeight(max(1, m.height-3))
	m.config.setSize(m.width, m.height)
	m.ops.setSize(m.width, m.height)
}

func (m Model) Title() string {
	switch m.screen {
	case Config:
		return "Fleet / Configuration"
	case Operations:
		return "Fleet / Operations"
	default:
		return "Fleet"
	}
}

func (m Model) Help() []string {
	switch m.screen {
	case Config:
		return []string{"s save", "up/down scroll", "esc dashboard"}
	case Operations:
		if m.ops.inDetail {
			return []string{"up/down scroll", "esc operations"}
		}
		return []string{"enter detail", "r refresh", "esc dashboard"}
	default:
		return []string{"enter open", "s/x start/stop", "r restart", "R reload", "e/d enable/disable", "D delete", "n new", "f config", "O operations", "v persist"}
	}
}

func (m Model) Screen() Screen { return m.screen }

func (m Model) SelectedStationID() string {
	row := m.table.SelectedRow()
	if len(row) == 0 {
		return ""
	}
	index := m.table.Cursor()
	if index < 0 || index >= len(m.stations) {
		return ""
	}
	return m.stations[index].StationID
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case statusLoadedMsg:
		if msg.err != nil {
			return m, toastCmd(ToastMsg{Kind: toastError, Err: msg.err})
		}
		m.applyStatus(msg.status)
		return m, nil
	case TickMsg:
		for id, tick := range msg {
			m.ticks[id] = tick
		}
		m.rebuildRows()
		return m, nil
	case FleetTickMsg:
		m.ticks = make(map[string]client.Tick, len(msg))
		for id, tick := range msg {
			m.ticks[id] = tick
		}
		m.rebuildRows()
		return m, nil
	case EventMsg:
		typeName := client.Event(msg).Type
		refreshInventory := eventRefreshesInventory(typeName)
		refreshOperations := strings.HasPrefix(typeName, "station_operation_") && m.screen == Operations
		if refreshInventory && refreshOperations {
			return m, tea.Batch(m.Refresh(), m.ops.refresh(m.cli))
		}
		if refreshInventory {
			return m, m.Refresh()
		}
		if refreshOperations {
			return m, m.ops.refresh(m.cli)
		}
		return m, nil
	case mutationResultMsg:
		if msg.err != nil {
			return m, toastCmd(ToastMsg{Kind: toastError, Err: msg.err})
		}
		text := msg.message
		if text == "" {
			text = "request completed"
		}
		if msg.operationID != "" {
			text += " (op " + msg.operationID + ")"
		}
		return m, tea.Batch(toastCmd(ToastMsg{Kind: toastSuccess, Message: text}), m.Refresh())
	case form.ResultMsg:
		return m.handleResult(msg)
	}

	if m.screen == Config {
		return m.updateConfig(msg)
	}
	if m.screen == Operations {
		return m.updateOperations(msg)
	}
	return m.updateDashboard(msg)
}

func (m Model) updateDashboard(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd
	}
	id := m.SelectedStationID()
	switch key.String() {
	case "enter":
		if id != "" {
			return m, func() tea.Msg { return OpenStationMsg{StationID: id} }
		}
	case "s":
		return m.openStationConfirm(actionStart, id, "Start station", "Request station start?")
	case "x":
		return m.openStationConfirm(actionStop, id, "Stop station", "Request station stop?")
	case "r":
		return m.openStationConfirm(actionRestart, id, "Restart station", "Request a full station restart?")
	case "R":
		return m.openStationConfirm(actionReload, id, "Reload station", "Reload this station's configuration from disk?")
	case "e":
		return m.openStationConfirm(actionEnable, id, "Enable station", "Enable this station?")
	case "d":
		return m.openStationConfirm(actionDisable, id, "Disable station", "Disable this station?")
	case "D":
		if id != "" {
			m.pendingID = id
			modal := form.NewTypedConfirm("Delete station", "This removes the station configuration. Runtime state and credentials are retained.", actionDelete, id)
			return m, form.Open(modal)
		}
	case "n":
		return m, form.Open(newStationForm())
	case "f":
		m.screen = Config
		return m, m.config.load(m.cli)
	case "O":
		m.screen = Operations
		return m, m.ops.refresh(m.cli)
	case "v":
		return m.openStationConfirm(actionPersist, id, "Persist station", "Persist this station's runtime state now?")
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m Model) openStationConfirm(action, id, title, body string) (Model, tea.Cmd) {
	if id == "" {
		return m, nil
	}
	m.pendingID = id
	return m, form.Open(form.NewConfirm(title, body+"\n\nStation: "+id, action))
}

func (m Model) handleResult(result form.ResultMsg) (Model, tea.Cmd) {
	if !result.OK {
		m.pendingID = ""
		return m, nil
	}
	id := m.pendingID
	m.pendingID = ""
	switch result.Action {
	case actionStart:
		return m, operationCmd(func() (api.OperationResponse, error) { return m.cli.StartStation(id) })
	case actionStop:
		return m, operationCmd(func() (api.OperationResponse, error) { return m.cli.StopStation(id) })
	case actionRestart:
		return m, operationCmd(func() (api.OperationResponse, error) { return m.cli.RestartStation(id) })
	case actionEnable:
		return m, operationCmd(func() (api.OperationResponse, error) { return m.cli.EnableStation(id) })
	case actionDisable:
		return m, operationCmd(func() (api.OperationResponse, error) { return m.cli.DisableStation(id) })
	case actionReload:
		return m, responseCmd(func() (api.Response, error) { return m.cli.ReloadStation(id) })
	case actionPersist:
		return m, responseCmd(func() (api.Response, error) { return m.cli.PersistStation(id) })
	case actionDelete:
		return m, responseCmd(func() (api.Response, error) {
			return m.cli.DeleteStation(id, client.DeleteStationOptions{AllowEmpty: true})
		})
	case actionCreate:
		req, err := stationRequest(result.Values)
		if err != nil {
			return m, toastCmd(ToastMsg{Kind: toastError, Err: err})
		}
		return m, operationCmd(func() (api.OperationResponse, error) { return m.cli.CreateStation(req) })
	case actionSaveConfig:
		return m, responseCmd(m.cli.SaveFleetConfig)
	}
	return m, nil
}

func (m *Model) applyStatus(status api.FleetStatusResponse) {
	selected := m.SelectedStationID()
	m.defaultID = status.DefaultStationID
	m.stations = append([]api.StationSnapshot(nil), status.Stations...)
	sort.SliceStable(m.stations, func(i, j int) bool { return m.stations[i].StationID < m.stations[j].StationID })
	m.rebuildRows()
	if selected != "" {
		for i := range m.stations {
			if m.stations[i].StationID == selected {
				m.table.SetCursor(i)
				return
			}
		}
	}
	if len(m.stations) > 0 {
		m.table.SetCursor(0)
	}
}

func (m *Model) rebuildRows() {
	rows := make([]table.Row, 0, len(m.stations))
	for _, station := range m.stations {
		name := station.StationID
		if name == m.defaultID {
			name = lipgloss.NewStyle().Bold(true).Render("* " + name)
		}
		connected := station.Connected
		connectors := strconv.Itoa(station.ConnectorCount)
		sessions := station.ActiveSessionCount
		uptime := station.UptimeSeconds
		if tick, ok := m.ticks[station.StationID]; ok {
			connected = tick.OCPPConnected
			connectors = connectorSummary(tick.Connectors)
			sessions = len(tick.ActiveSessions)
			uptime = tick.UptimeSeconds
		}
		ocpp := "offline"
		if connected {
			ocpp = "connected"
		}
		rows = append(rows, table.Row{name, statusStyle(station.LifecycleState).Render(station.LifecycleState), statusStyle(ocpp).Render(ocpp), connectors, strconv.Itoa(sessions), formatUptime(uptime)})
	}
	m.table.SetRows(rows)
}

func connectorSummary(connectors []client.TickConnector) string {
	if len(connectors) == 0 {
		return "0"
	}
	active := 0
	for _, connector := range connectors {
		if connector.Status == "Charging" {
			active++
		}
	}
	if active == 0 {
		return strconv.Itoa(len(connectors))
	}
	return fmt.Sprintf("%d (%d charging)", len(connectors), active)
}

func formatUptime(seconds float64) string {
	if seconds <= 0 {
		return "-"
	}
	return (time.Duration(seconds) * time.Second).Round(time.Second).String()
}

func eventRefreshesInventory(eventType string) bool {
	switch eventType {
	case "station_operation_completed", "station_operation_failed", "station_lifecycle_changed", "station_added", "station_removed", "station_config_changed", "station_restart_required_changed", "fleet_config_saved":
		return true
	default:
		return false
	}
}

func statusStyle(status string) lipgloss.Style {
	style := lipgloss.NewStyle()
	switch strings.ToLower(status) {
	case "available", "running", "connected", "succeeded", "completed":
		return style.Foreground(lipgloss.Color("82"))
	case "charging":
		return style.Foreground(lipgloss.Color("154"))
	case "suspended", "starting", "stopping", "restarting":
		return style.Foreground(lipgloss.Color("214"))
	case "faulted", "failed", "offline":
		return style.Foreground(lipgloss.Color("196"))
	case "reserved":
		return style.Foreground(lipgloss.Color("87"))
	case "finishing", "configured":
		return style.Foreground(lipgloss.Color("245"))
	case "unavailable", "disabled", "stopped":
		return style.Foreground(lipgloss.Color("240"))
	default:
		return style
	}
}

func newStationForm() form.Modal {
	countMin, countMax := form.Range(1, 64)
	voltageMin, voltageMax := form.Range(120, 1000)
	currentMin, currentMax := form.Range(6, 150)
	return form.New("New station", actionCreate, []form.Field{
		{Name: "station_id", Label: "Station ID", Kind: form.Text},
		{Name: "ocpp_id", Label: "OCPP ID", Kind: form.Text, Required: true},
		{Name: "version", Label: "OCPP version", Kind: form.Select, Value: "1.6", Required: true, Options: []string{"1.6", "2.0.1"}},
		{Name: "url", Label: "CSMS URL", Kind: form.Text, Value: "wss://localhost:3000", Required: true},
		{Name: "connector_count", Label: "Connector count", Kind: form.Number, Value: "1", Required: true, Min: countMin, Max: countMax, Validate: func(value string) error {
			if _, err := strconv.Atoi(value); err != nil {
				return fmt.Errorf("Connector count must be an integer")
			}
			return nil
		}},
		{Name: "voltage", Label: "Voltage", Kind: form.Number, Value: "230", Required: true, Min: voltageMin, Max: voltageMax},
		{Name: "current", Label: "Current", Kind: form.Number, Value: "32", Required: true, Min: currentMin, Max: currentMax},
		{Name: "phase", Label: "Phase", Kind: form.Select, Value: "1", Required: true, Options: []string{"1", "3"}},
		{Name: "enabled", Label: "Enabled", Kind: form.Toggle, Value: "true"},
		{Name: "start", Label: "Start now", Kind: form.Toggle, Value: "true"},
		{Name: "save", Label: "Save config", Kind: form.Toggle, Value: "true"},
	})
}

func stationRequest(values map[string]string) (api.CreateStationRequest, error) {
	count, err := strconv.Atoi(values["connector_count"])
	if err != nil {
		return api.CreateStationRequest{}, fmt.Errorf("connector count must be an integer")
	}
	voltage, err := strconv.ParseFloat(values["voltage"], 64)
	if err != nil {
		return api.CreateStationRequest{}, fmt.Errorf("voltage must be a number")
	}
	current, err := strconv.ParseFloat(values["current"], 64)
	if err != nil {
		return api.CreateStationRequest{}, fmt.Errorf("current must be a number")
	}
	phase, err := strconv.Atoi(values["phase"])
	if err != nil {
		return api.CreateStationRequest{}, fmt.Errorf("phase must be an integer")
	}
	if count < 1 || count > 64 || voltage < 120 || voltage > 1000 || current < 6 || current > 150 || (phase != 1 && phase != 3) {
		return api.CreateStationRequest{}, fmt.Errorf("connector settings are out of range")
	}
	connectors := make([]config.ConnectorConfig, count)
	for i := range connectors {
		connectors[i] = config.ConnectorConfig{Voltage: voltage, Current: current, Phase: phase}
	}
	return api.CreateStationRequest{
		ID: values["station_id"], OCPPID: values["ocpp_id"], OCPPVersion: values["version"], ConnectionURL: values["url"],
		Connectors: connectors, Enabled: values["enabled"] == "true", Start: values["start"] == "true", Save: values["save"] == "true",
	}, nil
}

func operationCmd(call func() (api.OperationResponse, error)) tea.Cmd {
	return func() tea.Msg {
		response, err := call()
		return mutationResultMsg{message: response.Message, operationID: response.OperationID, err: err}
	}
}

func responseCmd(call func() (api.Response, error)) tea.Cmd {
	return func() tea.Msg {
		response, err := call()
		return mutationResultMsg{message: response.Message, err: err}
	}
}

func toastCmd(msg ToastMsg) tea.Cmd { return func() tea.Msg { return msg } }

func (m Model) View() string {
	switch m.screen {
	case Config:
		return m.config.view()
	case Operations:
		return m.ops.view()
	default:
		if len(m.stations) == 0 {
			return "No stations configured. Press n to create one."
		}
		return m.table.View()
	}
}
