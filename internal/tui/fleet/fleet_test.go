package fleet

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chargeghost/engine/internal/api"
	"github.com/chargeghost/engine/internal/client"
	"github.com/chargeghost/engine/internal/config"
	"github.com/chargeghost/engine/internal/tui/form"
)

type fakeClient struct {
	status          api.FleetStatusResponse
	config          api.FleetConfigResponse
	operations      []api.Operation
	operation       api.Operation
	statusCalls     int
	operationsCalls int
	startID         string
	deleteID        string
	deleteOptions   client.DeleteStationOptions
	created         api.CreateStationRequest
	saveCalls       int
}

func (f *fakeClient) FleetStatus() (api.FleetStatusResponse, error) {
	f.statusCalls++
	return f.status, nil
}
func (f *fakeClient) FleetConfig() (api.FleetConfigResponse, error) { return f.config, nil }
func (f *fakeClient) SaveFleetConfig() (api.Response, error) {
	f.saveCalls++
	return api.Response{Success: true, Message: "fleet config saved"}, nil
}
func (f *fakeClient) Operations() ([]api.Operation, error) {
	f.operationsCalls++
	return f.operations, nil
}
func (f *fakeClient) Operation(string) (api.Operation, error) { return f.operation, nil }
func (f *fakeClient) CreateStation(req api.CreateStationRequest) (api.OperationResponse, error) {
	f.created = req
	return api.OperationResponse{Success: true, Message: "station created", OperationID: "op-create"}, nil
}
func (f *fakeClient) StartStation(id string) (api.OperationResponse, error) {
	f.startID = id
	return api.OperationResponse{Success: true, Message: "start requested", OperationID: "op-start"}, nil
}
func (f *fakeClient) StopStation(string) (api.OperationResponse, error) {
	return api.OperationResponse{Success: true}, nil
}
func (f *fakeClient) RestartStation(string) (api.OperationResponse, error) {
	return api.OperationResponse{Success: true}, nil
}
func (f *fakeClient) EnableStation(string) (api.OperationResponse, error) {
	return api.OperationResponse{Success: true}, nil
}
func (f *fakeClient) DisableStation(string) (api.OperationResponse, error) {
	return api.OperationResponse{Success: true}, nil
}
func (f *fakeClient) ReloadStation(string) (api.Response, error) {
	return api.Response{Success: true}, nil
}
func (f *fakeClient) PersistStation(string) (api.Response, error) {
	return api.Response{Success: true}, nil
}
func (f *fakeClient) DeleteStation(id string, options client.DeleteStationOptions) (api.Response, error) {
	f.deleteID = id
	f.deleteOptions = options
	return api.Response{Success: true, Message: "station deleted"}, nil
}

func TestFleetMergeDefaultMarkerAndSelection(t *testing.T) {
	fake := &fakeClient{}
	model := New(fake)
	model.applyStatus(api.FleetStatusResponse{
		DefaultStationID: "b",
		Stations: []api.StationSnapshot{
			{StationID: "b", LifecycleState: "running", ConnectorCount: 1},
			{StationID: "a", LifecycleState: "configured", ConnectorCount: 2},
		},
	})
	require.Len(t, model.table.Rows(), 2)
	assert.Equal(t, "a", model.stations[0].StationID)
	assert.Contains(t, model.table.Rows()[1][0], "* b")

	model.table.SetCursor(1)
	model.applyStatus(api.FleetStatusResponse{
		DefaultStationID: "b",
		Stations:         []api.StationSnapshot{{StationID: "c"}, {StationID: "b"}, {StationID: "a"}},
	})
	assert.Equal(t, "b", model.SelectedStationID())

	before := []string{model.stations[0].StationID, model.stations[1].StationID, model.stations[2].StationID}
	model, _ = model.Update(TickMsg{"b": {
		OCPPConnected:  true,
		Connectors:     []client.TickConnector{{ID: 1, Status: "Charging"}},
		ActiveSessions: []client.TickSession{{TransactionID: 1}},
		UptimeSeconds:  65,
	}})
	after := []string{model.stations[0].StationID, model.stations[1].StationID, model.stations[2].StationID}
	assert.Equal(t, before, after, "ticks must not reorder inventory")
	assert.Contains(t, model.table.Rows()[1][2], "connected")
	assert.Equal(t, "1 (1 charging)", model.table.Rows()[1][3])
	assert.Equal(t, "1", model.table.Rows()[1][4])

	model, _ = model.Update(FleetTickMsg{})
	assert.Contains(t, model.table.Rows()[1][2], "offline")
	assert.Equal(t, "0", model.table.Rows()[1][3], "a full fleet tick must remove stale live snapshots")
}

func TestFleetMutationAndTypedDeleteOptions(t *testing.T) {
	fake := &fakeClient{}
	model := New(fake)
	model.applyStatus(api.FleetStatusResponse{Stations: []api.StationSnapshot{{StationID: "station-1"}}})

	model, cmd := model.Update(key("s"))
	require.IsType(t, form.OpenMsg{}, cmd())
	model, cmd = model.Update(form.ResultMsg{Action: actionStart, OK: true})
	result := cmd()
	assert.Equal(t, "station-1", fake.startID)
	model, _ = model.Update(result)

	model, cmd = model.Update(key("D"))
	open := cmd().(form.OpenMsg)
	assert.Contains(t, open.Modal.View(), "station-1")
	model, cmd = model.Update(form.ResultMsg{Action: actionDelete, OK: true})
	_ = cmd()
	assert.Equal(t, "station-1", fake.deleteID)
	assert.Equal(t, client.DeleteStationOptions{AllowEmpty: true}, fake.deleteOptions)
}

func TestFleetCreationBuildsValidatedConnectors(t *testing.T) {
	fake := &fakeClient{}
	model := New(fake)
	values := map[string]string{
		"station_id": "s2", "ocpp_id": "CP_2", "version": "2.0.1", "url": "wss://example.test",
		"connector_count": "2", "voltage": "400", "current": "32", "phase": "3",
		"enabled": "true", "start": "true", "save": "true",
	}
	model, cmd := model.Update(form.ResultMsg{Action: actionCreate, OK: true, Values: values})
	_ = model
	_ = cmd()
	assert.Equal(t, "CP_2", fake.created.OCPPID)
	assert.Equal(t, "2.0.1", fake.created.OCPPVersion)
	require.Len(t, fake.created.Connectors, 2)
	assert.Equal(t, config.ConnectorConfig{Voltage: 400, Current: 32, Phase: 3}, fake.created.Connectors[0])
	assert.True(t, fake.created.Enabled)
	assert.True(t, fake.created.Start)
	assert.True(t, fake.created.Save)

	_, err := stationRequest(map[string]string{"connector_count": "0", "voltage": "230", "current": "32", "phase": "1"})
	assert.Error(t, err)

	modal := newStationForm()
	modal, _ = modal.Update(tea.KeyMsg{Type: tea.KeyTab})
	modal, _ = modal.Update(key("CP_2"))
	for range 3 {
		modal, _ = modal.Update(tea.KeyMsg{Type: tea.KeyTab})
	}
	modal, _ = modal.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	modal, _ = modal.Update(key("2.5"))
	modal, cmd = modal.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Nil(t, cmd)
	assert.Contains(t, modal.View(), "Connector count must be an integer")
}

func TestFleetOperationLifecycleAndConfigEventsRefresh(t *testing.T) {
	fake := &fakeClient{status: api.FleetStatusResponse{Stations: []api.StationSnapshot{{StationID: "s1"}}}}
	model := New(fake)

	for _, eventType := range []string{"station_lifecycle_changed", "station_added", "station_removed", "station_config_changed"} {
		var cmd tea.Cmd
		model, cmd = model.Update(EventMsg(client.Event{Type: eventType}))
		require.NotNil(t, cmd)
		_ = cmd()
	}
	assert.Equal(t, 4, fake.statusCalls)

	model.screen = Operations
	model, cmd := model.Update(EventMsg(client.Event{Type: "station_operation_started"}))
	require.NotNil(t, cmd)
	_ = cmd()
	assert.Equal(t, 1, fake.operationsCalls)

	model, cmd = model.Update(EventMsg(client.Event{Type: "station_operation_completed"}))
	require.NotNil(t, cmd)
	batch := cmd().(tea.BatchMsg)
	require.Len(t, batch, 2)
	for _, nested := range batch {
		_ = nested()
	}
	assert.Equal(t, 5, fake.statusCalls)
	assert.Equal(t, 2, fake.operationsCalls)
}

func TestFleetConfigSaveAndOperationsDetail(t *testing.T) {
	started := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	ended := started.Add(time.Second)
	fake := &fakeClient{
		config:     api.FleetConfigResponse{Config: config.DefaultConfig()},
		operations: []api.Operation{{ID: "op-1", Type: "station.start", StationID: "s1", State: "succeeded", StartedAt: started}},
		operation:  api.Operation{ID: "op-1", Type: "station.start", StationID: "s1", State: "failed", StartedAt: started, EndedAt: &ended, Error: "boom"},
	}
	model := New(fake)
	model.SetSize(100, 30)

	model, cmd := model.Update(key("f"))
	model, _ = model.Update(cmd())
	assert.Equal(t, Config, model.Screen())
	assert.Contains(t, model.View(), "connection_url")
	model, cmd = model.Update(key("s"))
	require.IsType(t, form.OpenMsg{}, cmd())
	model, cmd = model.Update(form.ResultMsg{Action: actionSaveConfig, OK: true})
	_ = cmd()
	assert.Equal(t, 1, fake.saveCalls)

	model.screen = Dashboard
	model, cmd = model.Update(key("O"))
	model, _ = model.Update(cmd())
	assert.Equal(t, Operations, model.Screen())
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model, _ = model.Update(cmd())
	detail := model.View()
	assert.Contains(t, detail, "ID:         op-1")
	assert.Contains(t, detail, "Error:      boom")
	assert.NotContains(t, detail, "Message:")
}

func key(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}
