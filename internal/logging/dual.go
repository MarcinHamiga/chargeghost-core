package logging

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Ring is a bounded, thread-safe buffer of pre-formatted log lines.
// New lines are fanned out to subscribers with a non-blocking send;
// a slow consumer misses lines rather than blocking the caller.
type Ring struct {
	mu    sync.Mutex
	lines []string
	cap   int
	subs  map[chan string]struct{}
}

// NewRing creates a ring holding at most capacity lines.
func NewRing(capacity int) *Ring {
	return &Ring{
		lines: make([]string, 0, capacity),
		cap:   capacity,
		subs:  make(map[chan string]struct{}),
	}
}

// Add appends a line, evicting the oldest when at capacity, and delivers
// it to every subscriber without blocking.
func (r *Ring) Add(line string) {
	r.mu.Lock()
	if len(r.lines) >= r.cap {
		r.lines = r.lines[1:]
	}
	r.lines = append(r.lines, line)
	subs := make([]chan string, 0, len(r.subs))
	for ch := range r.subs {
		subs = append(subs, ch)
	}
	r.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- line:
		default: // drop on slow consumer
		}
	}
}

// Snapshot returns a copy of the buffered lines, oldest first.
func (r *Ring) Snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}

// Subscribe registers a channel receiving new lines. Send is
// non-blocking: a full channel simply misses lines. Unsubscribe when done.
func (r *Ring) Subscribe(ch chan string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subs[ch] = struct{}{}
}

// Unsubscribe removes a previously registered channel.
func (r *Ring) Unsubscribe(ch chan string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, ch)
}

// DualHandler fans every record out to a JSON file handler and a Ring of
// pre-formatted lines, so logs persist to disk while staying renderable
// in-process (the TUI Logs tab).
type DualHandler struct {
	file  slog.Handler
	f     *os.File // underlying log file, exposed via Writer
	ring  *Ring
	level *slog.LevelVar // shared by all WithAttrs/WithGroup clones

	// attrs and groups are immutable per handler instance: WithAttrs and
	// WithGroup build fresh slices, so concurrent Handle reads are safe.
	attrs  []slog.Attr
	groups []string
}

// NewDualHandler creates ~/.chargeghost-style log directory dir, opens
// <dir>/<filename> for append, and returns a handler writing JSON to the
// file and formatted lines into a new ring of the given capacity.
func NewDualHandler(dir, filename string, ringCapacity int) (*DualHandler, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, filename), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	// The TUI owns the terminal, so non-slog writers must be silenced too:
	// chi's request logger (and anything else on the std log package) goes
	// to the same file instead of stderr.
	log.SetOutput(f)
	h := &DualHandler{
		file:  slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}),
		f:     f,
		ring:  NewRing(ringCapacity),
		level: &slog.LevelVar{},
	}
	return h, nil
}

// Writer exposes the underlying log file so other writers (e.g. the HTTP
// access log) can be pointed at the same destination.
func (h *DualHandler) Writer() *os.File { return h.f }

// Ring exposes the in-memory line buffer.
func (h *DualHandler) Ring() *Ring { return h.ring }

// Level exposes the shared level switch both sinks honour.
func (h *DualHandler) Level() *slog.LevelVar { return h.level }

func (h *DualHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *DualHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level < h.level.Level() {
		return nil
	}

	attrs := make([]slog.Attr, 0, len(h.attrs)+r.NumAttrs())
	attrs = append(attrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	groups := h.groups

	line := formatLine(r, groups, attrs)
	h.ring.Add(line)
	return h.file.Handle(context.Background(), r)
}

func (h *DualHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	clone.attrs = append(clone.attrs, h.attrs...)
	clone.attrs = append(clone.attrs, attrs...)
	return &clone
}

func (h *DualHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.groups = make([]string, 0, len(h.groups)+1)
	clone.groups = append(clone.groups, h.groups...)
	clone.groups = append(clone.groups, name)
	return &clone
}

func formatLine(r slog.Record, groups []string, attrs []slog.Attr) string {
	line := r.Time.Format("2006-01-02 15:04:05") + " " + r.Level.String() + " " + r.Message
	for _, a := range attrs {
		line += " " + qualifyKey(groups, a.Key) + "=" + formatAttrValue(a.Value)
	}
	return line
}

func qualifyKey(groups []string, key string) string {
	out := ""
	for _, g := range groups {
		out += g + "."
	}
	return out + key
}

func formatAttrValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return strconv.FormatInt(v.Int64(), 10)
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", v.Any())
	}
}
