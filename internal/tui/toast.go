package tui

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chargeghost/engine/internal/client"
)

type toastKind int

const (
	toastInfo toastKind = iota
	toastSuccess
	toastError
)

type toast struct {
	id      int
	kind    toastKind
	message string
}

type toastExpireMsg int

func formatError(err error) string {
	if err == nil {
		return "unknown error"
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("%s (HTTP %d)", apiErr.Message, apiErr.Status)
	}
	return err.Error()
}

func expireToast(id int) tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return toastExpireMsg(id) })
}
