package v16

import "github.com/gorilla/websocket"

// isGracefulClose reports whether err represents a graceful WebSocket
// closure initiated by the peer (close code 1000 normal closure, 1001
// going away, or no error at all). We use it to pick a less alarming log
// level for intentional shutdowns.
func isGracefulClose(err error) bool {
	if err == nil {
		return true
	}
	ce, ok := err.(*websocket.CloseError)
	if !ok {
		return false
	}
	switch ce.Code {
	case websocket.CloseNormalClosure, websocket.CloseGoingAway:
		return true
	}
	return false
}
