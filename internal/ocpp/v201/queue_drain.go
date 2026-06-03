package v201

import (
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"

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
	if msg.RetryCount >= msg.MaxRetries {
		slog.Warn("drainQueue: queued message moved to exhausted state",
			"type", msg.Type,
			"id", msg.ID,
			"retryCount", msg.RetryCount,
			"maxRetries", msg.MaxRetries,
			"error", err)
	}
	if updateErr := b.queue.Update(msg); updateErr != nil {
		slog.Warn("drainQueue: failed to persist queued message failure", "type", msg.Type, "id", msg.ID, "error", updateErr)
	}
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
