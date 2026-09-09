package form

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfirmSubmitAndCancel(t *testing.T) {
	confirm := NewConfirm("Stop", "Stop it?", "stop")
	_, cmd := confirm.Update(runes("y"))
	require.NotNil(t, cmd)
	assert.True(t, cmd().(ResultMsg).OK)

	confirm = NewConfirm("Stop", "Stop it?", "stop")
	_, cmd = confirm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.NotNil(t, cmd)
	assert.False(t, cmd().(ResultMsg).OK)
}

func TestTypedConfirmRequiresExactValue(t *testing.T) {
	confirm := NewTypedConfirm("Delete", "Danger", "delete", "station-1")
	updated, _ := confirm.Update(runes("wrong"))
	confirm = updated.(*Confirm)
	updated, cmd := confirm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Nil(t, cmd)
	confirm = updated.(*Confirm)
	assert.Contains(t, confirm.err, "does not match")

	confirm.input.SetValue("")
	updated, _ = confirm.Update(runes("station-1"))
	confirm = updated.(*Confirm)
	_, cmd = confirm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	result := cmd().(ResultMsg)
	assert.True(t, result.OK)
	assert.Equal(t, "delete", result.Action)
}
