package form

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind identifies how a form field is edited and validated.
type Kind int

const (
	Text Kind = iota
	Number
	Select
	Toggle
	ReadOnly
)

// Field describes one ordered form input.
type Field struct {
	Name     string
	Label    string
	Kind     Kind
	Value    string
	Required bool
	Options  []string
	Min      *float64
	Max      *float64
	Validate func(string) error
}

func (f Field) validate(value string) error {
	value = strings.TrimSpace(value)
	if f.Required && value == "" {
		return fmt.Errorf("%s is required", f.Label)
	}
	if value == "" {
		return nil
	}
	if f.Kind == Number {
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("%s must be a number", f.Label)
		}
		if f.Min != nil && n < *f.Min {
			return fmt.Errorf("%s must be at least %g", f.Label, *f.Min)
		}
		if f.Max != nil && n > *f.Max {
			return fmt.Errorf("%s must be at most %g", f.Label, *f.Max)
		}
	}
	if f.Kind == Select {
		for _, option := range f.Options {
			if value == option {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of %s", f.Label, strings.Join(f.Options, ", "))
	}
	if f.Validate != nil {
		return f.Validate(value)
	}
	return nil
}

func float(v float64) *float64 { return &v }

// Range returns pointers suitable for a number field's Min and Max values.
func Range(minimum, maximum float64) (*float64, *float64) {
	return float(minimum), float(maximum)
}
