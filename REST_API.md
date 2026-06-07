# ChargeGhost REST API Reference

ChargeGhost exposes a REST API on port **8080** (default) and a WebSocket endpoint for real-time event streaming. All API endpoints are prefixed with `/api/v1/` unless otherwise noted.

**Base URL:** `http://localhost:8080`

---

## Table of Contents

- [Response Format](#response-format)
- [Health](#health)
- [About](#about)
- [Status](#status)
- [Connectors](#connectors)
- [Sessions](#sessions)
- [Configuration](#configuration)
- [Reservations](#reservations)
- [Timeline](#timeline)
- [Local Authorization List](#local-authorization-list)
- [Firmware Updates](#firmware-updates)
- [Diagnostics](#diagnostics)
- [Charging Profiles](#charging-profiles)
- [OCPP Control](#ocpp-control)
- [WebSocket](#websocket)

---

## Response Format

Mutation endpoints return a standard response envelope:

```json
{
  "success": true,
  "message": "Connector created",
  "details": {}
}
```

| Field     | Type   | Description                                |
|-----------|--------|--------------------------------------------|
| `success` | bool   | Whether the operation succeeded            |
| `message` | string | Human-readable result description          |
| `details` | object | Optional additional data (omitted if null) |

### HTTP Status Codes

| Code | Meaning                                                                 |
|------|-------------------------------------------------------------------------|
| 200  | Successful read or mutation                                             |
| 201  | Resource created (connectors, reservations)                             |
| 202  | Accepted but deferred (e.g. availability change after active session)    |
| 204  | Successful deletion with no response body                               |
| 400  | Invalid input or missing required parameters                            |
| 403  | Read-only OCPP config key, or local authorization rejected (offline)   |
| 404  | Resource not found                                                      |
| 409  | Business logic conflict (e.g. active sessions prevent topology changes) |
| 503  | OCPP bridge not connected                                               |

### CORS

All origins are allowed. `OPTIONS` requests return `204 No Content`.

---

## Health

### `GET /health`

Liveness probe.

**Response:**

```json
{
  "status": "ok"
}
```

---

## About

### `GET /api/v1/about`

Returns engine version and feature list.

The payload is intentionally high-level. It advertises supported protocol versions and broad simulator capabilities only; version-specific unsupported OCPP operations are documented below.

**Response:**

```json
{
  "version": "0.5.0",
  "description": "ChargeGhost EVSE Simulator",
  "ocpp_versions": ["1.6J", "2.0.1"],
  "features": [
    "OCPP 1.6J and 2.0.1 charging station simulation",
    "Charging profile management and composite schedules",
    "Local authorization list",
    "Firmware and diagnostics simulation",
    "REST API and WebSocket event streaming",
    "Offline message queue with JSON persistence"
  ],
  "license": "AGPL-3.0",
  "copyright": "2026 Marcin Hamiga"
}
```

---

## Status

### `GET /api/v1/status`

Returns full engine status including all connectors, active sessions, and energy meters.

**Response:**

```json
{
  "ocpp_connected": true,
  "uptime_seconds": 3612.5,
  "connectors": [
    {
      "id": 1,
      "status": "Available",
      "voltage": 230.0,
      "current": 32.0,
      "phase": 3,
      "is_plugged_in": false,
      "id_tag": null
    }
  ],
  "active_sessions": [
    {
      "transaction_id": 1001,
      "connector_id": 1,
      "energy_charged_wh": 4500.0,
      "state_of_charge": 45.0,
      "start_time": "2025-04-09T12:00:00Z",
      "id_tag": "RFID001",
      "is_charging": true
    }
  ],
  "energy_meters": {
    "1": {
      "reading_wh": 12500.0,
      "is_charging": true
    }
  },
  "reservations": [
    {
      "reservation_id": 42,
      "connector_id": 1,
      "id_tag": "RFID001",
      "expiry_date": "2025-04-09T14:00:00Z",
      "parent_id_tag": null
    }
  ],
  "pending_remote_starts": [
    {
      "connector_id": 2,
      "transaction_id": -1,
      "id_tag": "RFID002",
      "expiry": "2025-04-09T12:35:30Z"
    }
  ]
}
```

| Field                   | Type   | Description                                            |
|-------------------------|--------|--------------------------------------------------------|
| `ocpp_connected`        | bool   | Whether the OCPP bridge is connected to CSMS           |
| `uptime_seconds`        | float  | Engine uptime in seconds                               |
| `connectors`            | array  | All connectors (see Connector object)                  |
| `active_sessions`       | array  | Currently active charging sessions (see Session object)|
| `energy_meters`         | object | Energy meters keyed by connector ID (as string)        |
| `reservations`          | array  | Active reservations                                    |
| `pending_remote_starts` | array  | Remote starts waiting for EV plug-in                   |

---

## Connectors

### Connector Object

| Field          | Type    | Description                           |
|----------------|---------|---------------------------------------|
| `id`           | int     | Connector ID                          |
| `status`       | string  | Current state (see Connector States)  |
| `voltage`      | float   | Voltage in volts                      |
| `current`      | float   | Current in amps                       |
| `phase`        | int     | Number of phases (1 or 3)             |
| `is_plugged_in`| bool    | Whether an EV is plugged in           |
| `id_tag`       | string? | RFID tag associated with connector    |

#### Connector States

```
Available, Preparing, Charging, SuspendedEV, SuspendedEVSE,
Finishing, Reserved, Unavailable, Faulted
```

#### Connector Validation Rules

- Voltage: 120 - 1000 V
- Current: 6 - 150 A
- Phase: 1 or 3

### `GET /api/v1/connectors`

List all connectors.

**Response:** `ConnectorObject[]`

### `POST /api/v1/connectors`

Create a new connector.

**Request Body:**

```json
{
  "voltage": 230.0,
  "current": 32.0,
  "phase": 3
}
```

**Response (201):**

```json
{
  "success": true,
  "message": "Connector created",
  "details": { "id": 1, "status": "Available", "voltage": 230.0, "current": 32.0, "phase": 3, "is_plugged_in": false, "id_tag": null }
}
```

### `GET /api/v1/connectors/{id}`

Get a single connector by ID.

**Response:** `ConnectorObject`

### `PUT /api/v1/connectors/{id}/availability`

Change connector availability.

**Request Body:**

```json
{
  "type": "Inoperative"
}
```

Allowed values: `"Operative"`, `"Inoperative"`.

**Response:** Standard response envelope. Returns **202 Accepted** when the change is scheduled until the active session ends (`message`: availability change scheduled after the active session ends).

### `PUT /api/v1/connectors/{id}`

Update connector parameters. All fields are optional (partial update).

**Request Body:**

```json
{
  "voltage": 400.0,
  "current": 63.0,
  "phase": 3
}
```

**Response:** Standard response envelope.

### `DELETE /api/v1/connectors/{id}`

Delete a connector.

**Response:** Standard response envelope. Returns `409` if connector has an active session.

### `POST /api/v1/connectors/{id}/plug_in`

Simulate plugging in an EV.

**Response:** Standard response envelope.

### `POST /api/v1/connectors/{id}/unplug`

Simulate unplugging an EV.

**Response:** Standard response envelope.

### `POST /api/v1/connectors/{id}/suspend_ev`

Suspend charging (EV-side suspension).

**Response:** Standard response envelope.

### `POST /api/v1/connectors/{id}/resume_charging`

Resume charging from EV suspension.

**Response:** Standard response envelope.

### `POST /api/v1/connectors/{id}/start-charging`

Begin a charging session on the connector.

**Query Parameters:**

| Parameter         | Type | Required | Description                                              |
|-------------------|------|----------|----------------------------------------------------------|
| `timeout_seconds` | int  | No       | Queue a pending start if the EV is not yet plugged in    |

Uses `config.rfid_tag` as the session `id_tag` (connector-level RFID is not read by this endpoint). Returns `403` when offline local authorization rejects that tag.

**Response:** Standard response envelope.

### `POST /api/v1/connectors/{id}/stop-charging`

Stop the active charging session on the connector.

**Response:** Standard response envelope. Returns `409` when no session is active on the connector.

### `PUT /api/v1/connectors/{id}/rfid`

Set an RFID tag on a connector.

**Query Parameters:**

| Parameter  | Type   | Required | Description     |
|------------|--------|----------|-----------------|
| `rfid_tag` | string | Yes      | The RFID tag ID |

**Response:** Standard response envelope.

### `DELETE /api/v1/connectors/{id}/rfid`

Clear the RFID tag from a connector.

**Response:** Standard response envelope.

---

## Sessions

### Session Object

| Field              | Type    | Description                                     |
|--------------------|---------|-------------------------------------------------|
| `transaction_id`   | int     | OCPP transaction identifier                     |
| `connector_id`     | int     | Connector this session belongs to               |
| `energy_charged_wh`| float   | Cumulative energy delivered in Wh               |
| `state_of_charge`  | float   | Battery percentage (0-100, 0 if capacity unset) |
| `start_time`       | string  | ISO 8601 timestamp                              |
| `id_tag`           | string? | RFID tag used to start the session              |
| `is_charging`      | bool    | Whether energy is actively being delivered       |

### Stopped Session Object

| Field              | Type    | Description                        |
|--------------------|---------|------------------------------------|
| `transaction_id`   | int     | OCPP transaction identifier        |
| `connector_id`     | int     | Connector ID                       |
| `energy_charged_wh`| float   | Total energy delivered in Wh       |
| `meter_stop`       | float   | Final meter reading in Wh          |
| `reason`           | string  | Stop reason                        |
| `id_tag`           | string? | RFID tag used for the session      |

### `GET /api/v1/sessions`

List all **active** sessions (same data as `GET /api/v1/sessions/info`).

**Response:** `SessionObject[]`

### `POST /api/v1/sessions/start`

Start a new charging session.

**Request Body:**

```json
{
  "connector_id": 1,
  "id_tag": "RFID001",
  "timeout_seconds": 30
}
```

| Field             | Type    | Required | Description                                           |
|-------------------|---------|----------|-------------------------------------------------------|
| `connector_id`    | int     | Yes      | Target connector                                      |
| `id_tag`          | string? | No       | RFID tag for authorization                            |
| `timeout_seconds` | int     | No       | Queue a pending start if the EV is not yet plugged in |

Session energy tracking and full-charge suspension always use the configured `ev_battery_capacity`.

If `id_tag` is omitted and `config.rfid_tag` is set, the configured default tag is used.

Returns `403` when offline local authorization rejects the resolved tag.

**Response:** Standard response envelope.

### `POST /api/v1/sessions/stop`

Stop all active sessions.

**Response:** Standard response envelope with `details.stopped_count`.

### `GET /api/v1/sessions/last-stopped`

Get information about the last stopped session.

**Response:** `StoppedSessionObject`

### `GET /api/v1/sessions/active`

Get the active session for a specific connector.

**Query Parameters:**

| Parameter      | Type | Required | Description  |
|----------------|------|----------|--------------|
| `connector_id` | int  | Yes      | Connector ID |

**Response:** `SessionObject`

### `GET /api/v1/sessions/info`

Get info for all active sessions.

**Response:** `SessionObject[]`

### `GET /api/v1/sessions/{connector_id}`

Get the session for a specific connector by path parameter.

**Response:** `SessionObject`

---

## Configuration

### `GET /api/v1/config`

Get the current configuration.

Sensitive credentials are redacted. `ocpp_password` is never returned by this endpoint.

**Response:** Configuration object with at least the fields below. Values reflect the in-memory config (including unsaved `PATCH` changes).

| Field               | Type     | Description                                              |
|---------------------|----------|----------------------------------------------------------|
| `connection_url`    | string   | CSMS WebSocket URL                                       |
| `ocpp_id`           | string   | Charge point identity                                    |
| `charge_point_model`| string   | Charge point model name                                  |
| `charge_point_vendor`| string  | Charge point vendor name                                 |
| `connectors`        | array    | Startup connector definitions (`voltage`, `current`, `phase`) |
| `security_profile`  | int      | OCPP transport security profile (0–2)                    |
| `skip_tls_verify`   | bool     | Skip TLS server certificate verification                 |
| `tls_ca_path`       | string?  | PEM CA bundle path                                       |
| `tls_client_cert_path` | string? | Client certificate path for mTLS                      |
| `tls_client_key_path`  | string? | Client private key path for mTLS                      |
| `log_mode`          | string   | Logging mode                                             |
| `multi_evse_mode`   | bool     | Multi-EVSE metering mode                                 |
| `ev_battery_capacity` | float  | EV battery capacity in **kWh** (SoC simulation)          |
| `ocpp_version`      | string   | `"1.6"` or `"2.0.1"`                                     |
| `persist_message_queue` | bool | Durable outbound message queue                           |
| `rfid_tag`          | string?  | Default RFID tag for session starts                      |
| `connector_type`    | string   | Connector type label for OCPP 2.0.1 device model (e.g. `"cType2"`) |
| `ignored_version`   | string?  | Optional version string ignored on upgrade               |

### `PATCH /api/v1/config`

Update configuration fields. All fields are optional.

Security Profile 2 uses `wss://` and TLS server verification. Profiles other than 2 may use
`ws://` or `wss://`. `tls_ca_path` points to a PEM CA bundle, `tls_client_cert_path` and
`tls_client_key_path` provide a client certificate pair for mTLS, and `skip_tls_verify`
disables server certificate verification for development or test use only.

**Request Body:**

```json
{
  "connection_url": "wss://csms.example.com/ocpp",
  "ocpp_id": "CP001",
  "ocpp_password": "secret",
  "security_profile": 2,
  "charge_point_model": "ChargeGhost",
  "charge_point_vendor": "ChargeGhost",
  "skip_tls_verify": false,
  "tls_ca_path": "/etc/ssl/certs/csms-ca.pem",
  "tls_client_cert_path": "/etc/ssl/certs/client.crt",
  "tls_client_key_path": "/etc/ssl/private/client.key",
  "log_mode": "debug",
  "multi_evse_mode": true,
  "ev_battery_capacity": 64.0,
  "ocpp_version": "1.6",
  "persist_message_queue": true,
  "rfid_tag": "DEFAULT_TAG"
}
```

| Field                  | Type    | Description                                        |
|------------------------|---------|----------------------------------------------------|
| `connection_url`       | string  | CSMS WebSocket URL                                 |
| `ocpp_id`              | string  | Charge point identity                              |
| `ocpp_password`        | string  | CSMS auth password (stored only in keyring)       |
| `security_profile`     | int     | OCPP transport security profile                    |
| `charge_point_model`   | string  | Charge point model name                            |
| `charge_point_vendor`  | string  | Charge point vendor name                           |
| `skip_tls_verify`      | bool    | Unsafe; skip TLS certificate verification          |
| `tls_ca_path`          | string  | PEM CA bundle path for server verification         |
| `tls_client_cert_path` | string  | Client certificate path for mTLS                   |
| `tls_client_key_path`  | string  | Client private key path for mTLS                   |
| `log_mode`             | string  | Logging mode                                       |
| `multi_evse_mode`      | bool    | Enable multi-EVSE mode                             |
| `ev_battery_capacity`  | float   | EV battery capacity in kWh; applied immediately to the engine (SoC) |
| `ocpp_version`         | string  | `"1.6"` or `"2.0.1"`                               |
| `persist_message_queue`| bool    | Enable durable message queue persistence           |
| `rfid_tag`             | string  | Default RFID tag                                   |

Changes to `ev_battery_capacity` and `rfid_tag` take effect immediately without a restart.

The following fields require a process restart (`action: "restart_required"`): `connection_url`, `ocpp_id`, `ocpp_password`, `security_profile`, `skip_tls_verify`, `tls_ca_path`, `tls_client_cert_path`, `tls_client_key_path`, `charge_point_model`, `charge_point_vendor`, `log_mode`, `multi_evse_mode`, `ocpp_version`, and `persist_message_queue`.

**Response:**

```json
{
  "success": true,
  "action": "restart_required",
  "changed_fields": ["connection_url", "ocpp_id"],
  "message": "Configuration updated in memory. Restart the process to apply startup-only changes."
}
```

| Action               | Meaning                                                      |
|----------------------|--------------------------------------------------------------|
| `"no-op"`           | No changes were made                                         |
| `"applied"`         | Changes are active immediately                               |
| `"restart_required"`| Changes are stored in memory; restart is needed for startup-only fields |

### `POST /api/v1/config/save`

Persist the current configuration to disk (`~/.chargeghost/config.json`).

`ocpp_password` is excluded from the saved file and remains in the system keyring.

**Response:** Standard response envelope.

---

## Reservations

### Reservation Object

| Field            | Type    | Description                         |
|------------------|---------|-------------------------------------|
| `reservation_id` | int     | Unique reservation identifier       |
| `connector_id`   | int     | Reserved connector                  |
| `id_tag`         | string  | RFID tag that owns the reservation  |
| `expiry_date`    | string  | Expiry timestamp (RFC 3339)         |
| `parent_id_tag`  | string? | Parent RFID tag (group reservation) |

### `GET /api/v1/reservations`

List all active reservations.

**Response:** `ReservationObject[]`

### `POST /api/v1/reservations`

Create a reservation.

**Request Body:**

```json
{
  "connector_id": 1,
  "reservation_id": 42,
  "id_tag": "RFID001",
  "expiry_date": "2025-04-09T14:00:00Z",
  "parent_id_tag": "GROUP001"
}
```

| Field            | Type    | Required | Description                |
|------------------|---------|----------|----------------------------|
| `connector_id`   | int     | Yes      | Connector to reserve       |
| `reservation_id` | int     | Yes      | Unique reservation ID      |
| `id_tag`         | string  | Yes      | RFID tag                   |
| `expiry_date`    | string  | Yes      | Expiry in RFC 3339 format  |
| `parent_id_tag`  | string  | No       | Parent RFID tag            |

**Response (201):** Standard response envelope.

### `DELETE /api/v1/reservations/{reservation_id}`

Cancel a reservation.

**Response:** Standard response envelope.

---

## Timeline

The timeline stores OCPP protocol events in a ring buffer (max 1000 entries).

### Timeline Event Object

| Field             | Type    | Description                                |
|-------------------|---------|--------------------------------------------|
| `event_id`        | string  | Unique event identifier                    |
| `timestamp`       | string  | ISO 8601 timestamp                         |
| `source`          | string  | `"ocpp_adapter"` or `"csms"`               |
| `direction`       | string  | `"inbound"` or `"outbound"`                |
| `event_type`      | string  | `"call"`, `"call_result"`, or `"call_error"` |
| `action`          | string  | OCPP action name (e.g. `"BootNotification"`) |
| `message_id`      | string  | OCPP message ID                            |
| `connector_id`    | int?    | Associated connector (if applicable)       |
| `transaction_id`  | int?    | Associated transaction (if applicable)     |
| `level`           | string  | `"info"`, `"warn"`, or `"error"`           |
| `summary`         | string  | Human-readable summary                     |
| `payload`         | object  | Raw OCPP message payload                   |
| `correlation_key` | string? | Correlates request/response pairs          |
| `tags`            | array   | String tags for categorization             |

### `GET /api/v1/timeline`

List timeline events with filtering and pagination.

**Query Parameters:**

| Parameter        | Type   | Default | Description                         |
|------------------|--------|---------|-------------------------------------|
| `limit`          | int    | 100     | Max events to return                |
| `offset`         | int    | 0       | Skip this many events               |
| `source`         | string |         | Filter by source                    |
| `direction`      | string |         | Filter by direction                 |
| `event_type`     | string |         | Filter by event type                |
| `action`         | string |         | Filter by OCPP action               |
| `search`         | string |         | Substring match on summary          |
| `connector_id`   | int    |         | Filter by connector ID              |
| `transaction_id` | int    |         | Filter by transaction ID            |

**Response:**

```json
{
  "events": [ /* TimelineEventObject[] */ ],
  "total": 250
}
```

### `GET /api/v1/timeline/count`

Get total event count.

**Response:**

```json
{
  "count": 250
}
```

### `DELETE /api/v1/timeline`

Clear all timeline events.

**Response:** `204 No Content`

---

## Local Authorization List

Manages OCPP local authorization entries for offline idTag validation.

### Local Auth Entry Object (list view)

Returned in `GET /api/v1/local-auth-list` under `entries`:

| Field                   | Type    | Description                                       |
|-------------------------|---------|---------------------------------------------------|
| `id_tag`                | string  | Authorization tag identifier                      |
| `authorization_status`  | string  | `"Accepted"`, `"Blocked"`, `"Expired"`, `"ConcurrentTx"`, etc. |
| `expiry_date`           | string? | Optional expiry timestamp (RFC 3339)              |
| `is_expired`            | bool    | Whether `expiry_date` is in the past                |

`GET /api/v1/local-auth-list/{id_tag}` returns the internal entry shape (`IDTag`, `Status`, `Expiry`, `ParentIDTag`, `Delete`) because it serializes `ocpp.LocalAuthEntry` directly.

### `GET /api/v1/local-auth-list`

Get the full local authorization list.

**Response:**

```json
{
  "version": 3,
  "entry_count": 12,
  "max_entries": 100,
  "enabled": true,
  "entries": [ /* LocalAuthEntryObject[] */ ]
}
```

### `GET /api/v1/local-auth-list/{id_tag}`

Get a specific authorization entry.

**Response:** `LocalAuthEntryObject`

### `PUT /api/v1/local-auth-list`

Update or replace the authorization list.

**Request Body:**

```json
{
  "list_version": 4,
  "entries": [
    {
      "IDTag": "RFID001",
      "Status": "Accepted",
      "Expiry": "2025-12-31T23:59:59Z",
      "ParentIDTag": "GROUP001"
    }
  ],
  "update_type": "Full"
}
```

| Field          | Type   | Description                              |
|----------------|--------|------------------------------------------|
| `list_version` | int    | New list version number                  |
| `entries`      | array  | `LocalAuthEntry` objects (Go field names) |
| `update_type`  | string | `"Full"` (replace all) or `"Differential"` (merge/update/delete via `Delete: true`) |

**Response:**

```json
{
  "success": true,
  "message": "List updated to version 4",
  "version": 4,
  "count": 12
}
```

### `DELETE /api/v1/local-auth-list/{id_tag}`

Delete a specific authorization entry.

**Response:** `204 No Content`

### `DELETE /api/v1/local-auth-list`

Clear all authorization entries.

**Response:** Standard response envelope.

---

## Firmware Updates

### `GET /api/v1/firmware/status`

Get current firmware update state.

**Response:** `FirmwareStatus` object (Go struct field names):

```json
{
  "Status": "Idle",
  "Location": null,
  "RetrieveDate": null,
  "FileName": null,
  "FileHash": null
}
```

Possible `Status` values: `Idle`, `Downloading`, `Downloaded`, `Installing`, `Installed`, `InstallationFailed`

Returns `409` if an update is already in progress when triggering.

### `POST /api/v1/firmware/trigger`

Trigger a firmware update simulation.

**Request Body:**

```json
{
  "location": "https://firmware.example.com/v2.0.bin",
  "retrieve_date": "2025-04-09T14:00:00Z"
}
```

| Field           | Type   | Required | Description                       |
|-----------------|--------|----------|-----------------------------------|
| `location`      | string | Yes      | Firmware download URL             |
| `retrieve_date` | string | Yes      | When to start (RFC 3339)          |

**Response:** Standard response envelope.

### `POST /api/v1/firmware/cancel`

Cancel an ongoing firmware update.

**Response:** Standard response envelope. Returns `409` if no update is in progress.

---

## Diagnostics

### `GET /api/v1/diagnostics/status`

Get current diagnostics upload state.

**Response:** `DiagnosticsStatus` object:

```json
{
  "Status": "Idle",
  "Location": null
}
```

Possible `Status` values: `Idle`, `Uploading`, `Uploaded`, `UploadFailed`

### `POST /api/v1/diagnostics/trigger`

Trigger a diagnostics upload simulation.

**Request Body:**

```json
{
  "location": "https://diag.example.com/upload",
  "retries": 3,
  "retry_interval": 30
}
```

| Field            | Type | Required | Description                        |
|------------------|------|----------|------------------------------------|
| `location`       | string | Yes    | Upload destination URL             |
| `retries`        | int    | No     | Number of upload retries           |
| `retry_interval` | int    | No     | Seconds between retries            |

Diagnostics uploads are still simulated, but retries and failure outcomes are now real.
To force deterministic failures for testing, add `chargeghost_failures=N` to the upload URL query string.
Example: `https://diag.example.com/upload?chargeghost_failures=2` fails two attempts before succeeding or returning `UploadFailed` if retries are exhausted.

**Response:** Standard response envelope.

### `POST /api/v1/diagnostics/cancel`

Cancel an ongoing diagnostics upload.

**Response:** Standard response envelope. Returns `409` if no upload is in progress.

---

## Charging Profiles

### `GET /api/v1/charging-profiles`

List all installed charging profiles.

**Response:** `ChargingProfile[]`

### `POST /api/v1/charging-profiles`

Install a charging profile.

**Request Body:**

```json
{
  "connector_id": 1,
  "profile": {
    "ProfileID": 10,
    "ConnectorID": 1,
    "StackLevel": 0,
    "Purpose": "TxDefaultProfile",
    "Kind": "Absolute",
    "Schedule": {
      "ChargingRateUnit": "W",
      "Periods": [
        { "StartPeriod": 0, "Limit": 7400.0 },
        { "StartPeriod": 3600, "Limit": 11000.0 }
      ]
    }
  }
}
```

The `profile` object uses `engine.ChargingProfile` field names (PascalCase). List/get responses use the same shape.

**Response:** Standard response envelope. Returns `409` on install conflict.

### `DELETE /api/v1/charging-profiles`

Clear charging profiles matching the given criteria.

**Query Parameters:**

| Parameter      | Type   | Description                                        |
|----------------|--------|----------------------------------------------------|
| `profile_id`   | int    | Delete a specific profile by ID                    |
| `connector_id` | int    | Delete all profiles on a connector                 |
| `purpose`      | string | Delete by purpose (e.g. `"TxDefaultProfile"`)      |

**Response:** Standard response envelope.

### `GET /api/v1/charging-profiles/{profile_id}`

Get a specific charging profile.

**Query Parameters:**

| Parameter      | Type | Description               |
|----------------|------|---------------------------|
| `connector_id` | int  | Connector to search within|

**Response:** `ChargingProfile`

### `POST /api/v1/charging-profiles/composite-schedule`

Calculate the composite charging schedule.

This endpoint computes the schedule from ChargeGhost's internal charging profile manager and matches the currently active protocol profile implementation. Inbound OCPP 2.0.1 `GetCompositeSchedule` from the CSMS is rejected; use this REST endpoint instead.

**Request Body:**

```json
{
  "connector_id": 1,
  "duration": 3600
}
```

| Field          | Type | Required | Description                       |
|----------------|------|----------|-----------------------------------|
| `connector_id` | int  | Yes      | Target connector                  |
| `duration`     | int  | Yes      | Schedule duration in seconds      |

**Response:**

```json
{
  "periods": [
    { "StartPeriod": 0, "Limit": 7400.0 },
    { "StartPeriod": 3600, "Limit": 11000.0 }
  ]
}
```

---

## OCPP Control

The OCPP REST surface is intentionally narrow. `status`, `authorize`, `heartbeat`, and `raw/*` are helper endpoints for observability, simulator control, and testing; they are not a generic transport for arbitrary OCPP operations.

### `GET /api/v1/ocpp/status`

Returns a link-health snapshot from the active OCPP bridge (`StatusTracker`). Same JSON shape for OCPP 1.6J and 2.0.1; use `version` to discriminate.

**Response (200):**

```json
{
  "version": "1.6",
  "connected": true,
  "connectedAt": "2025-04-09T12:00:00Z",
  "lastMessageAt": "2025-04-09T12:34:56Z",
  "reconnectCount": 2,
  "upSince": "2025-04-09T11:00:00Z",
  "csmsUrl": "wss://csms.example.com/ocpp/CP_1",
  "ocppId": "CP_1",
  "lastHeartbeatAt": "2025-04-09T12:34:00Z",
  "lastHeartbeatRttMs": 84,
  "heartbeatSuccesses": 17,
  "heartbeatFailures": 1
}
```

| Field                 | Type    | Description |
|-----------------------|---------|-------------|
| `version`             | string  | `"1.6"` or `"2.0.1"` |
| `connected`           | bool    | WebSocket link up |
| `connectedAt`         | string? | Last connect time (RFC 3339) |
| `disconnectedAt`      | string? | Last disconnect time |
| `lastMessageAt`       | string? | Last successful outbound message |
| `lastError`           | string? | Last error text (disconnect or send failure) |
| `lastErrorAt`         | string? | Timestamp of `lastError` |
| `reconnectCount`      | int     | Times the link was re-established after the first connect |
| `upSince`             | string  | Process/tracker start time (does not reset on reconnect) |
| `csmsUrl`             | string  | Configured CSMS URL |
| `ocppId`              | string  | Configured charge point ID |
| `lastHeartbeatAt`     | string? | Last heartbeat attempt time |
| `lastHeartbeatRttMs`  | int64?  | Last heartbeat round-trip time (ms) |
| `heartbeatSuccesses`  | int64   | Successful heartbeats |
| `heartbeatFailures`   | int64   | Failed heartbeats |
| `queueDepth`          | int?    | OCPP 2.0.1 offline queue depth |
| `queueExhausted`      | int?    | OCPP 2.0.1 messages that exhausted retries |
| `queueDropped`        | int?    | OCPP 2.0.1 messages moved to dead-letter storage |
| `drainInProgress`     | bool?   | OCPP 2.0.1 queue drain active |

Omitted optional fields use JSON `omitempty` (zero values are not sent).

**Response (503):** Standard envelope when the OCPP bridge is not configured (`success: false`, `message: "OCPP bridge is not configured"`).

### `GET /api/v1/ocpp/config-keys`

Get all OCPP configuration keys (1.6) or device-model variables (2.0.1) and their values.

**Response:** Array of config key entries:

```json
[
  {
    "key": "HeartbeatInterval",
    "value": "300",
    "readonly": false,
    "type": "int"
  }
]
```

### `PATCH /api/v1/ocpp/config-keys`

Update an OCPP configuration key.

**Request Body:**

```json
{
  "key": "MeterValueSampleInterval",
  "value": "30"
}
```

**Response:** Standard response envelope. Returns `403` for read-only keys.

### `POST /api/v1/ocpp/authorize`

Send an Authorize request to the CSMS.

**Request Body:**

```json
{
  "id_tag": "RFID001"
}
```

**Response:** Standard response envelope acknowledging that the request was sent. The CSMS authorization decision is logged asynchronously; it is not returned inline.

### `POST /api/v1/ocpp/heartbeat`

Send a Heartbeat to the CSMS.

**Response:** Standard response envelope.

### `POST /api/v1/ocpp/raw/status-notification`

Send a raw StatusNotification message.

**Request Body:**

```json
{
  "connector_id": 1,
  "error_code": "NoError",
  "status": "Available"
}
```

**Response:** Standard response envelope.

### `POST /api/v1/ocpp/raw/meter-values`

Send a raw MeterValues message.

**Request Body:**

```json
{
  "connector_id": 1,
  "transaction_id": 1001
}
```

**Response:** Standard response envelope.

### `POST /api/v1/ocpp/raw/data-transfer`

Send a raw DataTransfer message.

**Request Body:**

```json
{
  "vendor_id": "com.example",
  "message_id": "CustomMessage",
  "data": "{\"key\": \"value\"}"
}
```

**Response:**

```json
{
  "status": "Accepted",
  "data": "{\"result\": \"ok\"}"
}
```

### `POST /api/v1/ocpp/raw/start-transaction`

Send an OCPP 1.6-style `StartTransaction` helper message.

This endpoint exists for targeted outbound testing. It is not a generic OCPP 2.0.1 transaction API.

**Request Body:**

```json
{
  "connector_id": 1,
  "id_tag": "RFID001",
  "meter_start": 12500.0,
  "timestamp": "2025-04-09T12:00:00Z",
  "reservation_id": 42
}
```

| Field            | Type    | Required | Description |
|------------------|---------|----------|-------------|
| `connector_id`   | int     | Yes      | Target connector |
| `id_tag`         | string  | Yes      | Authorization tag |
| `meter_start`    | float   | No       | Defaults to current meter reading |
| `timestamp`      | string  | No       | RFC 3339; defaults to now |
| `reservation_id` | int     | No       | Reservation consumed by the transaction |

**Response:** Standard response envelope. `details.transaction_id` is set when the bridge returns an ID.

### `POST /api/v1/ocpp/raw/stop-transaction`

Send an OCPP 1.6-style `StopTransaction` helper message.

This endpoint exists for targeted outbound testing. It is not a generic OCPP 2.0.1 transaction API.

**Request Body:**

```json
{
  "transaction_id": 1001,
  "meter_stop": 15000.0,
  "timestamp": "2025-04-09T14:00:00Z",
  "reason": "Local"
}
```

| Field            | Type   | Required | Description |
|------------------|--------|----------|-------------|
| `transaction_id` | int    | Yes      | Active transaction ID |
| `reason`         | string | Yes      | OCPP stop reason (e.g. `"Local"`, `"Remote"`) |
| `meter_stop`     | float  | No       | Defaults to current meter reading |
| `timestamp`      | string | No       | RFC 3339; defaults to now |

**Response:** Standard response envelope. Returns `409` when no active session matches `transaction_id`.

### OCPP 2.0.1 Capability Notes

- Supported and validated: device variable get/set, reset, availability, authorization, remote start/stop, trigger message, charging profile install/clear/reporting/composite schedule, local authorization lists, reservations, firmware update, diagnostics `GetLog`, display messages, data transfer, and tariff cost updates.
- Explicitly unsupported today: `GetBaseReport`, `GetReport`, `NotifyEVChargingSchedule`, `NotifyEVChargingNeeds`, `SetVariableMonitoring`, `GetMonitoringReport`, `SetMonitoringBase`, `SetMonitoringLevel`, and `CustomerInformation`.

---

## WebSocket

### Connection

**URL:** `ws://localhost:8080/ws`

Connect via standard WebSocket upgrade. Upon connection, the server immediately sends a full state snapshot.

**Connection Parameters:**

| Parameter       | Value      |
|-----------------|------------|
| Ping interval   | 54 seconds |
| Read timeout    | 60 seconds |
| Write timeout   | 10 seconds |
| Read limit      | 512 bytes  |
| Broadcast buffer| 256 messages|

If a client's send buffer is full, the client is disconnected. If the hub broadcast queue is full, messages are dropped and a warning is logged server-side.

### Message Format

All WebSocket messages use this JSON envelope:

```json
{
  "type": "event_type",
  "timestamp": "2025-04-09T12:34:56Z",
  "data": {}
}
```

### Message Types

#### `state_snapshot`

Sent once immediately after connection. Contains full simulator state.

```json
{
  "type": "state_snapshot",
  "timestamp": "2025-04-09T12:34:56Z",
  "data": {
    "ocpp_connected": true,
    "uptime_seconds": 3612.5,
    "connectors": [
      {
        "id": 1,
        "status": "Available",
        "voltage": 230.0,
        "current": 32.0,
        "phase": 3,
        "is_plugged_in": false,
        "id_tag": null
      }
    ],
    "active_sessions": [
      {
        "transaction_id": 1001,
        "connector_id": 1,
        "energy_charged_wh": 4500.0,
        "state_of_charge": 45.0,
        "start_time": "2025-04-09T12:00:00Z",
        "id_tag": "RFID001",
        "is_charging": true
      }
    ],
    "energy_meters": {
      "1": {
        "reading_wh": 12500.0,
        "is_charging": true
      }
    },
    "reservations": [],
    "pending_remote_starts": []
  }
}
```

#### `tick`

Periodic full state snapshot broadcast (every **1 second**). The `data` payload matches `state_snapshot` (including `uptime_seconds`, `reservations`, and `pending_remote_starts`).

#### `connector_status_changed`

Fired when a connector transitions to a new state.

```json
{
  "type": "connector_status_changed",
  "timestamp": "2025-04-09T12:35:00Z",
  "data": {
    "connector_id": 1,
    "status": "Charging",
    "is_plugged_in": true
  }
}
```

#### `connector_plug_changed`

Fired when `is_plugged_in` changes, including when status does not change (e.g. plug-in while `Unavailable`).

```json
{
  "type": "connector_plug_changed",
  "data": {
    "connector_id": 1,
    "is_plugged_in": true
  }
}
```

#### `connector_id_tag_changed`

Fired when a connector's RFID tag is set or cleared (REST or internal updates).

```json
{
  "type": "connector_id_tag_changed",
  "data": {
    "connector_id": 1,
    "id_tag": "RFID001"
  }
}
```

#### `connector_params_changed`

Fired when a connector's electrical parameters are updated.

```json
{
  "type": "connector_params_changed",
  "timestamp": "2025-04-09T12:35:00Z",
  "data": {
    "connector_id": 1,
    "voltage": 400.0,
    "current": 63.0,
    "phase": 3
  }
}
```

#### `session_started`

Fired when a new charging session begins.

```json
{
  "type": "session_started",
  "timestamp": "2025-04-09T12:35:00Z",
  "data": {
    "connector_id": 1,
    "transaction_id": 1001,
    "id_tag": "RFID001",
    "meter_start": 12500.0,
    "reservation_id": 42
  }
}
```

`reservation_id` is included only when the session consumed a reservation.

#### `transaction_id_changed`

Fired when the CSMS assigns or updates the active transaction ID (e.g. after `StartTransaction` response).

```json
{
  "type": "transaction_id_changed",
  "data": {
    "connector_id": 1,
    "transaction_id": 1001
  }
}
```

#### `session_stopped`

Fired when a charging session ends. Includes transaction details when available.

```json
{
  "type": "session_stopped",
  "timestamp": "2025-04-09T12:35:00Z",
  "data": {
    "connector_id": 1,
    "transaction_id": 1001,
    "energy_charged_wh": 15000.0,
    "reason": "Local"
  }
}
```

If session info is not available, only `connector_id` is included.

#### `reservation_changed`

Fired when a reservation is created, cancelled, or expires. REST and OCPP paths both emit this event.

| `action`    | When |
|-------------|------|
| `created`   | `ReserveNow` or `POST /api/v1/reservations` accepted |
| `cancelled` | `CancelReservation` or `DELETE /api/v1/reservations/{id}` accepted |
| `expired`   | Reservation past `expiry_date` (simulation tick or plug-in) |

```json
{
  "type": "reservation_changed",
  "timestamp": "2025-04-09T12:35:00Z",
  "data": {
    "action": "expired",
    "reservation_id": 42,
    "connector_id": 1
  }
}
```

#### `connection_state_changed`

Fired when the OCPP WebSocket connects or disconnects.

```json
{
  "type": "connection_state_changed",
  "data": { "connected": true }
}
```

#### `ocpp_connected`

Fired on initial CSMS connection (in addition to `connection_state_changed`).

```json
{
  "type": "ocpp_connected",
  "data": { "url": "wss://csms.example.com/ocpp/CP_1" }
}
```

#### `ocpp_disconnected`

Fired when the CSMS link drops.

```json
{
  "type": "ocpp_disconnected",
  "data": { "reason": "websocket: close 1000 (normal)" }
}
```

#### `ocpp_reconnected`

Fired when the WebSocket reconnects after a prior disconnect.

```json
{
  "type": "ocpp_reconnected",
  "data": { "reconnectCount": 2 }
}
```

#### `ocpp_queue_overflow`

Fired when the serial OCPP command dispatcher drops a command because its buffer is full.

```json
{
  "type": "ocpp_queue_overflow",
  "data": {
    "description": "MeterValues",
    "queueDepth": 256,
    "queueCap": 256,
    "droppedTotal": 3
  }
}
```

#### `display_message_set` (OCPP 2.0.1)

Fired when the CSMS sets a display message.

```json
{
  "type": "display_message_set",
  "data": { "id": 1, "text": "Charging complete" }
}
```

#### `cost_updated` (OCPP 2.0.1)

Fired when the CSMS pushes tariff/cost data for a transaction.

```json
{
  "type": "cost_updated",
  "data": { "transaction_id": 1001, "total_cost": 12.50 }
}
```

#### `ocpp_config_key_changed`

Fired when the CSMS changes an OCPP 1.6 configuration key (accepted changes only).

```json
{
  "type": "ocpp_config_key_changed",
  "data": { "key": "HeartbeatInterval", "value": "60" }
}
```

#### `ocpp_variable_changed`

Fired when the CSMS changes an OCPP 2.0.1 variable (accepted changes only).

```json
{
  "type": "ocpp_variable_changed",
  "data": {
    "component": "OCPPCommCtrlr",
    "variable": "HeartbeatInterval",
    "value": "60",
    "evse_id": 0
  }
}
```

#### `charging_profile_changed`

Fired when a charging profile is installed or cleared via OCPP smart charging.

#### `firmware_status_changed`

Fired when the firmware update state changes.

```json
{
  "type": "firmware_status_changed",
  "timestamp": "2025-04-09T12:35:00Z",
  "data": {
    "status": "Downloading"
  }
}
```

#### `diagnostics_status_changed`

Fired when the diagnostics upload state changes.

```json
{
  "type": "diagnostics_status_changed",
  "timestamp": "2025-04-09T12:35:00Z",
  "data": {
    "status": "Uploading"
  }
}
```
