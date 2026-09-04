package tui

import (
	"encoding/json"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/chargeghost/engine/internal/api"
	"github.com/chargeghost/engine/internal/client"
	"github.com/chargeghost/engine/internal/logging"
	"github.com/chargeghost/engine/internal/tui/fleet"
	"github.com/chargeghost/engine/internal/tui/form"
	"github.com/chargeghost/engine/internal/tui/station"
)

type View int

const (
	ViewFleet View = iota
	ViewStation
)

const actionQuit = "app.quit"

// App is the root Bubble Tea model for shared chrome and navigation.
type App struct {
	cli                 *client.Client
	events              <-chan client.Event
	ring                *logging.Ring
	addr                string
	width               int
	height              int
	view                View
	fleet               fleet.Model
	station             *station.Model
	toasts              []toast
	nextID              int
	modal               form.Modal
	modalOwner          View
	suspendedModal      form.Modal
	suspendedModalOwner View
	connState           string
	helpOpen            bool
	theme               themeStyles
}

func NewApp(cli *client.Client, events *client.Events, ring *logging.Ring, addr string) App {
	var eventChan <-chan client.Event
	if events != nil {
		eventChan = events.Chan()
	}
	return newApp(cli, eventChan, ring, addr)
}

func newApp(cli *client.Client, events <-chan client.Event, ring *logging.Ring, addr string) App {
	return App{
		cli: cli, events: events, ring: ring, addr: addr,
		fleet: fleet.New(cli), theme: newTheme(),
	}
}

func (m App) Init() tea.Cmd {
	return tea.Batch(tea.WindowSize(), m.fleet.Init(), waitForEvent(m.events))
}

func (m App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case eventStreamClosedMsg:
		m.events = nil
		m.connState = "disconnected"
		return m, nil
	case clientEventMsg:
		cmds := []tea.Cmd{waitForEvent(m.events)}
		cmds = append(cmds, m.handleClientEvent(client.Event(msg))...)
		return m, tea.Batch(cmds...)
	case form.OpenMsg:
		m.modal = msg.Modal
		m.modalOwner = m.view
		m.modal.SetSize(m.width, m.height)
		return m, m.modal.Init()
	case form.ResultMsg:
		owner := m.modalOwner
		m.modal = nil
		if msg.Action == actionQuit {
			if msg.OK {
				m.suspendedModal = nil
				return m, tea.Quit
			}
			m.modal = m.suspendedModal
			m.modalOwner = m.suspendedModalOwner
			m.suspendedModal = nil
			return m, nil
		}
		m.suspendedModal = nil
		if owner == ViewStation && m.station != nil {
			updated, cmd := m.station.Update(msg)
			m.station = &updated
			return m, cmd
		}
		var cmd tea.Cmd
		m.fleet, cmd = m.fleet.Update(msg)
		return m, cmd
	case fleet.OpenStationMsg:
		model := station.New(msg.StationID)
		model.SetSize(m.width, m.contentHeight())
		m.station = &model
		m.view = ViewStation
		return m, model.Init()
	case station.BackMsg:
		m.station = nil
		m.view = ViewFleet
		return m, nil
	case fleet.ToastMsg:
		kind := toastInfo
		if msg.Kind == "success" {
			kind = toastSuccess
		} else if msg.Kind == "error" {
			kind = toastError
		}
		message := msg.Message
		if msg.Err != nil {
			message = formatError(msg.Err)
		}
		return m, m.addToast(kind, message)
	case toastExpireMsg:
		m.removeToast(int(msg))
		return m, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		if key.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		if m.modal != nil {
			if key.String() == keyQuit && m.modal.Dirty() {
				m.suspendedModal = m.modal
				m.suspendedModalOwner = m.modalOwner
				m.modal = form.NewConfirm("Quit ChargeGhost", "Discard unsaved changes and quit?", actionQuit)
				m.modal.SetSize(m.width, m.height)
				return m, m.modal.Init()
			}
			var cmd tea.Cmd
			m.modal, cmd = m.modal.Update(msg)
			return m, cmd
		}
		if m.helpOpen {
			if key.String() == keyHelp || key.String() == "esc" {
				m.helpOpen = false
			}
			return m, nil
		}
		switch key.String() {
		case keyQuit:
			return m, tea.Quit
		case keyHelp:
			m.helpOpen = true
			return m, nil
		}
	}

	if m.view == ViewStation && m.station != nil {
		updated, cmd := m.station.Update(msg)
		m.station = &updated
		return m, cmd
	}
	var cmd tea.Cmd
	m.fleet, cmd = m.fleet.Update(msg)
	return m, cmd
}

func (m *App) handleClientEvent(event client.Event) []tea.Cmd {
	cmds := make([]tea.Cmd, 0, 3)
	switch event.Type {
	case client.EventDisconnected:
		m.connState = "reconnecting"
		return cmds
	case client.EventReconnected:
		m.connState = ""
		cmds = append(cmds, m.addToast(toastInfo, "event stream reconnected"))
		return cmds
	case "fleet_tick":
		ticks, err := client.DecodeFleetTick(event.Raw)
		if err != nil {
			return append(cmds, m.addToast(toastError, "invalid fleet update: "+err.Error()))
		}
		var cmd tea.Cmd
		m.fleet, cmd = m.fleet.Update(fleet.FleetTickMsg(ticks))
		return append(cmds, cmd)
	case "tick":
		tick, err := client.DecodeTick(event.Raw)
		if err != nil {
			return append(cmds, m.addToast(toastError, "invalid station update: "+err.Error()))
		}
		var cmd tea.Cmd
		m.fleet, cmd = m.fleet.Update(fleet.TickMsg{event.StationID: tick})
		return append(cmds, cmd)
	}

	var cmd tea.Cmd
	m.fleet, cmd = m.fleet.Update(fleet.EventMsg(event))
	cmds = append(cmds, cmd)
	if strings.HasPrefix(event.Type, "station_operation_") {
		kind, message := operationToast(event)
		cmds = append(cmds, m.addToast(kind, message))
	}
	return cmds
}

func operationToast(event client.Event) (toastKind, string) {
	var operation api.Operation
	_ = json.Unmarshal(event.Raw, &operation)
	operationID := operation.ID
	if operationID == "" {
		operationID = event.OperationID
	}
	description := operation.Type
	if description == "" {
		description = "operation"
	}
	if operationID != "" {
		description += " (op " + operationID + ")"
	}
	switch event.Type {
	case "station_operation_failed":
		if operation.Error != "" {
			return toastError, description + " failed: " + operation.Error
		}
		return toastError, description + " failed"
	case "station_operation_completed":
		return toastSuccess, description + " completed"
	default:
		return toastInfo, description + " started"
	}
}

func (m *App) addToast(kind toastKind, message string) tea.Cmd {
	if message == "" {
		return nil
	}
	m.nextID++
	m.toasts = append(m.toasts, toast{id: m.nextID, kind: kind, message: message})
	if len(m.toasts) > 3 {
		m.toasts = m.toasts[len(m.toasts)-3:]
	}
	m.resizeContent()
	return expireToast(m.nextID)
}

func (m *App) removeToast(id int) {
	for i := range m.toasts {
		if m.toasts[i].id == id {
			m.toasts = append(m.toasts[:i], m.toasts[i+1:]...)
			m.resizeContent()
			return
		}
	}
}

func (m *App) resize(width, height int) {
	m.width = max(0, width)
	m.height = max(0, height)
	m.resizeContent()
}

func (m *App) resizeContent() {
	m.fleet.SetSize(m.width, m.contentHeight())
	if m.station != nil {
		m.station.SetSize(m.width, m.contentHeight())
	}
	if m.modal != nil {
		m.modal.SetSize(m.width, m.height)
	}
}

func (m App) contentHeight() int {
	return max(0, m.height-3-len(m.toasts))
}

func (m App) View() string {
	header := m.theme.header.Render("ChargeGhost") + "  " + m.theme.breadcrumb.Render(m.fleet.Title())
	if m.view == ViewStation && m.station != nil {
		header = m.theme.header.Render("ChargeGhost") + "  " + m.theme.breadcrumb.Render("Fleet / "+m.station.StationID())
	}
	if m.addr != "" {
		header += m.theme.help.Render("  http://" + m.addr)
	}
	if m.connState != "" {
		header += "  " + m.theme.banner.Render("event stream "+m.connState)
	}

	body := m.fleet.View()
	help := m.fleet.Help()
	if m.view == ViewStation && m.station != nil {
		body = m.station.View()
		help = m.station.Help()
	}
	toastLines := make([]string, 0, len(m.toasts))
	for _, item := range m.toasts {
		style := m.theme.toastInfo
		if item.kind == toastSuccess {
			style = m.theme.toastOK
		} else if item.kind == toastError {
			style = m.theme.toastError
		}
		toastLines = append(toastLines, style.Render(item.message))
	}
	footer := helpBar(m.theme, help, false)
	view := strings.Join([]string{header, body, strings.Join(toastLines, "\n"), footer}, "\n")

	if m.helpOpen {
		return lipgloss.Place(max(1, m.width), max(1, m.height), lipgloss.Center, lipgloss.Center, helpBar(m.theme, help, true))
	}
	if m.modal != nil {
		return lipgloss.Place(max(1, m.width), max(1, m.height), lipgloss.Center, lipgloss.Center, m.theme.modal.Render(m.modal.View()))
	}
	if m.width == 0 || m.height == 0 {
		return view
	}
	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(view)
}
