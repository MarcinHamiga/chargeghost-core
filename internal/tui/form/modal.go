package form

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Modal is the root application's modal contract.
type Modal interface {
	Init() tea.Cmd
	Update(tea.Msg) (Modal, tea.Cmd)
	View() string
	Dirty() bool
	SetSize(width, height int)
}

// OpenMsg asks the root application to display a modal.
type OpenMsg struct{ Modal Modal }

// ResultMsg is emitted when a form or confirmation closes.
type ResultMsg struct {
	Action string
	Values map[string]string
	OK     bool
}

// Open returns a command that asks the root application to display modal.
func Open(modal Modal) tea.Cmd {
	return func() tea.Msg { return OpenMsg{Modal: modal} }
}

// Form is a reusable, ordered modal form.
type Form struct {
	title  string
	action string
	fields []Field
	inputs []textinput.Model
	focus  int
	err    string
	width  int
	dirty  bool
}

func New(title, action string, fields []Field) *Form {
	f := &Form{title: title, action: action, fields: append([]Field(nil), fields...), width: 64}
	f.inputs = make([]textinput.Model, len(fields))
	for i, field := range fields {
		input := textinput.New()
		input.SetValue(field.Value)
		input.Prompt = ""
		input.CharLimit = 512
		input.Width = 42
		f.inputs[i] = input
	}
	f.focus = f.firstEditable()
	f.syncFocus()
	return f
}

func (f *Form) Init() tea.Cmd {
	if f.focus < 0 {
		return nil
	}
	return textinput.Blink
}

func (f *Form) Dirty() bool { return f.dirty }

func (f *Form) SetSize(width, _ int) {
	f.width = max(24, min(width-8, 72))
	for i := range f.inputs {
		f.inputs[i].Width = max(8, f.width-22)
	}
}

func (f *Form) Update(msg tea.Msg) (Modal, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return f, nil
	}
	if key.Type == tea.KeyEsc {
		return f, func() tea.Msg { return ResultMsg{Action: f.action, OK: false} }
	}
	if key.Type == tea.KeyTab || key.Type == tea.KeyShiftTab || key.Type == tea.KeyUp || key.Type == tea.KeyDown {
		direction := 1
		if key.Type == tea.KeyShiftTab || key.Type == tea.KeyUp {
			direction = -1
		}
		f.move(direction)
		return f, nil
	}
	if key.Type == tea.KeyEnter {
		values, err := f.values()
		if err != nil {
			f.err = err.Error()
			return f, nil
		}
		return f, func() tea.Msg { return ResultMsg{Action: f.action, Values: values, OK: true} }
	}
	if f.focus < 0 {
		return f, nil
	}
	field := &f.fields[f.focus]
	switch field.Kind {
	case Toggle:
		if key.String() == " " || key.Type == tea.KeyLeft || key.Type == tea.KeyRight {
			field.Value = strconvBool(field.Value != "true")
			f.dirty = true
			f.err = ""
		}
	case Select:
		if key.String() == " " || key.Type == tea.KeyLeft || key.Type == tea.KeyRight {
			direction := 1
			if key.Type == tea.KeyLeft {
				direction = -1
			}
			field.Value = nextOption(field.Options, field.Value, direction)
			f.inputs[f.focus].SetValue(field.Value)
			f.dirty = true
			f.err = ""
		}
	case Text, Number:
		before := f.inputs[f.focus].Value()
		var cmd tea.Cmd
		f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
		field.Value = f.inputs[f.focus].Value()
		if field.Value != before {
			f.dirty = true
			f.err = ""
		}
		return f, cmd
	}
	return f, nil
}

func (f *Form) View() string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render(f.title))
	b.WriteString("\n\n")
	for i, field := range f.fields {
		cursor := "  "
		if i == f.focus {
			cursor = "> "
		}
		value := field.Value
		switch field.Kind {
		case Text, Number:
			value = f.inputs[i].View()
		case Select:
			value = "< " + value + " >"
		case Toggle:
			if value == "true" {
				value = "[x] enabled"
			} else {
				value = "[ ] disabled"
			}
		case ReadOnly:
			value = lipgloss.NewStyle().Faint(true).Render(value)
		}
		b.WriteString(fmt.Sprintf("%s%-18s %s\n", cursor, field.Label, value))
	}
	if f.err != "" {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(f.err))
	}
	b.WriteString("\n" + lipgloss.NewStyle().Faint(true).Render("tab navigate  enter submit  esc cancel"))
	return lipgloss.NewStyle().Width(f.width).Border(lipgloss.RoundedBorder()).Padding(1, 2).Render(b.String())
}

func (f *Form) values() (map[string]string, error) {
	values := make(map[string]string, len(f.fields))
	for i := range f.fields {
		value := f.fields[i].Value
		if f.fields[i].Kind == Text || f.fields[i].Kind == Number {
			value = f.inputs[i].Value()
		}
		if err := f.fields[i].validate(value); err != nil {
			f.focus = i
			f.syncFocus()
			return nil, err
		}
		values[f.fields[i].Name] = strings.TrimSpace(value)
	}
	return values, nil
}

func (f *Form) firstEditable() int {
	for i := range f.fields {
		if f.fields[i].Kind != ReadOnly {
			return i
		}
	}
	return -1
}

func (f *Form) move(direction int) {
	if f.focus < 0 || len(f.fields) == 0 {
		return
	}
	for range f.fields {
		f.focus = (f.focus + direction + len(f.fields)) % len(f.fields)
		if f.fields[f.focus].Kind != ReadOnly {
			break
		}
	}
	f.syncFocus()
}

func (f *Form) syncFocus() {
	for i := range f.inputs {
		if i == f.focus && (f.fields[i].Kind == Text || f.fields[i].Kind == Number) {
			f.inputs[i].Focus()
		} else {
			f.inputs[i].Blur()
		}
	}
}

func nextOption(options []string, current string, direction int) string {
	if len(options) == 0 {
		return current
	}
	index := 0
	for i, option := range options {
		if option == current {
			index = i
			break
		}
	}
	return options[(index+direction+len(options))%len(options)]
}

func strconvBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
