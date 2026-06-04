package ocpp

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chargeghost/engine/internal/timeline"
)

func TestTimelineLoggerCorrelatesOutboundResponseAndError(t *testing.T) {
	store := timeline.NewStore(10)
	logger := NewTimelineLogger(store)

	payload := map[string]string{"status": "test"}
	messageID := logger.LogOutbound("Heartbeat", nil, nil, "Heartbeat", payload)
	if messageID == "" {
		t.Fatal("expected message ID")
	}

	logger.LogResponse("Heartbeat", nil, nil, messageID, 25*time.Millisecond, "Accepted", map[string]string{"currentTime": "now"})
	logger.LogError("Heartbeat", "outbound", nil, errors.New("boom").Error(), payload, messageID)

	events, total := store.Query(timeline.TimelineFilter{Limit: 10})
	if total != 3 {
		t.Fatalf("expected 3 events, got %d", total)
	}

	for _, evt := range events {
		if evt.CorrelationKey == nil || *evt.CorrelationKey != messageID {
			t.Fatalf("expected correlation key %q, got %#v", messageID, evt.CorrelationKey)
		}
	}
	if !strings.Contains(events[1].Summary, "rtt=25ms") {
		t.Fatalf("expected response summary to include RTT, got %q", events[1].Summary)
	}
	if events[0].Payload == nil {
		t.Fatal("expected error event to retain request payload")
	}
}

func TestTimelineLoggerGeneratesInboundCorrelationKey(t *testing.T) {
	store := timeline.NewStore(10)
	logger := NewTimelineLogger(store)

	correlationKey := logger.LogInbound("Reset", nil, "type=Immediate", nil, "")
	if correlationKey == "" {
		t.Fatal("expected generated correlation key")
	}

	events, total := store.Query(timeline.TimelineFilter{Limit: 10})
	if total != 1 {
		t.Fatalf("expected 1 event, got %d", total)
	}
	if events[0].MessageID != correlationKey {
		t.Fatalf("expected message id %q, got %q", correlationKey, events[0].MessageID)
	}
	if events[0].CorrelationKey == nil || *events[0].CorrelationKey != correlationKey {
		t.Fatalf("expected correlation key %q, got %#v", correlationKey, events[0].CorrelationKey)
	}
}
