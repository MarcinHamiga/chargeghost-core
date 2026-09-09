package v201

import (
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"

	"github.com/chargeghost/engine/internal/persistence"
)

// File names for v201 sub-managers.
const (
	profiles201File     = "profiles_201.json"
	monitoringFile      = "monitoring.json"
	displayMessagesFile = "display_messages.json"
	costFile            = "cost.json"
	transactions201File = "transactions_201.json"
)

// --- ChargingProfileManager201 persistence ---

type managedProfileJSON struct {
	EvseID  int                   `json:"evse_id"`
	Profile types.ChargingProfile `json:"profile"`
}

func (pm *ChargingProfileManager201) SetPersistDir(dir string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.persistDir = dir
}

func (pm *ChargingProfileManager201) SaveState(dir string) error {
	pm.mu.RLock()
	entries := make([]managedProfileJSON, 0, len(pm.profiles))
	for _, mp := range pm.profiles {
		entries = append(entries, managedProfileJSON{EvseID: mp.evseID, Profile: mp.profile})
	}
	pm.mu.RUnlock()
	return persistence.WriteJSON(dir, profiles201File, entries)
}

func (pm *ChargingProfileManager201) LoadState(dir string) error {
	var entries []managedProfileJSON
	if err := persistence.ReadJSON(dir, profiles201File, &entries); err != nil {
		return err
	}
	if entries == nil {
		return nil
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.profiles = make(map[int]managedProfile, len(entries))
	for _, e := range entries {
		pm.profiles[e.Profile.ID] = managedProfile{evseID: e.EvseID, profile: e.Profile}
	}
	return nil
}

func (pm *ChargingProfileManager201) autoSave() {
	if pm.persistDir != "" {
		_ = pm.SaveState(pm.persistDir)
	}
}

// --- MonitoringManager persistence ---

type monitorJSON struct {
	ID        int         `json:"id"`
	Component string      `json:"component"`
	Instance  string      `json:"instance,omitempty"`
	EVSEID    int         `json:"evse_id"`
	Variable  string      `json:"variable"`
	Type      MonitorType `json:"type"`
	Value     float64     `json:"value"`
	Severity  int         `json:"severity"`
}

type monitoringSnapshot struct {
	Monitors []monitorJSON `json:"monitors"`
	NextID   int           `json:"next_id"`
}

func (mm *MonitoringManager) SetPersistDir(dir string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.persistDir = dir
}

func (mm *MonitoringManager) SaveState(dir string) error {
	mm.mu.RLock()
	snap := monitoringSnapshot{
		NextID:   mm.nextID,
		Monitors: make([]monitorJSON, 0, len(mm.monitors)),
	}
	for _, m := range mm.monitors {
		snap.Monitors = append(snap.Monitors, monitorJSON{
			ID: m.ID, Component: m.Component, Instance: m.Instance,
			EVSEID: m.EVSEID, Variable: m.Variable,
			Type: m.Type, Value: m.Value, Severity: m.Severity,
		})
	}
	mm.mu.RUnlock()
	return persistence.WriteJSON(dir, monitoringFile, snap)
}

func (mm *MonitoringManager) LoadState(dir string) error {
	var snap monitoringSnapshot
	if err := persistence.ReadJSON(dir, monitoringFile, &snap); err != nil {
		return err
	}
	if snap.Monitors == nil {
		return nil
	}

	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.nextID = snap.NextID
	mm.monitors = make(map[int]*Monitor, len(snap.Monitors))
	for _, mj := range snap.Monitors {
		mm.monitors[mj.ID] = &Monitor{
			ID: mj.ID, Component: mj.Component, Instance: mj.Instance,
			EVSEID: mj.EVSEID, Variable: mj.Variable,
			Type: mj.Type, Value: mj.Value, Severity: mj.Severity,
		}
	}
	return nil
}

func (mm *MonitoringManager) autoSave() {
	if mm.persistDir != "" {
		_ = mm.SaveState(mm.persistDir)
	}
}

// --- DisplayMessageStore persistence ---

type displayMessageJSON struct {
	ID       int    `json:"id"`
	Priority string `json:"priority"`
	State    string `json:"state"`
	Text     string `json:"text"`
	Language string `json:"language,omitempty"`
}

func (s *DisplayMessageStore) SetPersistDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persistDir = dir
}

func (s *DisplayMessageStore) SaveState(dir string) error {
	s.mu.RLock()
	msgs := make([]displayMessageJSON, 0, len(s.messages))
	for _, m := range s.messages {
		msgs = append(msgs, displayMessageJSON{
			ID: m.ID, Priority: m.Priority, State: m.State,
			Text: m.Text, Language: m.Language,
		})
	}
	s.mu.RUnlock()
	return persistence.WriteJSON(dir, displayMessagesFile, msgs)
}

func (s *DisplayMessageStore) LoadState(dir string) error {
	var msgs []displayMessageJSON
	if err := persistence.ReadJSON(dir, displayMessagesFile, &msgs); err != nil {
		return err
	}
	if msgs == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = make(map[int]DisplayMessage, len(msgs))
	for _, mj := range msgs {
		s.messages[mj.ID] = DisplayMessage{
			ID: mj.ID, Priority: mj.Priority, State: mj.State,
			Text: mj.Text, Language: mj.Language,
		}
	}
	return nil
}

func (s *DisplayMessageStore) autoSave() {
	if s.persistDir != "" {
		_ = s.SaveState(s.persistDir)
	}
}

// --- CostStore persistence ---

func (cs *CostStore) SetPersistDir(dir string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.persistDir = dir
}

func (cs *CostStore) SaveState(dir string) error {
	cs.mu.RLock()
	// json.Marshal handles map[string]float64 directly.
	data := make(map[string]float64, len(cs.costs))
	for k, v := range cs.costs {
		data[k] = v
	}
	cs.mu.RUnlock()
	return persistence.WriteJSON(dir, costFile, data)
}

func (cs *CostStore) LoadState(dir string) error {
	data := make(map[string]float64)
	if err := persistence.ReadJSON(dir, costFile, &data); err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.costs = data
	return nil
}

func (cs *CostStore) autoSave() {
	if cs.persistDir != "" {
		_ = cs.SaveState(cs.persistDir)
	}
}

// --- Bridge201 convenience ---

// transactionRecord is the persisted form of one live transaction: the
// string UUID the CSMS knows, the synthetic int the engine uses, the EVSE
// it belongs to, and the next sequence number to emit.
type transactionRecord struct {
	EVSEID      int      `json:"evse_id"`
	ConnectorID int      `json:"connector_id"`
	TxInt       int      `json:"tx_int"`
	TxUUID      string   `json:"tx_uuid"`
	NextSeq     int      `json:"next_seq"`
	LastCost    *float64 `json:"last_cost,omitempty"`
}

type transactionsSnapshot struct {
	NextTxInt    int                 `json:"next_tx_int"`
	Transactions []transactionRecord `json:"transactions"`
}

// saveTransactions persists the live transaction-builder state so a
// restart continues the same UUIDs and sequence numbers instead of
// splitting transactions.
func (b *Bridge201) saveTransactions(dir string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	intByEVSE := make(map[int]int, len(b.txIntToEVSE))
	for txInt, evseID := range b.txIntToEVSE {
		intByEVSE[evseID] = txInt
	}
	snap := transactionsSnapshot{NextTxInt: b.nextTxInt}
	for evseID, builder := range b.txBuilders {
		snap.Transactions = append(snap.Transactions, transactionRecord{
			EVSEID:      evseID,
			ConnectorID: builder.connectorID,
			TxInt:       intByEVSE[evseID],
			TxUUID:      builder.TransactionID(),
			NextSeq:     builder.seqNo,
			LastCost:    builder.LastCost,
		})
	}
	return persistence.WriteJSON(dir, transactions201File, snap)
}

// loadTransactions restores the live transaction-builder state saved by
// saveTransactions. A missing file is a fresh station, not an error.
func (b *Bridge201) loadTransactions(dir string) error {
	var snap transactionsSnapshot
	if err := persistence.ReadJSON(dir, transactions201File, &snap); err != nil {
		return err
	}
	if snap.Transactions == nil && snap.NextTxInt == 0 {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.txBuilders = make(map[int]*TransactionEventBuilder, len(snap.Transactions))
	b.txIntToEVSE = make(map[int]int, len(snap.Transactions))
	b.txStringToEVSE = make(map[string]int, len(snap.Transactions))
	maxTxInt := 0
	for _, rec := range snap.Transactions {
		if rec.TxUUID == "" {
			continue
		}
		builder := RestoreTransactionEventBuilder(rec.TxUUID, rec.EVSEID, rec.ConnectorID, rec.NextSeq)
		builder.LastCost = rec.LastCost
		b.txBuilders[rec.EVSEID] = builder
		b.txStringToEVSE[rec.TxUUID] = rec.EVSEID
		if rec.TxInt != 0 {
			b.txIntToEVSE[rec.TxInt] = rec.EVSEID
			if rec.TxInt > maxTxInt {
				maxTxInt = rec.TxInt
			}
		}
	}
	if snap.NextTxInt > maxTxInt {
		maxTxInt = snap.NextTxInt
	}
	b.nextTxInt = maxTxInt
	return nil
}

// persistTransactions auto-saves builder state after every mutation when a
// persist dir is configured. Best-effort: failures are logged by callers
// only if they care; a missed save is repaired by the next mutation.
func (b *Bridge201) persistTransactions() {
	if b.txPersistDir == "" {
		return
	}
	_ = b.saveTransactions(b.txPersistDir)
}

// SaveState persists all v201 sub-manager state.
func (b *Bridge201) SaveState(dir string) error {
	var firstErr error
	save := func(name string, fn func(string) error) {
		if err := fn(dir); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	save("device_model", b.deviceModel.SaveState)
	save("profiles", b.profileManager.SaveState)
	save("monitoring", b.monitoringManager.SaveState)
	save("display", b.displayStore.SaveState)
	save("cost", b.costStore.SaveState)
	save("transactions", b.saveTransactions)
	return firstErr
}

// LoadState restores all v201 sub-manager state.
func (b *Bridge201) LoadState(dir string) error {
	var firstErr error
	load := func(name string, fn func(string) error) {
		if err := fn(dir); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	load("device_model", b.deviceModel.LoadState)
	load("profiles", b.profileManager.LoadState)
	load("monitoring", b.monitoringManager.LoadState)
	load("display", b.displayStore.LoadState)
	load("cost", b.costStore.LoadState)
	load("transactions", b.loadTransactions)
	return firstErr
}

// SetPersistDir enables auto-save on all v201 sub-managers.
func (b *Bridge201) SetPersistDir(dir string) {
	b.deviceModel.SetPersistDir(dir)
	b.profileManager.SetPersistDir(dir)
	b.monitoringManager.SetPersistDir(dir)
	b.displayStore.SetPersistDir(dir)
	b.costStore.SetPersistDir(dir)
	b.txPersistDir = dir
}
