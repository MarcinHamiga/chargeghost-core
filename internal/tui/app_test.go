package tui

import (
	"encoding/json"
	"net/http"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chargeghost/engine/internal/api"
	"github.com/chargeghost/engine/internal/client"
	"github.com/chargeghost/engine/internal/tui/fleet"
	"github.com/chargeghost/engine/internal/tui/form"
	"github.com/chargeghost/engine/internal/tui/station"
)

func TestAppRoutesStationNavigation(t *testing.T) {
	app := newApp(nil, nil, nil, "127.0.0.1:1234")
	updated, _ := app.Update(fleet.OpenStationMsg{StationID: "station-1"})
	app = updated.(App)
	assert.Equal(t, ViewStation, app.view)
	require.NotNil(t, app.station)
	assert.Equal(t, "station-1", app.station.StationID())

	updated, _ = app.Update(station.BackMsg{})
	app = updated.(App)
	assert.Equal(t, ViewFleet, app.view)
	assert.Nil(t, app.station)
}

func TestAppModalRoutesKeysBeforeGlobals(t *testing.T) {
	app := newApp(nil, nil, nil, "")
	modal := form.New("Edit", "edit", []form.Field{{Name: "name", Label: "Name", Kind: form.Text}})
	updated, _ := app.Update(form.OpenMsg{Modal: modal})
	app = updated.(App)

	updated, cmd := app.Update(appKey("x"))
	app = updated.(App)
	assert.NotNil(t, app.modal)
	if cmd != nil {
		_, quit := cmd().(tea.QuitMsg)
		assert.False(t, quit)
	}
	assert.True(t, app.modal.Dirty())

	updated, cmd = app.Update(appKey("q"))
	app = updated.(App)
	assert.Contains(t, app.modal.View(), "Discard unsaved")
	assert.NotNil(t, app.suspendedModal)

	updated, cmd = app.Update(appKey("n"))
	app = updated.(App)
	require.NotNil(t, cmd)
	result := cmd().(form.ResultMsg)
	assert.False(t, result.OK)
	updated, _ = app.Update(result)
	app = updated.(App)
	assert.Same(t, modal, app.modal)
	assert.Nil(t, app.suspendedModal)

	updated, _ = app.Update(appKey("q"))
	app = updated.(App)
	updated, cmd = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = updated.(App)
	require.NotNil(t, cmd)
	updated, quit := app.Update(cmd().(form.ResultMsg))
	app = updated.(App)
	require.NotNil(t, quit)
	require.IsType(t, tea.QuitMsg{}, quit())
	assert.Nil(t, app.modal)
}

func TestAppEventPumpReconnectAndClosedChannel(t *testing.T) {
	events := make(chan client.Event, 1)
	events <- client.Event{Type: client.EventDisconnected}
	cmd := waitForEvent(events)
	require.NotNil(t, cmd)
	message := cmd().(clientEventMsg)

	app := newApp(nil, events, nil, "")
	updated, next := app.Update(message)
	app = updated.(App)
	assert.Equal(t, "reconnecting", app.connState)
	require.NotNil(t, next, "each event must reissue the pump")

	updated, _ = app.Update(clientEventMsg(client.Event{Type: client.EventReconnected}))
	app = updated.(App)
	assert.Empty(t, app.connState)
	assert.Len(t, app.toasts, 1)

	close(events)
	closed := waitForEvent(events)()
	require.IsType(t, eventStreamClosedMsg{}, closed)
	updated, next = app.Update(closed)
	app = updated.(App)
	assert.Nil(t, next, "closed streams must not spin")
	assert.Nil(t, app.events)
	assert.Equal(t, "disconnected", app.connState)
}

func TestAppOperationEventAndToastBound(t *testing.T) {
	app := newApp(nil, nil, nil, "")
	raw, err := json.Marshal(api.Operation{ID: "op-1", Type: "station.start", State: "succeeded"})
	require.NoError(t, err)
	updated, _ := app.Update(clientEventMsg(client.Event{Type: "station_operation_completed", OperationID: "op-1", Raw: raw}))
	app = updated.(App)
	require.Len(t, app.toasts, 1)
	assert.Contains(t, app.toasts[0].message, "completed")

	for i := 0; i < 4; i++ {
		updated, _ = app.Update(fleet.ToastMsg{Kind: "info", Message: string(rune('a' + i))})
		app = updated.(App)
	}
	require.Len(t, app.toasts, 3)
	assert.Equal(t, "b", app.toasts[0].message)
	assert.Equal(t, "d", app.toasts[2].message)

	firstID := app.toasts[0].id
	updated, _ = app.Update(toastExpireMsg(firstID))
	app = updated.(App)
	assert.Len(t, app.toasts, 2)
}

func TestAPIErrorToastFormattingAndResize(t *testing.T) {
	err := &client.APIError{Status: http.StatusConflict, Message: "already stopping"}
	assert.Equal(t, "already stopping (HTTP 409)", formatError(err))

	app := newApp(nil, nil, nil, "")
	updated, _ := app.Update(tea.WindowSizeMsg{Width: -1, Height: -1})
	app = updated.(App)
	assert.Zero(t, app.width)
	assert.Zero(t, app.height)
	assert.NotPanics(t, func() { _ = app.View() })
}

func appKey(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}
