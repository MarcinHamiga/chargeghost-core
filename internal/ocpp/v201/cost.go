package v201

import "sync"

// CostStore tracks running transaction costs from CSMS CostUpdated messages.
type CostStore struct {
	mu    sync.RWMutex
	costs map[string]float64 // keyed by transaction ID
}

func NewCostStore() *CostStore {
	return &CostStore{costs: make(map[string]float64)}
}

func (cs *CostStore) Update(transactionID string, totalCost float64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.costs[transactionID] = totalCost
}

func (cs *CostStore) Get(transactionID string) (float64, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	cost, ok := cs.costs[transactionID]
	return cost, ok
}

func (cs *CostStore) Clear(transactionID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.costs, transactionID)
}
