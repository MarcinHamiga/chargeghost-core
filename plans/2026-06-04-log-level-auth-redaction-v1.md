# Plan 8: Log Level Toggle & Auth Decision Logging (C6, C7, C13)

**Date:** 2026-06-04
**Version:** v1
**Source review:** 2026-06-04 OCPP communications review, recommendations C6, C7, C13
**Priority:** P2 — observability

## Objective

Allow runtime log-level changes (flag or env var), log the authorization
decision chain, and redact PII (idTags) in production logs.

## Background

`cmd/chargeghost/main.go` initializes `slog` with a fixed level
(slog.LevelInfo or default). Operators cannot increase verbosity without a
restart.

`internal/ocpp/local_session_admission.go` and `internal/ocpp/auth.go` return
an auth decision but do not log the chain: cache hit, local list hit, CSMS
response, fallback. Operators cannot explain "why was this idTag accepted
offline".

`idTag` values may be PII (RFID serial numbers, account identifiers) and
should be redacted in non-audit logs.

## Design

### 1. Log level flag

Add `-log-level` (string, default `info`, values `debug|info|warn|error`)
and `LOG_LEVEL` env var to `main.go`. Use a `slog.LevelVar` so the level can
be changed at runtime if a SIGHUP handler is added.

### 2. Auth decision chain

Modify `local_session_admission.go` to accept a logger (or return the chain
as a structured value). Each step is logged at `slog.Debug` (full chain) or
`slog.Info` (final decision only). Example:

```
slog.Debug("auth decision",
  "idTag", redact(idTag),
  "cache", "hit|miss|expired",
  "localList", "hit|miss|na",
  "csms", "reachable|unreachable|rejected",
  "decision", "Accepted|Blocked|Expired|Invalid")
```

### 3. PII redaction

Add `internal/ocpp/redact.go`:

```go
func RedactIDTag(s string) string {
    if len(s) <= 4 { return "***" }
    return s[:2] + "***" + s[len(s)-2:]
}
```

Use in all log calls. The full idTag is still available via the audit log
endpoint (deferred).

## Files Touched

- **Edit:** `cmd/chargeghost/main.go` (log level flag, env var)
- **Edit:** `internal/ocpp/local_session_admission.go` (decision chain log)
- **Edit:** `internal/ocpp/auth.go` (decision chain log)
- **Edit:** `internal/ocpp/auth_cache.go` (redact in logs)
- **New:** `internal/ocpp/redact.go` and `redact_test.go`
- **Edit:** various senders/handlers (redact idTags)

## Acceptance Criteria

- `-log-level debug` produces Debug-level output.
- `LOG_LEVEL=warn` produces only Warn/Error.
- Auth decision log includes cache/list/CSMS result chain.
- idTags are redacted in all log lines.

## Tasks

- [x] Add `-log-level` flag and `LOG_LEVEL` env var
- [x] Use `slog.LevelVar` for runtime changeability
- [x] Add `RedactIDTag` helper + tests
- [x] Log auth decision chain
- [x] Apply redaction in all OCPP log call sites (idTag/IdToken redaction in `local_session_admission.go`)
- [x] Tests
- [x] Run `go build ./...` and `go test ./...`
