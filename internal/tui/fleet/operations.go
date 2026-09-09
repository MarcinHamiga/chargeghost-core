package fleet

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/chargeghost/engine/internal/api"
)

type operationsLoadedMsg struct {
	operations []api.Operation
	err        error
}

type operationLoadedMsg struct {
	operation api.Operation
	err       error
}

type operationsView struct {
	table      table.Model
	detail     viewport.Model
	operations []api.Operation
	loading    bool
	inDetail   bool
}

func newOperationsView() operationsView {
	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "ID", Width: 24},
			{Title: "Type", Width: 20},
			{Title: "Station", Width: 18},
			{Title: "State", Width: 12},
			{Title: "Age", Width: 10},
		}),
		table.WithFocused(true),
		table.WithHeight(defaultTableHeight),
	)
	return operationsView{table: t, detail: viewport.New(0, 0)}
}

func (v *operationsView) setSize(width, height int) {
	v.table.SetWidth(max(0, width))
	v.table.SetHeight(max(1, height-2))
	v.detail.Width = max(0, width)
	v.detail.Height = max(0, height)
}

func (v *operationsView) refresh(cli FleetClient) tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		operations, err := cli.Operations()
		return operationsLoadedMsg{operations: operations, err: err}
	}
}

func (m Model) updateOperations(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case operationsLoadedMsg:
		m.ops.loading = false
		if msg.err != nil {
			return m, toastCmd(ToastMsg{Kind: toastError, Err: msg.err})
		}
		m.ops.operations = append([]api.Operation(nil), msg.operations...)
		rows := make([]table.Row, 0, len(msg.operations))
		for _, operation := range msg.operations {
			age := "-"
			if !operation.StartedAt.IsZero() {
				age = time.Since(operation.StartedAt).Round(time.Second).String()
			}
			rows = append(rows, table.Row{operation.ID, operation.Type, operation.StationID, operation.State, age})
		}
		m.ops.table.SetRows(rows)
		return m, nil
	case operationLoadedMsg:
		if msg.err != nil {
			return m, toastCmd(ToastMsg{Kind: toastError, Err: msg.err})
		}
		m.ops.inDetail = true
		m.ops.detail.SetContent(formatOperation(msg.operation))
		m.ops.detail.GotoTop()
		return m, nil
	case tea.KeyMsg:
		if m.ops.inDetail {
			if msg.String() == "esc" {
				m.ops.inDetail = false
				return m, nil
			}
			var cmd tea.Cmd
			m.ops.detail, cmd = m.ops.detail.Update(msg)
			return m, cmd
		}
		switch msg.String() {
		case "esc":
			m.screen = Dashboard
			return m, nil
		case "r":
			return m, m.ops.refresh(m.cli)
		case "enter":
			index := m.ops.table.Cursor()
			if index >= 0 && index < len(m.ops.operations) {
				id := m.ops.operations[index].ID
				return m, func() tea.Msg {
					operation, err := m.cli.Operation(id)
					return operationLoadedMsg{operation: operation, err: err}
				}
			}
		}
	}
	var cmd tea.Cmd
	m.ops.table, cmd = m.ops.table.Update(msg)
	return m, cmd
}

func (v operationsView) view() string {
	if v.inDetail {
		return v.detail.View()
	}
	if v.loading && len(v.operations) == 0 {
		return "Loading operations..."
	}
	if len(v.operations) == 0 {
		return "No operations recorded. Press r to refresh."
	}
	return v.table.View()
}

func formatOperation(operation api.Operation) string {
	ended := "-"
	if operation.EndedAt != nil {
		ended = operation.EndedAt.Format(time.RFC3339)
	}
	started := "-"
	if !operation.StartedAt.IsZero() {
		started = operation.StartedAt.Format(time.RFC3339)
	}
	fields := []string{
		"ID:         " + operation.ID,
		"Type:       " + operation.Type,
		"Station:    " + operation.StationID,
		"State:      " + operation.State,
		"Started at: " + started,
		"Ended at:   " + ended,
		"Error:      " + operation.Error,
	}
	return strings.Join(fields, "\n") + "\n\n[esc] returns to operations"
}
