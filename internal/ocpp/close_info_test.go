package ocpp

import (
	"errors"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

func TestFormatDisconnectReason_NilReturnsEmptyString(t *testing.T) {
	assert.Equal(t, "", FormatDisconnectReason(nil))
}

func TestFormatDisconnectReason_PlainError(t *testing.T) {
	got := FormatDisconnectReason(errors.New("connection reset by peer"))
	assert.Equal(t, "connection reset by peer", got)
}

func TestFormatDisconnectReason_CloseErrorWithCodeAndText(t *testing.T) {
	ce := &websocket.CloseError{Code: websocket.ClosePolicyViolation, Text: "billing suspended"}
	got := FormatDisconnectReason(ce)
	// Expect: "close 1008 (policy_violation): billing suspended"
	assert.Contains(t, got, "close 1008")
	assert.Contains(t, got, "policy_violation")
	assert.Contains(t, got, "billing suspended")
}

func TestFormatDisconnectReason_CloseErrorNoText(t *testing.T) {
	ce := &websocket.CloseError{Code: websocket.CloseInternalServerErr}
	got := FormatDisconnectReason(ce)
	assert.Contains(t, got, "close 1011")
	assert.Contains(t, got, "internal_server_error")
}

func TestFormatDisconnectReason_AppLevelCode(t *testing.T) {
	// CSMS uses 4xxx for application-level kicks.
	ce := &websocket.CloseError{Code: 4001}
	got := FormatDisconnectReason(ce)
	assert.Contains(t, got, "close 4001")
	assert.Contains(t, got, "app_code")
}

func TestFormatDisconnectReason_UnknownCodeUsesLiteralCode(t *testing.T) {
	ce := &websocket.CloseError{Code: 3999}
	got := FormatDisconnectReason(ce)
	// 3999 is in the 3xxx range (libraries/frameworks) so falls into the
	// default branch and is labelled simply "code".
	assert.Contains(t, got, "close 3999")
	assert.Contains(t, got, "code")
}
