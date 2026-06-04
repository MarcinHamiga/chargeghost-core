package ocpp

import (
	"fmt"

	"github.com/gorilla/websocket"
)

// FormatDisconnectReason extracts a human-readable description of why the
// OCPP WebSocket was disconnected. When the peer (CSMS) closes the link,
// the ocpp-go library surfaces a *websocket.CloseError; we surface the close
// code and any reason text so operators can distinguish a graceful shutdown
// (1000) from a server error (1011) or a CSMS policy kick (e.g. 4001).
//
// Network-level errors (connection reset, EOF) are returned as err.Error()
// so the operator still gets a string. nil errors produce an empty string.
func FormatDisconnectReason(err error) string {
	if err == nil {
		return ""
	}
	if ce, ok := err.(*websocket.CloseError); ok {
		// Text is optional in the close frame. Include it when present so
		// operators can see "kicked: billing suspended" etc.
		if ce.Text != "" {
			return fmt.Sprintf("close %d (%s): %s", ce.Code, closeCodeName(ce.Code), ce.Text)
		}
		return fmt.Sprintf("close %d (%s)", ce.Code, closeCodeName(ce.Code))
	}
	return err.Error()
}

// closeCodeName maps a subset of well-known WebSocket close codes to a
// short, human-friendly name. Unknown codes are returned as "code" so the
// numeric value is still useful.
func closeCodeName(code int) string {
	switch code {
	case websocket.CloseNormalClosure:
		return "normal_closure"
	case websocket.CloseGoingAway:
		return "going_away"
	case websocket.CloseProtocolError:
		return "protocol_error"
	case websocket.CloseUnsupportedData:
		return "unsupported_data"
	case websocket.CloseNoStatusReceived:
		return "no_status_received"
	case websocket.CloseAbnormalClosure:
		return "abnormal_closure"
	case websocket.CloseInvalidFramePayloadData:
		return "invalid_frame_payload"
	case websocket.ClosePolicyViolation:
		return "policy_violation"
	case websocket.CloseMessageTooBig:
		return "message_too_big"
	case websocket.CloseMandatoryExtension:
		return "mandatory_extension"
	case websocket.CloseInternalServerErr:
		return "internal_server_error"
	case websocket.CloseTLSHandshake:
		return "tls_handshake"
	default:
		// OCPP CSMSes often use 4xxx for application-level codes
		// (e.g. 4001 "chargepoint kicked"). Tag them as "app" so
		// operators can spot application-level disconnects vs. transport
		// errors at a glance.
		if code >= 4000 && code < 5000 {
			return "app_code"
		}
		return "code"
	}
}
