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
| 204  | Successful deletion with no response body                               |
| 400  | Invalid input or missing required parameters                            |
| 403  | Attempted write to a read-only config key                               |
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

**Response:**

```json
{
  "version": "0.5.0",
  "description": "ChargeGhost EVSE Simulator",
  "ocpp_versions": ["1.6J"],
  "features": [
    "OCPP 1.6J charging station simulation",
    "Smart charging profiles (TxDefaultProfile, TxProfile, ChargePointMaxProfile)",
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
  }
}
```

| Field             | Type   | Description                                            |
|-------------------|--------|--------------------------------------------------------|
| `ocpp_connected`  | bool   | Whether the OCPP bridge is connected to CSMS           |
| `uptime_seconds`  | float  | Engine uptime in seconds                               |
| `connectors`      | array  | All connectors (see Connector object)                  |
| `active_sessions` | array  | Currently active charging sessions (see Session object)|
| `energy_meters`   | object | Energy meters keyed by connector ID (as string)        |

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

**Response:** Standard response envelope.

### `POST /api/v1/connectors/{id}/stop-charging`

Stop the active charging session on the connector.

**Response:** Standard response envelope.

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

List all sessions.

**Response:** `SessionObject[]`

### `POST /api/v1/sessions/start`

Start a new charging session.

**Request Body:**

```json
{
  "connector_id": 1,
  "max_energy": 50000.0,
  "id_tag": "RFID001"
}
```

| Field          | Type    | Required | Description                                |
|----------------|---------|----------|--------------------------------------------|
| `connector_id` | int     | Yes      | Target connector                           |
| `max_energy`   | float   | No       | Max energy in Wh (0 = unlimited)           |
| `id_tag`       | string? | No       | RFID tag for authorization                 |

**Response:** Standard response envelope.

### `POST /api/v1/sessions/stop`

Stop all active sessions.

**Response:** Standard response envelope.

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

**Response:** Full configuration object (fields match `PatchConfigRequest` below, plus additional runtime fields).

### `PATCH /api/v1/config`

Update configuration fields. All fields are optional.

**Request Body:**

```json
{
  "connection_url": "ws://csms.example.com/ocpp",
  "ocpp_id": "CP001",
  "ocpp_password": "secret",
  "charge_point_model": "ChargeGhost",
  "charge_point_vendor": "ChargeGhost",
  "skip_tls_verify": false,
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
| `ocpp_password`        | string  | CSMS authentication password (stored in keyring)   |
| `charge_point_model`   | string  | Charge point model name                            |
| `charge_point_vendor`  | string  | Charge point vendor name                           |
| `skip_tls_verify`      | bool    | Skip TLS certificate verification                  |
| `log_mode`             | string  | Logging mode                                       |
| `multi_evse_mode`      | bool    | Enable multi-EVSE mode                             |
| `ev_battery_capacity`  | float   | EV battery capacity in kWh (for SoC calculation)   |
| `ocpp_version`         | string  | `"1.6"` or `"2.0.1"`                               |
| `persist_message_queue`| bool    | Enable durable message queue persistence           |
| `rfid_tag`             | string  | Default RFID tag                                   |

**Response:**

```json
{
  "success": true,
  "action": "bridge_restart_required",
  "changed_fields": ["connection_url", "ocpp_id"],
  "message": "Config updated; OCPP bridge restart required"
}
```

| Action                     | Meaning                                    |
|----------------------------|--------------------------------------------|
| `"no-op"`                  | No changes were made                       |
| `"bridge_restart_required"`| OCPP connection needs to be restarted      |
| `"runtime_rebuild_required"`| Full runtime rebuild needed               |
| `"rejected"`               | Active sessions prevent topology changes   |

### `POST /api/v1/config/save`

Persist the current configuration to disk (`~/.chargeghost/config.json`).

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

### Local Auth Entry Object

| Field          | Type    | Description                                       |
|----------------|---------|---------------------------------------------------|
| `id_tag`       | string  | Authorization tag identifier                      |
| `status`       | string  | Authorization status (`"Accepted"`, `"Blocked"`, etc.) |
| `expiry_date`  | string? | Optional expiry timestamp (RFC 3339)              |
| `parent_id_tag`| string? | Parent tag for group authorization                |

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
      "id_tag": "RFID001",
      "status": "Accepted",
      "expiry_date": "2025-12-31T23:59:59Z",
      "parent_id_tag": "GROUP001"
    }
  ],
  "update_type": "Full"
}
```

| Field          | Type   | Description                              |
|----------------|--------|------------------------------------------|
| `list_version` | int    | New list version number                  |
| `entries`      | array  | Authorization entries                    |
| `update_type`  | string | `"Full"` (replace all) or `"Differential"` (merge) |

**Response:**

```json
{
  "success": true,
  "message": "Local auth list updated",
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

**Response:**

```json
{
  "status": "Idle"
}
```

Possible statuses: `Idle`, `Downloading`, `Downloaded`, `Installing`, `Installed`, `InstallationFailed`

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

**Response:** Standard response envelope.

---

## Diagnostics

### `GET /api/v1/diagnostics/status`

Get current diagnostics upload state.

**Response:**

```json
{
  "status": "Idle"
}
```

Possible statuses: `Idle`, `Uploading`, `Uploaded`, `UploadFailed`

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

**Response:** Standard response envelope.

### `POST /api/v1/diagnostics/cancel`

Cancel an ongoing diagnostics upload.

**Response:** Standard response envelope.

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
    "chargingProfileId": 10,
    "stackLevel": 0,
    "chargingProfilePurpose": "TxDefaultProfile",
    "chargingProfileKind": "Absolute",
    "chargingSchedule": {
      "chargingRateUnit": "W",
      "chargingSchedulePeriod": [
        { "startPeriod": 0, "limit": 7400.0 },
        { "startPeriod": 3600, "limit": 11000.0 }
      ]
    }
  }
}
```

**Response:** Standard response envelope.

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
    { "startPeriod": 0, "limit": 7400.0 },
    { "startPeriod": 3600, "limit": 11000.0 }
  ]
}
```

---

## OCPP Control

### `GET /api/v1/ocpp/config-keys`

Get all OCPP configuration keys and their values.

**Response:** Configuration key information object.

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

**Response:** Standard response envelope (includes authorization result).

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

Send a raw StartTransaction message.

**Response:** Standard response envelope.

### `POST /api/v1/ocpp/raw/stop-transaction`

Send a raw StopTransaction message.

**Response:** Standard response envelope.

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

If a client's send buffer is full, the client is disconnected.

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
    }
  }
}
```

#### `tick`

Periodic full state snapshot broadcast (default: every 1 second). Identical payload structure to `state_snapshot`.

```json
{
  "type": "tick",
  "timestamp": "2025-04-09T12:34:57Z",
  "data": {
    "ocpp_connected": true,
    "connectors": [ /* ConnectorObject[] */ ],
    "active_sessions": [ /* SessionObject[] */ ],
    "energy_meters": { /* meter map */ }
  }
}
```

#### `connector_status_changed`

Fired when a connector transitions to a new state.

```json
{
  "type": "connector_status_changed",
  "timestamp": "2025-04-09T12:35:00Z",
  "data": {
    "connector_id": 1,
    "status": "Charging"
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
    "connector_id": 1
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

Fired when a reservation expires.

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
