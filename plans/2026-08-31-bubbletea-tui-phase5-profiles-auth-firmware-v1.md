# Plan: TUI Phase 5 — Profiles, Local Auth, Firmware/Diagnostics, Config Tabs

**Date:** 2026-08-31
**Version:** v1
**Parent plan:** `plans/2026-08-31-bubbletea-tui-v1.md`
**Depends on:** Phase 2
**Unblocks:** nothing (parallel with 3 and 4)

## Objective

Complete the management surface with the four remaining station tabs:
Charging Profiles, Local Auth, Firmware & Diagnostics (one tab, two
panes), and Config. After this phase every REST capability of the JS
frontend has a terminal equivalent.

## Design

### 1. Charging Profiles tab (`internal/tui/station/profiles.go`)

`GET …/charging-profiles` → table
`ID | StackLevel | Purpose | Kind | Duration | Active?`.

| Key | Action | REST call |
|---|---|---|
| enter | profile detail (full JSON viewport) | `GET …/charging-profiles/{id}` |
| i | install (JSON textarea form) | `POST …/charging-profiles` |
| c | clear (form: id optional, purpose select) | `DELETE …/charging-profiles` |
| s | composite schedule (form: duration, purpose select) | `POST …/charging-profiles/composite-schedule` |

Install is deliberately raw-JSON: profiles are complex, version-dependent
objects, and hand-building a full field-by-field form would be brittle
against both OCPP dialects. The textarea validates JSON syntactically
client-side, surfaces the handler's validation error verbatim on failure,
and offers "paste from file" via a path input (`form.Field` extension:
`filePathField` reading the file into the textarea). `charging_profile_changed`
events refetch the table.

Composite schedule result renders as a read-only viewport
(period→limit table when the payload allows, raw JSON otherwise).

### 2. Local Auth tab (`internal/tui/station/localauth.go`)

`GET …/local-auth-list` → table `IdTag | Status | Expiry | ParentIdTag`
(filterable like config keys).

| Key | Action | REST call |
|---|---|---|
| n | add/update entry (form) | `PUT …/local-auth-list` |
| enter | entry detail | `GET …/local-auth-list/{id_tag}` |
| D | delete entry (confirm) | `DELETE …/local-auth-list/{id_tag}` |
| C | clear all (type-to-confirm) | `DELETE …/local-auth-list` |

Add/update reuses `relativeTimeField` (phase 3) for expiry; status is a
select matching the DTO's enum.

### 3. Firmware & Diagnostics tab (`internal/tui/station/firmware.go`)

Two stacked panes (or 1/2 sub-tabs if crowding warrants — decide during
implementation):

```
Firmware  [status]  …  [t] trigger  [x] cancel
Diagnostics [status]  …  [t] trigger  [x] cancel
```

- `GET/POST …/firmware/status|trigger|cancel` — trigger form: location
  URL, (plus any DTO-supported fields: retries, interval — mirror
  `handlers.TriggerFirmwareUpdate`).
- `GET/POST …/diagnostics/status|trigger|cancel` — trigger form: upload
  location (+ retries/interval fields per handler DTO).
- `firmware_status_changed` / `diagnostics_status_changed` events refresh
  the panes; long-running uploads show elapsed time on the 1s tick.

### 4. Config tab (`internal/tui/station/configtab.go`)

- `GET …/stations/{id}/config` → rendered config with a sectioned layout
  (identity, OCPP, simulation, connectors).
- `e` → edit form for the commonly touched scalar fields only (ocpp_id,
  connection_url, ocpp version, log mode, multi-EVSE toggle, battery
  capacity, connector defaults) → `PATCH …/stations/{id}/config`.
  Unknown/complex fields stay JSON-only (viewport shows the full config;
  a raw-JSON textarea editor with the same paste-from-file affordance as
  profiles is the escape hatch).
- `s` → save (`POST …/config/save`) with confirm + "a reload applies
  runtime-affecting changes" hint (`R` from the fleet view or
  `POST …/stations/{id}/reload`).
- Credentials: `k` → set OCPP password (masked input), `K` → clear, `T` →
  test — `PUT/DELETE …/credentials/ocpp-password`, `POST …/credentials/test`.
  Masked input is a `form.Field` variant (`secretField`) reused nowhere
  else today but trivial.

### 5. Client additions

Charging-profile, local-auth, firmware, diagnostics, station-config, and
credentials method groups. DTO reuse from `internal/api` /
`internal/api/handlers` where exported.

## Tests

- Each tab with fake client: render, key→call mapping, confirm gating on
  destructive ops (clear-all, delete).
- Profile install: syntactic-JSON validation + handler error passthrough
  (httptest happy-path with a minimal valid profile).
- Config PATCH body: only edited fields present (partial patch semantics).
- secretField masking + relativeTimeField round-trip.

## Files Touched

- **New:** `internal/tui/station/profiles.go`, `localauth.go`,
  `firmware.go`, `configtab.go` (+ `_test.go` each)
- **Edit:** `internal/client/client.go` (+ tests): four method groups
- **Edit:** `internal/tui/form/field.go`: `filePathField`, `secretField`,
  JSON-textarea validation
- **Edit:** `internal/tui/station/station.go`: activate tabs

## Acceptance Criteria

- Profile install→list→detail→clear round-trips from the terminal; a
  composite schedule renders for a charging connector.
- Local auth add with `90m` expiry and status select round-trips; entry
  visible in list without manual refresh.
- Firmware trigger with a dummy URL transitions the status pane (event-
  driven), cancel returns to idle.
- Station config scalar edit + save + reload applies (e.g. changing
  meter sample interval takes effect); password set/clear/test operable
  with masked input.
- Tests green; build/vet/test clean; server mode untouched.

## Tasks

- [ ] Client: profile + local-auth method groups + tests
- [ ] Client: firmware + diagnostics + config/credentials groups + tests
- [ ] Profiles tab (install textarea + filePathField, clear form, composite view)
- [ ] Local Auth tab (filter, add/update, delete, clear-all)
- [ ] Firmware & Diagnostics tab (status panes, trigger/cancel forms)
- [ ] Config tab (sectioned view, scalar edit, raw-JSON escape hatch, credentials)
- [ ] form field additions (`filePathField`, `secretField`, JSON validation)
- [ ] Manual pass; full verification suite
