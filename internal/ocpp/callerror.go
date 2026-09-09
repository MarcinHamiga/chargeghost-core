package ocpp

import (
	"errors"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	"github.com/lorenzodonini/ocpp-go/ocppj"
)

// DeliveryAction tells a queue drain loop what to do with a queued message
// after a send attempt.
type DeliveryAction int

const (
	// DeliveryAck dequeues the message: the CSMS accepted it.
	DeliveryAck DeliveryAction = iota
	// DeliveryRetry keeps the message queued with backoff: the failure was
	// transient and a later attempt may succeed.
	DeliveryRetry
	// DeliveryDeadLetter removes the message to dead-letter storage: the CSMS
	// deterministically rejects it, so retrying would only burn retry budget
	// and head-of-line-block the messages queued behind it.
	DeliveryDeadLetter
)

// ClassifyCallError maps a send error to a drain action plus the wire
// CALLERROR code ("" when the error is not an OCPP wire error).
//
// A nil error is DeliveryAck. An *ocpp.Error carrying a deterministic
// rejection code (unknown action, unsupported action, malformed payload,
// constraint violation, security refusal) is DeliveryDeadLetter. An
// *ocpp.Error with a transient code (InternalError, GenericError, unknown
// codes) — or any non-wire error such as timeouts and transport failures —
// is DeliveryRetry. Unknown shapes default to retry so billing evidence is
// never discarded on a guess.
func ClassifyCallError(err error) (DeliveryAction, string) {
	if err == nil {
		return DeliveryAck, ""
	}
	var ocppErr *ocpp.Error
	if !errors.As(err, &ocppErr) {
		return DeliveryRetry, ""
	}
	code := string(ocppErr.Code)
	if isFatalCallErrorCode(code) {
		return DeliveryDeadLetter, code
	}
	return DeliveryRetry, code
}

// isFatalCallErrorCode reports whether the CSMS will deterministically
// reject any resend of the message, making retries pointless.
func isFatalCallErrorCode(code string) bool {
	switch code {
	case string(ocppj.NotImplemented),
		string(ocppj.NotSupported),
		string(ocppj.FormatViolationV16), // "FormationViolation" (1.6 spelling)
		string(ocppj.FormatViolationV2),  // "FormatViolation" (2.0.1 spelling)
		string(ocppj.PropertyConstraintViolation),
		string(ocppj.OccurrenceConstraintViolation),
		string(ocppj.TypeConstraintViolation),
		string(ocppj.ProtocolError),
		string(ocppj.SecurityError),
		string(ocppj.MessageTypeNotSupported),
		// OCPP 2.1 RPC-framework code, matched by string so ocpp-go
		// versions without the constant still classify it.
		"RpcFrameworkError",
		// Tolerate the common misspelling seen in the wild.
		"OccurenceConstraintViolation":
		return true
	default:
		return false
	}
}
