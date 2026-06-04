package ocpp

import "testing"

func TestRedactIDTag(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "***"},
		{"ab", "***"},
		{"abcd", "***"},
		{"abcde", "ab***de"},
		{"12345678", "12***78"},
		{"DEADBEEFCAFE", "DE***FE"},
	}
	for _, c := range cases {
		got := RedactIDTag(c.in)
		if got != c.want {
			t.Errorf("RedactIDTag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
