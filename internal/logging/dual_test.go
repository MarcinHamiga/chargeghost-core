package logging

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestHandler(t *testing.T, capacity int) (*DualHandler, string) {
	t.Helper()
	dir := t.TempDir()
	h, err := NewDualHandler(dir, "tui.log", capacity)
	require.NoError(t, err)
	return h, filepath.Join(dir, "tui.log")
}

func TestDualHandlerFansOutToFileAndRing(t *testing.T) {
	h, logFile := newTestHandler(t, 100)
	slog.SetDefault(slog.New(h))

	slog.Info("hello world", "station", "CP_1", "count", 3)

	// Ring holds the formatted line.
	snap := h.Ring().Snapshot()
	require.Len(t, snap, 1)
	require.Contains(t, snap[0], "INFO hello world")
	require.Contains(t, snap[0], "station=CP_1")
	require.Contains(t, snap[0], "count=3")
	require.Regexp(t, `^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`, snap[0])

	// File holds a JSON entry.
	data, err := os.ReadFile(logFile)
	require.NoError(t, err)
	var entry map[string]any
	require.NoError(t, json.Unmarshal(data, &entry))
	require.Equal(t, "INFO", entry["level"])
	require.Equal(t, "hello world", entry["msg"])
	require.Equal(t, "CP_1", entry["station"])
	require.InDelta(t, float64(3), entry["count"], 0.001)
}

func TestDualHandlerLevelSwitch(t *testing.T) {
	h, _ := newTestHandler(t, 100)
	h.level.Set(slog.LevelWarn)

	logger := slog.New(h)
	logger.Info("dropped")
	require.Empty(t, h.Ring().Snapshot())

	logger.Warn("kept")
	require.Len(t, h.Ring().Snapshot(), 1)
}

func TestRingEvictionAtCapacity(t *testing.T) {
	r := NewRing(3)
	for i := 0; i < 5; i++ {
		r.Add("line" + string(rune('0'+i)))
	}
	snap := r.Snapshot()
	require.Equal(t, []string{"line2", "line3", "line4"}, snap)
}

func TestRingSubscribeDeliversNewLines(t *testing.T) {
	r := NewRing(10)
	ch := make(chan string, 4)
	r.Subscribe(ch)
	defer r.Unsubscribe(ch)

	r.Add("a")
	r.Add("b")

	deadline := time.After(2 * time.Second)
	got := make([]string, 0, 2)
	for len(got) < 2 {
		select {
		case line := <-ch:
			got = append(got, line)
		case <-deadline:
			t.Fatal("timed out waiting for subscribed lines")
		}
	}
	require.Equal(t, []string{"a", "b"}, got)
}

func TestRingSlowSubscriberDrops(t *testing.T) {
	r := NewRing(10)
	ch := make(chan string) // unbuffered: nobody receives
	r.Subscribe(ch)
	defer r.Unsubscribe(ch)

	done := make(chan struct{})
	go func() {
		r.Add("one")
		r.Add("two")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Add blocked on a slow subscriber")
	}
}

func TestDualHandlerConcurrentHandleRaceFree(t *testing.T) {
	h, logFile := newTestHandler(t, 500)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			logger := slog.New(h.WithAttrs([]slog.Attr{slog.Int("worker", n)}))
			for j := 0; j < 50; j++ {
				logger.Info("concurrent", "j", j)
				time.Sleep(time.Microsecond)
			}
		}(i)
	}
	wg.Wait()

	require.Len(t, h.Ring().Snapshot(), 400)
	data, err := os.ReadFile(logFile)
	require.NoError(t, err)
	require.Greater(t, len(data), 0)
}

func TestDualHandlerWithGroupQualifiesRingKeys(t *testing.T) {
	h, _ := newTestHandler(t, 10)
	logger := slog.New(h.WithGroup("http")).With("method", "GET")
	logger.Info("request", "path", "/health")

	snap := h.Ring().Snapshot()
	require.Len(t, snap, 1)
	require.Contains(t, snap[0], "http.method=GET")
	require.Contains(t, snap[0], "path=/health")
}
