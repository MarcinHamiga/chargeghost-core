# ChargeGhost EVSE Simulation Engine — REST API Documentation

**Base URL:** `http://localhost:8080`  
**Content-Type:** `application/json`

---

## Standard Response Envelope

Most mutation endpoints (POST, PATCH, PUT, DELETE) return:
```json
{
  "success": boolean,
  "message": string,
  "details": any        // optional
}
```

## Common Status Codes

| Code | Meaning |
|------|---------|
| `200` | Success |
| `201` | Resource created |
| `204` | Success, no body |
| `400` | Bad request / invalid body or params |
| `403` | Action not allowed (e.g. read-only field) |
| `404` | Resource not found |
| `409` | Conflict (invalid state, active sessions, etc.) |
| `500` | Internal server error |
| `503` | OCPP service not connected |

---

## 1. Health & About

### `GET /health`
Liveness probe.
```json
{ "status": "ok" }
```

### `GET /api/about`
Service info.
```json
{
  "version": "0.5.0",
  "description": "ChargeGhost EVSE Simulator",
  "ocpp_versions": ["1.6J"],
  "features": ["..."],
  "license": "MIT",
  "copyright": "2025 ChargeGhost"
}
```

---

## 2. Status

### `GET /api/v1/status`
Full system snapshot.
```json
{
  "ocpp_connected": false,
  "uptime_seconds": 3600.5,
  "connectors": [
    {
      "id": 1,
      "status": "Charging",
      "voltage": 230.0,
      "current": 16.0,
      "phase": 1,
      "is_plugged_in": true,
      "id_tag": "RFID-001"
    }
  ],
  "active_sessions": [
    {
      "transaction_id": 1,
      "connector_id": 1,
      "energy_charged_wh": 12500.0,
      "state_of_charge": 85.5,
      "start_time": "2025-01-15T10:30:00Z",
      "id_tag": "RFID-001",
      "is_charging": true
    }
  ],
  "energy_meters": {
    "1": { "reading_wh": 2512500.0, "is_charging": true }
  }
}
```

---

## 3. Connectors

### `GET /api/v1/connectors`
List all connectors.
```json
[
  {
    "id": 1,
    "status": "Available",
    "voltage": 230.0,
    "current": 16.0,
    "phase": 1,
    "is_plugged_in": false,
    "id_tag": null
  }
]
```

### `POST /api/v1/connectors`
Create a connector.

**Request:**
```json
{
  "voltage": 230.0,   // required, 120–1000 V
  "current": 16.0,    // required, 6–150 A
  "phase": 1          // required, 1 or 3
}
```
**Response:** `201 Created` — standard envelope

### `GET /api/v1/connectors/{id}`
Get one connector. Returns connector object or `404`.

### `PUT /api/v1/connectors/{id}`
Update connector params.

**Request:** (all fields optional)
```json
{
  "voltage": 230.0,
  "current": 32.0,
  "phase": 3
}
```

### `DELETE /api/v1/connectors/{id}`
Delete a connector. `409` if in use.

---

### Connector State Actions

All return standard envelope. `400` on invalid ID, `409` on invalid state.

| Endpoint | Action |
|----------|--------|
| `POST /api/v1/connectors/{id}/plug_in` | Simulate EV plug-in |
| `POST /api/v1/connectors/{id}/unplug` | Simulate EV unplug |
| `POST /api/v1/connectors/{id}/suspend_ev` | Suspend charging (EV side) |
| `POST /api/v1/connectors/{id}/resume_charging` | Resume from suspension |
| `POST /api/v1/connectors/{id}/start-charging` | Start charging session |
| `POST /api/v1/connectors/{id}/stop-charging` | Stop charging session |

### `PUT /api/v1/connectors/{id}/rfid`
Set RFID tag. Query param: `?rfid_tag=RFID-001`

### `DELETE /api/v1/connectors/{id}/rfid`
Clear RFID tag.

---

## 4. Sessions

### `GET /api/v1/sessions`
List all active sessions (array of session objects).

**Session object:**
```json
{
  "transaction_id": 1,
  "connector_id": 1,
  "energy_charged_wh": 12500.0,
  "state_of_charge": 85.5,
  "start_time": "2025-01-15T10:30:00Z",
  "id_tag": "RFID-001",
  "is_charging": true
}
```

### `POST /api/v1/sessions/start`
Start a session.
```json
{
  "connector_id": 1,      // required
  "max_energy": 50000.0,  // required, Wh (0 = no limit)
  "id_tag": "RFID-001"    // optional
}
```

### `POST /api/v1/sessions/stop`
Stop all active sessions.

### `GET /api/v1/sessions/last-stopped`
Last stopped session info. `404` if none.
```json
{
  "transaction_id": 1,
  "connector_id": 1,
  "energy_charged_wh": 12500.0,
  "meter_stop": 2512500.0,
  "reason": "user_requested",
  "id_tag": "RFID-001"
}
```

### `GET /api/v1/sessions/active`
Query: `?connector_id=1`. Returns session object or `404`.

### `GET /api/v1/sessions/{connector_id}`
Session for a specific connector. `404` if no active session.

---

## 5. Configuration

### `GET /api/v1/config`
Get full config object.

### `PATCH /api/v1/config`
Update config. All fields optional.

**Request:**
```json
{
  "connection_url": "ws://ocpp-server:8080/cp/",
  "ocpp_id": "CP001",
  "ocpp_password": "password123",
  "charge_point_model": "SimulatorV1",
  "charge_point_vendor": "ChargeGhost",
  "skip_tls_verify": false,
  "log_mode": "debug",
  "multi_evse_mode": false,
  "ev_battery_capacity": 60000.0,
  "ocpp_version": "1.6J",
  "persist_message_queue": true,
  "rfid_tag": "RFID-DEFAULT"
}
```

**Response:**
```json
{
  "success": true,
  "action": "bridge_restart_required",
  "changed_fields": ["connection_url", "ocpp_id"],
  "message": "Configuration updated in memory. Bridge restart required."
}
```

`action` values: `"no-op"` | `"bridge_restart_required"` | `"runtime_rebuild_required"` | `"rejected"`

`409` if topology changes are rejected due to active sessions.

### `POST /api/v1/config/save`
Persist in-memory config to disk. `500` on failure.

---

## 6. Reservations

### `GET /api/v1/reservations`
```json
[
  {
    "reservation_id": 1,
    "connector_id": 1,
    "id_tag": "RFID-001",
    "expiry_date": "2025-01-16T10:30:00Z",
    "parent_id_tag": null
  }
]
```

### `POST /api/v1/reservations`
```json
{
  "connector_id": 1,                         // required
  "reservation_id": 1,                       // required
  "id_tag": "RFID-001",                      // required
  "expiry_date": "2025-01-16T10:30:00Z",    // required, RFC3339
  "parent_id_tag": null                      // optional
}
```
**Response:** `201 Created`

### `DELETE /api/v1/reservations/{reservation_id}`
Cancel reservation. `404` if not found.

---

## 7. Timeline (Event Log)

### `GET /api/v1/timeline`
**Query params** (all optional):

| Param | Type | Description |
|-------|------|-------------|
| `limit` | int | Max events (default 100) |
| `offset` | int | Pagination offset (default 0) |
| `source` | string | Filter by source |
| `direction` | string | Filter by direction |
| `event_type` | string | Filter by event type |
| `action` | string | Filter by action |
| `search` | string | Free-text search |
| `connector_id` | int | Filter by connector |
| `transaction_id` | int | Filter by transaction |

**Response:**
```json
{
  "events": [
    {
      "id": 1,
      "timestamp": "2025-01-15T10:30:00Z",
      "source": "engine",
      "event_type": "ChargeStarted",
      "connector_id": 1,
      "action": "SessionStarted",
      "details": {}
    }
  ],
  "total": 42
}
```

### `GET /api/v1/timeline/count`
```json
{ "count": 42 }
```

### `DELETE /api/v1/timeline`
Clear all events. `204 No Content`.

---

## 8. Local Authorization List

### `GET /api/v1/local-auth-list`
```json
{
  "version": 5,
  "entry_count": 3,
  "max_entries": 100,
  "enabled": true,
  "entries": [
    {
      "id_tag": "RFID-001",
      "authorization_status": "Accepted",
      "expiry_date": "2025-12-31T23:59:59Z",
      "is_expired": false
    }
  ]
}
```

### `GET /api/v1/local-auth-list/{id_tag}`
Single entry. `404` if not found.

### `PUT /api/v1/local-auth-list`
Replace/update entries.
```json
{
  "list_version": 6,           // required
  "update_type": "Full",       // required: "Full" or "Differential"
  "entries": [
    {
      "id_tag": "RFID-001",
      "status": "Accepted",
      "expiry": "2025-12-31T23:59:59Z"
    }
  ]
}
```
**Response:**
```json
{ "success": true, "message": "List updated to version 6", "version": 6, "count": 3 }
```

### `DELETE /api/v1/local-auth-list/{id_tag}`
Remove one entry. `204 No Content`.

### `DELETE /api/v1/local-auth-list`
Clear all entries.

---

## 9. Firmware

### `GET /api/v1/firmware/status`
```json
{
  "status": "Idle",
  "current_version": "1.2.3",
  "target_version": null,
  "progress": 0,
  "error": null
}
```
Status values: `Idle` | `Downloading` | `Downloaded` | `Installing` | `Installed` | `InstallationFailed`

### `POST /api/v1/firmware/trigger`
```json
{
  "location": "https://firmware-server.com/v1.2.4.bin",  // required
  "retrieve_date": "2025-01-20T10:00:00Z"                // required, RFC3339
}
```
`409` if update already in progress.

### `POST /api/v1/firmware/cancel`
`409` if no update in progress.

---

## 10. Diagnostics

### `GET /api/v1/diagnostics/status`
```json
{
  "status": "Idle",
  "progress": 0,
  "error": null
}
```
Status values: `Idle` | `Uploading` | `Uploaded` | `UploadFailed`

### `POST /api/v1/diagnostics/trigger`
```json
{
  "location": "https://server.com/upload",  // required
  "retries": 3,                             // required
  "retry_interval": 300                     // required, seconds
}
```

### `POST /api/v1/diagnostics/cancel`
No body required.

---

## 11. Charging Profiles

### `GET /api/v1/charging-profiles`
Array of profile objects.

### `GET /api/v1/charging-profiles/{profile_id}`
Query: `?connector_id=1` (optional). `404` if not found.

### `POST /api/v1/charging-profiles`
```json
{
  "connector_id": 1,
  "profile": {
    "profile_id": 2,
    "connector_id": 1,
    "purpose": "TxProfile",
    "stack_level": 1,
    "charging_profile_kind": "Absolute",
    "schedule_period": []
  }
}
```

### `DELETE /api/v1/charging-profiles`
**Query params** (all optional): `?profile_id=1&connector_id=1&purpose=TxProfile`

### `POST /api/v1/charging-profiles/composite-schedule`
```json
{
  "connector_id": 1,  // required
  "duration": 86400   // required, seconds
}
```
**Response:**
```json
{
  "periods": [
    { "start_period": 0, "limit": 16.0, "number_phases": 1 }
  ]
}
```

---

## 12. OCPP Control

### `GET /api/v1/ocpp/config-keys`
```json
[
  { "key": "HeartbeatInterval", "value": "60", "readonly": false, "supported": true }
]
```

### `PATCH /api/v1/ocpp/config-keys`
```json
{ "key": "HeartbeatInterval", "value": "120" }
```
`403` if read-only, `404` if key unsupported.

### `POST /api/v1/ocpp/authorize`
```json
{ "id_tag": "RFID-001" }
```

### `POST /api/v1/ocpp/heartbeat`
No body required.

### `POST /api/v1/ocpp/raw/status-notification`
```json
{ "connector_id": 1, "error_code": "NoError", "status": "Charging" }
```

### `POST /api/v1/ocpp/raw/meter-values`
```json
{ "connector_id": 1, "transaction_id": 0 }
```

### `POST /api/v1/ocpp/raw/data-transfer`
```json
{
  "vendor_id": "ChargeGhost",
  "message_id": "GetMetrics",
  "data": "{\"type\": \"metrics\"}"
}
```
**Response:** `{ "status": "Accepted", "data": "{}" }`

### `POST /api/v1/ocpp/raw/start-transaction`
Alias for connector `start-charging`.

### `POST /api/v1/ocpp/raw/stop-transaction`
Alias for connector `stop-charging`.

---

## 13. WebSocket

### `GET /ws`
Upgrade to WebSocket for real-time event streaming.

**On connect** — initial state snapshot:
```json
{ "type": "state_snapshot", "data": { ... } }
```

**Streaming event types:**

| Type | Description |
|------|-------------|
| `connector_status_changed` | Connector state transitioned |
| `session_started` | Charging session began |
| `session_stopped` | Charging session ended |
| `connector_params_changed` | Voltage/current/phase updated |
| `reservation_changed` | Reservation created or expired |
| `firmware_status_changed` | Firmware update state changed |
| `diagnostics_status_changed` | Diagnostics upload state changed |

A full state snapshot is also broadcast to all clients every 1 second.

---

## Connector Status Values

| Status | Description |
|--------|-------------|
| `Available` | Ready to accept EV |
| `Preparing` | EV plugged in, waiting to start |
| `Charging` | Active charging session |
| `SuspendedEV` | EV has paused charging |
| `SuspendedEVSE` | EVSE has paused charging |
| `Finishing` | Session ending |
| `Reserved` | Reserved for a specific idTag |
| `Unavailable` | Out of service |
| `Faulted` | Error state |
