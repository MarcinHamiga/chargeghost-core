# Remediation Wave 2: Durable Transaction Delivery Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Persist transaction lifecycle and billable meter events at occurrence time and deliver them correctly through disconnects, transient failures, and process restarts.

**Architecture:** Add immutable engine domain events and a typed protocol outbox. Transaction delivery no longer depends on in-memory dispatcher closures; OCPP 1.6J resolves a stable local session ID to the CSMS integer, while OCPP 2.0.1 uses the local UUID and persisted sequence numbers directly.

**Tech Stack:** Go, existing queue persistence primitives, JSON versioned envelopes, OCPP adapters, integration tests.

## Task 1: Add stable session identity and occurrence timestamps

**Files:**
- Create: `internal/engine/events.go`
- Modify: `internal/engine/session.go`
- Modify: `internal/engine/engine.go`
- Modify: `internal/engine/persist.go`
- Test: `internal/engine/events_test.go`

**Step 1: Write failing tests**

Verify that SessionStarted, EnergySampled, ChargingStateChanged, and SessionStopped events share one stable `SessionID`, preserve strict occurrence timestamps supplied by a fake clock, and have monotonically increasing per-session event numbers.

**Step 2: Run tests**

```bash
go test ./internal/engine -run 'DomainEvent|SessionID' -count=1 -v
```

Expected: FAIL because the types do not exist.

**Step 3: Add domain event types**

```go
type SessionID string

type EventType string

const (
    EventSessionStarted       EventType = "session_started"
    EventChargingStateChanged EventType = "charging_state_changed"
    EventEnergySampled        EventType = "energy_sampled"
    EventSessionStopped       EventType = "session_stopped"
)

type DomainEvent struct {
    ID          string
    Type        EventType
    OccurredAt  time.Time
    ConnectorID int
    SessionID   SessionID
    SequenceNo  int
    Data        any
}
```

Add `SessionID` and `NextEventSequence` to `Session`. Inject an engine clock function defaulting to `time.Now`. Generate the session UUID before any start callback/event.

**Step 4: Preserve legacy callbacks temporarily**

Add `OnDomainEvent func(DomainEvent)` while retaining existing callbacks for WebSocket compatibility. Fire the domain event first, after releasing the engine lock, with captured immutable data.

**Step 5: Persist the new fields**

Add fields without removing current transaction fields. Wave 3 adds formal schema migrations; Wave 2 must round-trip newly created state.

**Step 6: Verify and commit**

```bash
go test ./internal/engine -run 'DomainEvent|SessionID|SaveLoadState' -count=1
git add internal/engine
git commit -m "feat(engine): emit stable timestamped session events"
```

## Task 2: Introduce a typed durable outbox

**Files:**
- Create: `internal/ocpp/outbox/record.go`
- Create: `internal/ocpp/outbox/store.go`
- Create: `internal/ocpp/outbox/json_store.go`
- Create: `internal/ocpp/outbox/store_test.go`
- Modify: `internal/ocpp/queue/queue.go`

**Step 1: Write the store contract tests**

Test append-before-delivery, stable ordering, acknowledge, retry update, JSON restart, malformed-record retention/dead-letter, and concurrent append/drain.

**Step 2: Run tests**

```bash
go test ./internal/ocpp/outbox -count=1 -v
```

Expected: FAIL because the package does not exist.

**Step 3: Implement the record and store interfaces**

Use the `OutboxRecord` shape from the design document. Add:

```go
type Store interface {
    Append(Record) error
    PeekReady(time.Time) (Record, bool, error)
    MarkAttempt(id string, next time.Time, lastErr string) error
    Acknowledge(id string) error
    Len() int
}
```

The JSON store must write atomically using `internal/persistence.WriteJSON`. Do not store `func`, `error`, or raw protocol request pointers.

**Step 4: Reuse queue dead-letter policy deliberately**

Extract or wrap existing retry/dead-letter behavior rather than maintaining two inconsistent policies. Keep legacy `MessageQueue` until migration tests prove old queues can be upgraded.

**Step 5: Verify and commit**

```bash
go test -race ./internal/ocpp/outbox -count=1
git add internal/ocpp/outbox internal/ocpp/queue
git commit -m "feat(ocpp): add typed durable protocol outbox"
```

## Task 3: Translate domain events into protocol outbox records

**Files:**
- Create: `internal/ocpp/event_adapter.go`
- Modify: `internal/ocpp/bridge.go`
- Modify: `cmd/chargeghost/callbacks.go`
- Modify: `cmd/chargeghost/station_runtime.go`
- Test: `cmd/chargeghost/callbacks_test.go`
- Test: `internal/ocpp/event_adapter_test.go`

**Step 1: Write failing callback-boundary tests**

With `bridge.IsConnected()==false`, start, sample, and stop a session and assert three durable records are written immediately with physical occurrence timestamps. Assert the command dispatcher contains no transaction-critical closure.

**Step 2: Define the adapter interface**

```go
type DomainEventAdapter interface {
    PersistEvent(engine.DomainEvent) error
}
```

Create version-specific adapters in later tasks but wire `Engine.OnDomainEvent` once in `station_runtime.go`. Existing WebSocket callbacks remain independent.

**Step 3: Remove transaction delivery from ephemeral callbacks**

`newSessionStartedCallback`, `newSessionStoppedCallback`, and `newChargingStateChangedCallback` should broadcast UI events only after their corresponding outbox adapters are active. Meter sampling appends an EnergySampled event/record instead of enqueueing a closure.

**Step 4: Restrict CommandDispatcher scope**

Document and test that it handles point-in-time or reconstructible messages such as heartbeat, current status refresh, and reports. Remove its link-down requeue policy for durable transaction work; a transport error must never be the only copy of a transaction event.

**Step 5: Verify and commit**

```bash
go test ./cmd/chargeghost ./internal/ocpp -run 'DomainEvent|Outbox|Disconnected' -count=1
git add cmd/chargeghost internal/ocpp
git commit -m "refactor(ocpp): persist transaction events before delivery"
```

## Task 4: Implement OCPP 1.6J local-to-CSMS transaction mapping

**Files:**
- Create: `internal/ocpp/v16/transaction_store.go`
- Create: `internal/ocpp/v16/transaction_store_test.go`
- Modify: `internal/ocpp/v16/senders.go`
- Modify: `internal/ocpp/v16/bridge.go`
- Modify: `internal/ocpp/v16/queue_payloads.go`
- Modify: `internal/ocpp/v16/senders_test.go`

**Step 1: Write failing end-to-end replay tests**

Scenario: offline SessionStarted → two meter samples → SessionStopped; restart; reconnect; StartTransaction returns `77`; assert both MeterValues and StopTransaction use `77`, retain original timestamps, and send in order.

Add the online rejection scenario: StartTransaction response contains non-Accepted `idTagInfo`; assert the generated StopTransaction uses the assigned CSMS transaction ID.

**Step 2: Add a persistent mapping**

```go
type TransactionMapping struct {
    SessionID         engine.SessionID `json:"session_id"`
    ConnectorID       int              `json:"connector_id"`
    CSMSTransactionID *int             `json:"csms_transaction_id,omitempty"`
    State             string           `json:"state"`
}
```

All queued records refer to `SessionID`. The delivery worker blocks later records for that session until StartTransaction has assigned and persisted the integer.

**Step 3: Stop using provisional IDs as durable references**

Engine integer transaction fields may remain for compatibility, but outbox resolution must never encode `-1` or an API-supplied placeholder. Update the engine snapshot only after the mapping is durable.

**Step 4: Verify and commit**

```bash
go test ./internal/ocpp/v16 -run 'Replay.*Transaction|Rejected.*Assigned' -count=1 -v
git add internal/ocpp/v16
git commit -m "fix(ocpp16): resolve queued events through stable session mappings"
```

## Task 5: Implement OCPP 2.0.1 UUID and sequence recovery

**Files:**
- Create: `internal/ocpp/v201/transaction_store.go`
- Create: `internal/ocpp/v201/transaction_store_test.go`
- Modify: `internal/ocpp/v201/transaction.go`
- Modify: `internal/ocpp/v201/senders.go`
- Modify: `internal/ocpp/v201/bridge.go`
- Modify: `internal/ocpp/v201/persist.go`
- Modify: `internal/ocpp/v201/replay_test.go`

**Step 1: Write failing restart tests**

Start and suspend a transaction, persist, reconstruct a new bridge, send another meter event, stop, and assert one transaction UUID with sequence numbers `0..N`, original timestamps, and `offline=true` for events that occurred while disconnected.

**Step 2: Make the engine session UUID canonical**

`TransactionEventBuilder` accepts an existing transaction ID and next sequence instead of generating both privately:

```go
func NewTransactionEventBuilder(txID string, evseID, connectorID, nextSeq int) *TransactionEventBuilder
```

Persist the builder state or derive it from the last durable outbox record. Do not maintain an unpersisted integer mapping as the only stop lookup.

**Step 3: Process asynchronous failures through the outbox**

The delivery callback acknowledges only success. Transport/protocol errors update retry metadata. Authorization rejection emits a new durable SessionStopped domain event with reason DeAuthorized.

**Step 4: Verify and commit**

```bash
go test ./internal/ocpp/v201 -run 'Restart|Offline|Sequence|TransactionEventReplay' -count=1 -v
git add internal/ocpp/v201
git commit -m "fix(ocpp201): persist transaction UUID and event sequence state"
```

## Task 6: Migrate existing queue payloads and complete the delivery worker

**Files:**
- Create: `internal/ocpp/outbox/migrate.go`
- Create: `internal/ocpp/outbox/testdata/legacy_v16_queue.json`
- Create: `internal/ocpp/outbox/testdata/legacy_v201_queue.json`
- Create: `internal/ocpp/outbox/migrate_test.go`
- Modify: `cmd/chargeghost/station_runtime.go`
- Modify: `internal/ocpp/health_ticker.go`

**Step 1: Add golden migration tests**

Legacy records that contain enough information must become typed outbox records. Ambiguous records must remain visible in dead-letter storage with a migration reason; never silently dequeue them.

**Step 2: Add one station-local delivery loop**

The loop waits for registration and connectivity, delivers ready records in protocol-safe order, uses configurable retry policy, and stops cleanly with the station context.

**Step 3: Expose health**

Include outbox depth, oldest-record age, retry count, and dead-letter count in the existing status/health surfaces.

**Step 4: Run Wave 2 gates**

```bash
go fmt ./...
go test ./...
go test -race ./internal/engine ./internal/ocpp/... ./cmd/chargeghost
go test -tags integration ./internal/ocpp/v201 -v -timeout 90s
go vet ./...
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/ocpp/outbox cmd/chargeghost internal/ocpp
git commit -m "feat(ocpp): migrate and deliver durable transaction outbox"
```
