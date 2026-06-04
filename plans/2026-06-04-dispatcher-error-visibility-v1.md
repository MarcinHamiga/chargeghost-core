# Plan 2: Dispatcher & Timeline Error Visibility (P0-3, P0-5)

**Date:** 2026-06-04
**Version:** v1
**Source review:** 2026-06-04 OCPP communications review, recommendations P0-3, P0-5
**Priority:** P0 — operational critical

## Objective

Make dispatcher overflow and command failures visible: rich context on drop,
write dispatcher errors to the timeline, and expose overflow counters.

## Background

`internal/ocpp/command.go:44-50` silently drops commands when its 256-slot
buffer is full. The single `slog.Warn` line is easy to miss and carries no
context. The `Run` loop in `command.go:35-37` only uses `slog.Error` — the
timeline logger never sees the failure, so the operator's message log shows
success for messages that were rejected or never delivered.

## Design

### 1. Track dispatcher metrics

Extend `CommandDispatcher` with:

- `dropped` `uint64` atomic counter
- `executed` `uint64` atomic counter
- `failed` `uint64` atomic counter
- `currentDepth` method returning `len(d.commands)`

Expose via new `Stats()` method returning a small struct.

### 2. Wire TimelineLogger into dispatcher

The dispatcher currently has no awareness of the timeline. Add a constructor
parameter or setter `SetTimelineLogger(tl *TimelineLogger)`. In `Run`, on
`cmd.Execute()` error, call `tl.LogError(action, "outbound", nil, err.Error(),
description)`.

### 3. Enrich drop log

`Enqueue`'s drop log becomes:

```
slog.Warn("OCPP command channel full, dropping",
  "description", cmd.Description,
  "queueDepth", len(d.commands),
  "queueCap", cap(d.commands),
  "droppedTotal", atomic.LoadUint64(&d.dropped))
```

## Files Touched

- **Edit:** `internal/ocpp/command.go`
- **Edit:** `internal/ocpp/command_test.go` (assert log content, counter)
- **Edit:** `cmd/chargeghost/main.go` (call `SetTimelineLogger` on dispatcher)

## Acceptance Criteria

- Drop log includes `queueDepth`, `queueCap`, `droppedTotal`.
- Dispatcher error path calls `tl.LogError` (verifiable via test).
- `Stats()` returns `{Depth, Cap, Dropped, Executed, Failed}`.
- All existing tests pass.

## Tasks

- [x] Add stats fields and `Stats()` to `CommandDispatcher`
- [x] Add `SetTimelineLogger` setter
- [x] Call `tl.LogError` in the `Run` error branch
- [x] Enrich `Enqueue` drop log
- [x] Wire `SetTimelineLogger` in `cmd/chargeghost/main.go`
- [x] Update tests to assert log content and counter increments
- [x] Run `go build ./...` and `go test ./...`
