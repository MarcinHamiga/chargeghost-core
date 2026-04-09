package ocpp

import (
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
)

// EngineView is the read-only interface the OCPP layer uses to query engine state.
// Engine implements all these methods — no separate wrapper needed.
type EngineView interface {
	GetConnector(connectorID int) *engine.Connector
	GetSession(connectorID int) *engine.Session
	GetEnergyMeter(connectorID int) *engine.EnergyMeter
	GetConnectorIDs() []int
	GetLastStoppedSession() *engine.StoppedSessionInfo
	GetConnectorStatus(connectorID int) string
	GetMeterSnapshot(connectorID int) (float64, int)
	GetActiveTransactionID(connectorID int) *int
	GetConnectorByTransaction(transactionID int) *int
	SetActiveTransaction(connectorID, transactionID int)
	ClearActiveTransaction(connectorID int)
}

// AuthorizationCacheStore manages per-tag authorization status caching.
// Plan 5a uses a no-op implementation; Plan 5d replaces it.
type AuthorizationCacheStore interface {
	Get(idTag string) (status string, expiry *time.Time, found bool)
	Put(idTag string, status string, expiry *time.Time)
	Remove(idTag string)
	Clear()
	Size() int
}

// NoopAuthCache is an empty auth cache used before Plan 5d.
type NoopAuthCache struct{}

func (NoopAuthCache) Get(idTag string) (string, *time.Time, bool)        { return "", nil, false }
func (NoopAuthCache) Put(idTag string, status string, expiry *time.Time) {}
func (NoopAuthCache) Remove(idTag string)                                {}
func (NoopAuthCache) Clear()                                             {}
func (NoopAuthCache) Size() int                                          { return 0 }
