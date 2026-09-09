package form

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Confirm is an ordinary or type-to-confirm modal.
type Confirm struct {
	title   string
	body    string
	action  string
	typed   string
	input   textinput.Model
	err     string
	width   int
	dirty   bool
	confirm string
	cancel  string
}

func NewConfirm(title, body, action string) *Confirm {
	return newConfirm(title, body, action, "")
}

func NewTypedConfirm(title, body, action, expected string) *Confirm {
	return newConfirm(title, body, action, expected)
}

func newConfirm(title, body, action, expected string) *Confirm {
	input := textinput.New()
	input.Prompt = "> "
	input.Width = 42
	if expected != "" {
		input.Focus()
	}
	return &Confirm{
		title: title, body: body, action: action, typed: expected, input: input,
		width: 60, confirm: "confirm", cancel: "cancel",
	}
}

func (c *Confirm) Init() tea.Cmd {
	if c.typed != "" {
		return textinput.Blink
	}
	return nil
}

func (c *Confirm) Dirty() bool { return c.dirty }

func (c *Confirm) SetSize(width, _ int) {
	c.width = max(24, min(width-8, 68))
	c.input.Width = max(8, c.width-8)
}

func (c *Confirm) Update(msg tea.Msg) (Modal, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return c, nil
	}
	if key.Type == tea.KeyEsc || (c.typed == "" && strings.EqualFold(key.String(), "n")) {
		return c, c.result(false)
	}
	if c.typed == "" {
		if key.Type == tea.KeyEnter || strings.EqualFold(key.String(), "y") {
			return c, c.result(true)
		}
		return c, nil
	}
	if key.Type == tea.KeyEnter {
		if c.input.Value() != c.typed {
			c.err = "value does not match"
			return c, nil
		}
		return c, c.result(true)
	}
	before := c.input.Value()
	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg)
	if c.input.Value() != before {
		c.dirty = true
		c.err = ""
	}
	return c, cmd
}

func (c *Confirm) View() string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render(c.title))
	b.WriteString("\n\n" + c.body)
	if c.typed != "" {
		b.WriteString(fmt.Sprintf("\n\nType %q to confirm:\n%s", c.typed, c.input.View()))
	} else {
		b.WriteString(fmt.Sprintf("\n\n[y/enter] %s  [n/esc] %s", c.confirm, c.cancel))
	}
	if c.err != "" {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(c.err))
	}
	return lipgloss.NewStyle().Width(c.width).Border(lipgloss.RoundedBorder()).Padding(1, 2).Render(b.String())
}

func (c *Confirm) result(ok bool) tea.Cmd {
	return func() tea.Msg { return ResultMsg{Action: c.action, OK: ok} }
}
