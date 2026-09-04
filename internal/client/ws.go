package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// eventChanCap bounds the subscriber's delivery queue. Periodic state refreshes
// may use all but eventPriorityReserve slots, leaving room for lifecycle and
// operation events without blocking the read pump or evicting queued events.
const (
	eventChanCap         = 256
	eventPriorityReserve = 16
)

// Events is a resilient WebSocket subscription to the server's /ws stream.
// It dials, delivers events on a channel, and reconnects with exponential
// backoff when the connection drops.
type Events struct {
	c      *Client
	query  string
	out    chan Event
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu   sync.Mutex
	conn *websocket.Conn
}

// Subscribe starts a WebSocket subscription. The query carries the server's
// scope parameters, e.g. "scope=all".
func (c *Client) Subscribe(query string) *Events {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Events{
		c:      c,
		query:  query,
		out:    make(chan Event, eventChanCap),
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go e.run()
	return e
}

// Chan returns the event stream. It is closed once Stop has completed.
func (e *Events) Chan() <-chan Event { return e.out }

// Stop cancels the subscription and waits for the read pump to exit.
func (e *Events) Stop() {
	e.cancel()
	e.mu.Lock()
	if e.conn != nil {
		_ = e.conn.Close()
	}
	e.mu.Unlock()
	<-e.done
}

func (e *Events) wsURL() string {
	base := e.c.baseURL
	if strings.HasPrefix(base, "https://") {
		base = "wss://" + strings.TrimPrefix(base, "https://")
	} else {
		base = "ws://" + strings.TrimPrefix(base, "http://")
	}
	return base + "/ws?" + e.query
}

func (e *Events) run() {
	defer close(e.out)
	defer close(e.done)

	const maxBackoff = 30 * time.Second
	backoff := 1 * time.Second
	connectedOnce := false

	for {
		if e.ctx.Err() != nil {
			return
		}

		conn, resp, err := websocket.DefaultDialer.Dial(e.wsURL(), nil)
		if err != nil {
			detail := ""
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				detail = " (endpoint not found)"
			}
			e.push(Event{Type: EventDisconnected, Ts: time.Now(),
				Raw: json.RawMessage(fmt.Sprintf(`{"error":%q}`, err.Error()+detail))})
			if !e.sleep(backoff) {
				return
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		e.mu.Lock()
		e.conn = conn
		e.mu.Unlock()

		if connectedOnce {
			e.push(Event{Type: EventReconnected, Ts: time.Now()})
		}
		connectedOnce = true
		backoff = 1 * time.Second

		err = e.readLoop(conn)

		e.mu.Lock()
		e.conn = nil
		e.mu.Unlock()
		_ = conn.Close()

		if e.ctx.Err() != nil {
			return
		}

		e.push(Event{Type: EventDisconnected, Ts: time.Now(),
			Raw: json.RawMessage(fmt.Sprintf(`{"error":%q}`, err.Error()))})
		if !e.sleep(backoff) {
			return
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (e *Events) readLoop(conn *websocket.Conn) error {
	// The server pushes 1 Hz ticks, so a read deadline detects dead
	// connections; generous enough to survive transient stalls.
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		var env wsEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue // skip malformed frames
		}
		e.push(Event{
			Type:        env.Type,
			StationID:   env.StationID,
			OperationID: env.OperationID,
			Ts:          env.Timestamp,
			Raw:         env.Data,
		})
	}
}

// sleep waits for d or until the subscription is stopped.
func (e *Events) sleep(d time.Duration) bool {
	select {
	case <-e.ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// push delivers an event without ever blocking the read pump. Periodic state
// refreshes are dropped before they consume the slots reserved for other
// events. Non-periodic events are dropped only if the entire channel is full.
func (e *Events) push(ev Event) {
	if (ev.Type == "tick" || ev.Type == "fleet_tick") && len(e.out) >= cap(e.out)-eventPriorityReserve {
		return
	}
	select {
	case e.out <- ev:
	default:
	}
}
