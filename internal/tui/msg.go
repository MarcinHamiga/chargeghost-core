package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/chargeghost/engine/internal/client"
)

type clientEventMsg client.Event
type eventStreamClosedMsg struct{}

func waitForEvent(events <-chan client.Event) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return eventStreamClosedMsg{}
		}
		return clientEventMsg(event)
	}
}
