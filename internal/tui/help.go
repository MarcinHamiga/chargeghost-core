package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func helpBar(styles themeStyles, entries []string, expanded bool) string {
	base := append([]string(nil), entries...)
	base = append(base, "? help", "q quit")
	if !expanded {
		return styles.help.Render(strings.Join(base, "  "))
	}
	lines := []string{
		styles.header.Render("Keyboard help"),
		"",
		strings.Join(base, "\n"),
		"",
		styles.help.Render("Press ? or esc to close"),
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Render(strings.Join(lines, "\n"))
}
