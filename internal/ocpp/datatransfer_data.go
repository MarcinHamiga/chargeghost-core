package ocpp

import (
	"encoding/json"
	"fmt"
)

// DataTransferDataString returns the payload for DataTransfer handlers as a string.
// JSON objects from the OCPP library are marshaled; plain strings pass through.
func DataTransferDataString(data any) string {
	if data == nil {
		return ""
	}
	switch v := data.(type) {
	case string:
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}
