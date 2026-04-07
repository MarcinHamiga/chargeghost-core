package ocpp

import "sync"

// DataTransferHandler is called when a DataTransfer request arrives for a registered vendorID/messageID pair.
type DataTransferHandler func(messageID, data string) (status, responseData string)

type vendorKey struct{ vendorID, messageID string }

// DataTransferRegistry routes inbound DataTransfer messages to registered handlers.
type DataTransferRegistry struct {
	mu       sync.RWMutex
	handlers map[vendorKey]DataTransferHandler
}

func NewDataTransferRegistry() *DataTransferRegistry {
	return &DataTransferRegistry{handlers: make(map[vendorKey]DataTransferHandler)}
}

// Register maps a vendorID/messageID pair to a handler.
func (r *DataTransferRegistry) Register(vendorID, messageID string, handler DataTransferHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[vendorKey{vendorID, messageID}] = handler
}

// Dispatch calls the handler for the given vendor/message pair.
// Returns "UnknownVendorId" if no handler is registered.
func (r *DataTransferRegistry) Dispatch(vendorID, messageID, requestMessageID, data string) (status, responseData string) {
	r.mu.RLock()
	handler, ok := r.handlers[vendorKey{vendorID, messageID}]
	r.mu.RUnlock()
	if !ok {
		return "UnknownVendorId", ""
	}
	return handler(requestMessageID, data)
}
