package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

func TestPlaceholderApp_QuitOnQ(t *testing.T) {
	m := newPlaceholderApp(nil, nil, "127.0.0.1:0")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	require.NotNil(t, cmd)
	require.IsType(t, placeholderApp{}, updated)

	msg := cmd()
	require.IsType(t, tea.QuitMsg{}, msg)
}

func TestPlaceholderApp_QuitOnCtrlC(t *testing.T) {
	m := newPlaceholderApp(nil, nil, "127.0.0.1:0")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	require.NotNil(t, cmd)
	require.IsType(t, tea.QuitMsg{}, cmd())
}

func TestPlaceholderApp_IgnoresOtherKeys(t *testing.T) {
	m := newPlaceholderApp(nil, nil, "127.0.0.1:0")

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'x'}},
		{Type: tea.KeyEnter},
		{Type: tea.KeyEsc},
	} {
		_, cmd := m.Update(key)
		require.Nil(t, cmd, "key %q should not trigger a command", key.String())
	}
}
