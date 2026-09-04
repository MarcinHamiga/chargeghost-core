package station

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStationNavigationAndBack(t *testing.T) {
	model := New("station-1")
	model.SetSize(0, 0)
	assert.Contains(t, model.View(), "coming in phase 3")

	model, _ = model.Update(stationKey("]"))
	assert.Equal(t, 1, model.ActiveTab())
	model, _ = model.Update(stationKey("["))
	assert.Equal(t, 0, model.ActiveTab())
	model, _ = model.Update(stationKey("4"))
	assert.Equal(t, 3, model.ActiveTab())
	assert.Contains(t, model.View(), "coming in phase 4")
	model, _ = model.Update(stationKey("0"))
	assert.Equal(t, 9, model.ActiveTab())
	assert.Contains(t, model.View(), "coming in phase 6")

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.NotNil(t, cmd)
	require.IsType(t, BackMsg{}, cmd())
}

func stationKey(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}
