package main

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyLogLevel(t *testing.T) {
	for _, test := range []struct {
		mode string
		want slog.Level
	}{
		{mode: "debug", want: slog.LevelDebug},
		{mode: "warn", want: slog.LevelWarn},
		{mode: "error", want: slog.LevelError},
		{mode: "shallow", want: slog.LevelInfo},
		{mode: "", want: slog.LevelInfo},
	} {
		t.Run(test.mode, func(t *testing.T) {
			var level slog.LevelVar
			applyLogLevel(&level, test.mode)
			assert.Equal(t, test.want, level.Level())
		})
	}
}
