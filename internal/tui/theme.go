package tui

import "github.com/charmbracelet/lipgloss"

type themeStyles struct {
	header     lipgloss.Style
	breadcrumb lipgloss.Style
	banner     lipgloss.Style
	help       lipgloss.Style
	helpKey    lipgloss.Style
	modal      lipgloss.Style
	toastInfo  lipgloss.Style
	toastOK    lipgloss.Style
	toastError lipgloss.Style
}

func newTheme() themeStyles {
	return themeStyles{
		header:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("87")),
		breadcrumb: lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		banner:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("166")).Padding(0, 1),
		help:       lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		helpKey:    lipgloss.NewStyle().Foreground(lipgloss.Color("87")),
		modal:      lipgloss.NewStyle().Background(lipgloss.Color("235")),
		toastInfo:  lipgloss.NewStyle().Foreground(lipgloss.Color("87")).BorderLeft(true).PaddingLeft(1),
		toastOK:    lipgloss.NewStyle().Foreground(lipgloss.Color("82")).BorderLeft(true).PaddingLeft(1),
		toastError: lipgloss.NewStyle().Foreground(lipgloss.Color("196")).BorderLeft(true).PaddingLeft(1),
	}
}
