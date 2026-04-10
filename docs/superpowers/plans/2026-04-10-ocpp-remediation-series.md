# OCPP Remediation Series

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers` or `feature-dev` to execute these waves sequentially. Do not skip forward until each wave's acceptance criteria pass.

**Goal:** Remediate the major OCPP faux-features and broken behaviors in both OCPP 1.6J and OCPP 2.0.1, with priority on correctness of outbound communications, queue durability, protocol-visible behavior, and documentation truthfulness.

**Why this series exists:** Several OCPP features are currently advertised, exposed through REST, or acknowledged at the protocol layer without producing the expected runtime or CSMS-visible effect. The largest risks are dropped offline traffic, broken replay after restart, miswired raw APIs, silent local auth list failures, fake reset behavior, and accepted-but-no-op 2.0.1 operations.

**Execution order:** Work top to bottom. Later waves assume earlier waves are complete.

---

## Wave Overview

### Wave 1: Outbound Reliability and Queue Durability

**Focus:** Ensure disconnected operation does not silently drop outbound OCPP traffic, and ensure persisted OCPP 2.0.1 messages replay correctly after restart.

**Primary files:** `cmd/chargeghost/main.go`, `internal/ocpp/meter_ticker.go`, `internal/ocpp/queue/*`, `internal/ocpp/v16/senders.go`, `internal/ocpp/v201/senders.go`, `internal/ocpp/v201/bridge.go`

**Reason first:** This is the most breaking issue. The code claims durable offline queueing, but the main callback layer often prevents queueable traffic from ever reaching the senders.

### Wave 2: Raw OCPP API and Local Authorization Correctness

**Focus:** Replace broken raw start/stop transaction REST routes with real OCPP handlers, and implement proper differential local auth list deletes in both protocol versions.

**Primary files:** `internal/api/router.go`, `internal/api/handlers/ocpp.go`, `internal/api/dto.go`, `internal/ocpp/v16/handlers.go`, `internal/ocpp/v201/handlers.go`, `internal/ocpp/local_auth_list.go`

**Reason second:** These are externally visible correctness bugs with clear API and protocol impact.

### Wave 3: Reset and Diagnostics Truthfulness

**Focus:** Make OCPP 2.0.1 reset actually reset charger runtime state, and implement real 2.0.1 diagnostics status signaling instead of a stub.

**Primary files:** `internal/ocpp/v201/handlers.go`, `internal/ocpp/v201/senders.go`, `cmd/chargeghost/main.go`, `internal/ocpp/firmware_manager.go`

**Reason third:** These are protocol-visible lifecycle behaviors that currently mislead the CSMS.

### Wave 4: Runtime Config and Meter Semantics

**Focus:** Make live config changes actually affect the running bridge, ticker, and heartbeat behavior, and stop discarding caller-supplied meter context semantics.

**Primary files:** `internal/api/handlers/ocpp.go`, `cmd/chargeghost/main.go`, `internal/ocpp/meter_ticker.go`, `internal/ocpp/v16/config_keys.go`, `internal/ocpp/v16/senders.go`, `internal/ocpp/v201/device_model.go`, `internal/ocpp/v201/senders.go`, `internal/ocpp/v201/transaction.go`

**Reason fourth:** These are softer than the earlier blockers, but still create fake configurability and inaccurate protocol data.

### Wave 5: Control Path Corrections and Honest Responses

**Focus:** Fix 1.6J handlers that report success without effect, and change unsupported 2.0.1 handlers to reject or report unsupported instead of fake success.

**Primary files:** `internal/ocpp/v16/handlers.go`, `internal/ocpp/v16/profile_manager.go`, `internal/ocpp/v201/handlers.go`

**Reason fifth:** This reduces false-positive interoperability and makes the simulator honest about what it can and cannot do.

### Wave 6: Monitoring, Reporting, Composite Schedule, and Documentation Truth

**Focus:** Either implement or explicitly narrow 2.0.1 monitoring, reporting, and composite schedule behavior, then align docs and runtime capability advertisement with reality.

**Primary files:** `internal/ocpp/v201/monitoring.go`, `internal/ocpp/v201/handlers.go`, `internal/ocpp/v201/device_model.go`, `internal/ocpp/v201/profile_manager.go`, `internal/api/handlers/about.go`, `REST_API.md`, `docs/REST_API.md`, `README.md`

**Reason sixth:** This wave depends on all earlier behavior being stabilized first.

---

## Global Rules

- Preserve current OCPP 1.6J behavior where it is already correct.
- Prefer explicit unsupported responses over accepted no-ops.
- Do not advertise durable queueing, monitoring, diagnostics, or raw OCPP operations unless the implementation and tests prove they work.
- Every wave must add or update tests before moving on.
- Do not collapse protocol-layer fixes into docs-only workarounds.

---

## Release Gate

Do not consider OCPP communications reliable until Waves 1 through 4 are complete and validated.

Do not consider the OCPP surface honest until Waves 5 and 6 are complete and documentation is updated.

---

## Validation Sequence

After each wave:

- `go test ./...`
- `go vet ./...`
- `go fmt ./...`

After Waves 1 through 4:

- `go test -tags integration ./internal/ocpp/v201/ -v -timeout 60s`

After Wave 6:

- Full docs review for every OCPP route and advertised feature.
