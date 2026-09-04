package form

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormNavigationValidationAndSubmit(t *testing.T) {
	minimum, maximum := Range(1, 3)
	modal := New("Test", "submit", []Field{
		{Name: "name", Label: "Name", Kind: Text, Required: true},
		{Name: "info", Label: "Info", Kind: ReadOnly, Value: "fixed"},
		{Name: "count", Label: "Count", Kind: Number, Value: "2", Min: minimum, Max: maximum},
		{Name: "mode", Label: "Mode", Kind: Select, Value: "a", Options: []string{"a", "b"}},
		{Name: "enabled", Label: "Enabled", Kind: Toggle, Value: "false"},
	})

	updated, cmd := modal.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Nil(t, cmd)
	modal = updated.(*Form)
	assert.Equal(t, 0, modal.focus)
	assert.Contains(t, modal.err, "required")

	updated, _ = modal.Update(runes("station"))
	modal = updated.(*Form)
	assert.True(t, modal.Dirty())
	updated, _ = modal.Update(tea.KeyMsg{Type: tea.KeyTab})
	modal = updated.(*Form)
	assert.Equal(t, 2, modal.focus, "read-only field must be skipped")
	updated, _ = modal.Update(tea.KeyMsg{Type: tea.KeyTab})
	modal = updated.(*Form)
	updated, _ = modal.Update(tea.KeyMsg{Type: tea.KeyRight})
	modal = updated.(*Form)
	updated, _ = modal.Update(tea.KeyMsg{Type: tea.KeyTab})
	modal = updated.(*Form)
	updated, _ = modal.Update(runes(" "))
	modal = updated.(*Form)

	updated, cmd = modal.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	result := cmd().(ResultMsg)
	assert.True(t, result.OK)
	assert.Equal(t, "submit", result.Action)
	assert.Equal(t, "station", result.Values["name"])
	assert.Equal(t, "b", result.Values["mode"])
	assert.Equal(t, "true", result.Values["enabled"])
}

func TestFormNumberValidationAndCancel(t *testing.T) {
	minimum, maximum := Range(1, 5)
	modal := New("Test", "save", []Field{{Name: "count", Label: "Count", Kind: Number, Value: "9", Min: minimum, Max: maximum}})

	updated, cmd := modal.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Nil(t, cmd)
	modal = updated.(*Form)
	assert.Contains(t, modal.err, "at most")

	_, cmd = modal.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.NotNil(t, cmd)
	result := cmd().(ResultMsg)
	assert.False(t, result.OK)
	assert.Equal(t, "save", result.Action)
}

func runes(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}
