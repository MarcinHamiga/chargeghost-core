package ocpp

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	"github.com/lorenzodonini/ocpp-go/ocppj"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyCallError_NilIsAck(t *testing.T) {
	action, code := ClassifyCallError(nil)
	assert.Equal(t, DeliveryAck, action)
	assert.Empty(t, code)
}

func TestClassifyCallError_FatalCodesDeadLetter(t *testing.T) {
	fatal := []ocpp.ErrorCode{
		ocppj.NotImplemented,
		ocppj.NotSupported,
		ocppj.FormatViolationV16,
		ocppj.FormatViolationV2,
		ocppj.PropertyConstraintViolation,
		ocppj.OccurrenceConstraintViolation,
		ocppj.TypeConstraintViolation,
		ocppj.ProtocolError,
		ocppj.SecurityError,
		ocppj.MessageTypeNotSupported,
		// Forward-compat strings without library constants.
		"RpcFrameworkError",
		"OccurenceConstraintViolation",
	}
	for _, code := range fatal {
		t.Run(string(code), func(t *testing.T) {
			action, got := ClassifyCallError(ocpp.NewError(code, "rejected", "msg-1"))
			assert.Equal(t, DeliveryDeadLetter, action)
			assert.Equal(t, string(code), got)
		})
	}
}

func TestClassifyCallError_TransientCodesRetry(t *testing.T) {
	for _, code := range []ocpp.ErrorCode{ocppj.InternalError, ocppj.GenericError, "SomeFutureCode"} {
		t.Run(string(code), func(t *testing.T) {
			action, got := ClassifyCallError(ocpp.NewError(code, "try again", "msg-1"))
			assert.Equal(t, DeliveryRetry, action)
			assert.Equal(t, string(code), got)
		})
	}
}

func TestClassifyCallError_NonWireErrorsRetry(t *testing.T) {
	action, code := ClassifyCallError(errors.New("send failed: timeout"))
	assert.Equal(t, DeliveryRetry, action)
	assert.Empty(t, code)
}

func TestClassifyCallError_WrappedWireErrorUnwraps(t *testing.T) {
	wrapped := fmt.Errorf("drain send: %w", ocpp.NewError(ocppj.NotSupported, "nope", "msg-2"))
	action, code := ClassifyCallError(wrapped)
	require.Equal(t, DeliveryDeadLetter, action)
	assert.Equal(t, "NotSupported", code)
}
