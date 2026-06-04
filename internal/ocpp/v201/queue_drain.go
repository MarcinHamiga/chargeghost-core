package v201

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strconv"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"

	"github.com/chargeghost/engine/internal/ocpp/queue"
)

func (b *Bridge201) deviceModelBool(component, variable string, fallback bool) bool {
	if b.deviceModel == nil {
		return fallback
	}
	result := b.deviceModel.GetVariable(component, "", 0, variable)
	if result.Status != provisioning.GetVariableStatusAccepted {
		return fallback
	}
	value, err := strconv.ParseBool(result.Value)
	if err != nil {
		return fallback
	}
	return value
}

func (b *Bridge201) deviceModelInt(component, variable string, fallback int) int {
	if b.deviceModel == nil {
		return fallback
	}
	result := b.deviceModel.GetVariable(component, "", 0, variable)
	if result.Status != provisioning.GetVariableStatusAccepted {
		return fallback
	}
	value, err := strconv.Atoi(result.Value)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func (b *Bridge201) transactionMessageAttempts() int {
	return b.deviceModelInt("OCPPCommCtrlr", "RetryBackOffRepeatTimes", 3)
}

func (b *Bridge201) transactionMessageRetryInterval() int {
	return 60
}

func (b *Bridge201) applyReplayPolicy(msg queue.QueuedMessage) queue.QueuedMessage {
	effectiveAttempts := b.transactionMessageAttempts()
	if msg.MaxRetries != effectiveAttempts {
		msg.MaxRetries = effectiveAttempts
		if err := b.queue.Update(msg); err != nil {
			slog.Warn("drainQueue: failed to update queued message policy", "type", msg.Type, "id", msg.ID, "error", err)
		}
	}
	return msg
}

func (b *Bridge201) retryPending(msg queue.QueuedMessage) bool {
	if msg.RetryCount == 0 || msg.LastAttemptAt == nil {
		return false
	}
	retryInterval := time.Duration(b.transactionMessageRetryInterval()) * time.Second
	if retryInterval <= 0 {
		return false
	}
	return time.Since(*msg.LastAttemptAt) < retryInterval
}

func (b *Bridge201) messageAttemptsExhausted(msg queue.QueuedMessage) bool {
	maxRetries := msg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = b.transactionMessageAttempts()
	}
	return msg.RetryCount >= maxRetries
}

func (b *Bridge201) markQueuedMessageFailure(msg queue.QueuedMessage, err error) {
	now := time.Now().UTC()
	msg = b.applyReplayPolicy(msg)
	if !b.messageAttemptsExhausted(msg) {
		msg.RetryCount++
	}
	msg.LastAttemptAt = &now
	msg.LastError = err.Error()

	if b.messageAttemptsExhausted(msg) {
		// Move to dead-letter so we don't retry forever. We support
		// two flavors: queues that have a DeadLetter() accessor
		// (real production queues) and queues that don't (mocks in
		// tests). In the latter case we still log and drop.
		dl, hasDL := b.queue.(queue.DeadLetterQueue)
		if hasDL && dl.DeadLetter() != nil && dl.DeadLetter().Enabled() {
			if writeErr := dl.DeadLetter().Write(msg, "exhausted"); writeErr != nil {
				slog.Error("drainQueue: failed to move exhausted message to dead-letter",
					"type", msg.Type, "id", msg.ID, "idempotencyKey", formatIdempotencyKey(msg.IdempotencyKey),
					"retryCount", msg.RetryCount, "maxRetries", msg.MaxRetries,
					"error", writeErr, "lastError", err)
			} else {
				dl.IncDropped()
				slog.Error("drainQueue: queued message exhausted, moved to dead-letter",
					"type", msg.Type, "id", msg.ID, "idempotencyKey", formatIdempotencyKey(msg.IdempotencyKey),
					"retryCount", msg.RetryCount, "maxRetries", msg.MaxRetries,
					"lastError", err)
			}
		} else {
			slog.Error("drainQueue: queued message exhausted (no dead-letter file configured)",
				"type", msg.Type, "id", msg.ID, "idempotencyKey", formatIdempotencyKey(msg.IdempotencyKey),
				"retryCount", msg.RetryCount, "maxRetries", msg.MaxRetries,
				"lastError", err)
		}
		// Remove from active queue — retries are exhausted.
		b.queue.Dequeue(msg.ID)
		return
	}

	if updateErr := b.queue.Update(msg); updateErr != nil {
		slog.Warn("drainQueue: failed to persist queued message failure", "type", msg.Type, "id", msg.ID, "error", updateErr)
	}
}

// idempotencyKeyFor derives a stable identifier from a
// TransactionEventRequest. The key is a SHA-256 hash of the
// (transactionID, sequenceNo, eventType, timestamp) tuple, encoded as
// hex. The same logical event, when re-serialized on replay, produces
// the same key. This lets operators correlate a CSMS-side event with
// the queued message that produced it.
func idempotencyKeyFor(req *transactions.TransactionEventRequest) string {
	if req == nil {
		return ""
	}
	timestamp := ""
	if req.Timestamp != nil {
		timestamp = req.Timestamp.Time.UTC().Format(time.RFC3339Nano)
	}
	h := sha256.Sum256([]byte(req.TransactionInfo.TransactionID + "|" +
		strconv.Itoa(req.SequenceNo) + "|" +
		string(req.EventType) + "|" +
		timestamp))
	return hex.EncodeToString(h[:8])
}

// IdempotencyKeyFor exposes the derivation so the senders code can
// set the key on the queued message at the moment of enqueue (which
// is what makes it survive across process restarts).
func IdempotencyKeyFor(req *transactions.TransactionEventRequest) string {
	return idempotencyKeyFor(req)
}

// formatIdempotencyKey produces a short, human-readable form for log
// output (first 12 hex chars of the full SHA-256).
func formatIdempotencyKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) > 12 {
		return key[:12]
	}
	return key
}

func (b *Bridge201) sendQueuedTransactionEvent(req interface{}) error {
	txReq, err := queuedTransactionEventRequest(req)
	if err != nil {
		return err
	}

	done := make(chan error, 1)
	sendErr := b.cs.SendRequestAsync(txReq, func(_ ocpp.Response, err error) {
		done <- err
	})
	if sendErr != nil {
		return sendErr
	}
	return <-done
}
