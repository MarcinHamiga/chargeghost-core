# Security Profile 2 Implementation Plan

## Goal

Implement OCPP Security Profile 2 transport support: `wss://` with TLS server verification and optional client certificate authentication where required by the CSMS.

This plan covers transport-level TLS first. OCPP certificate lifecycle operations are follow-up work unless needed to unblock initial connectivity.

## Current State

- Security Profile 1 Basic Auth is implemented in both OCPP bridges.
- Both bridges currently create websocket clients with `ws.NewClient()`.
- `skip_tls_verify` exists in config and API, but is not wired into the websocket transport.
- There is no config/API surface for security profile selection, CA bundle path, client certificate path, or client key path.
- OCPP 2.0.1 certificate management handlers are currently explicit stubs returning unsupported/rejected responses.

## Scope

In scope:

- Add explicit Security Profile 2 configuration.
- Build TLS websocket clients with `ws.NewTLSClient(...)` when needed.
- Support custom CA trust.
- Support client certificate/key authentication for mTLS.
- Wire `skip_tls_verify` into TLS config for development/test scenarios.
- Add unit and bridge tests for TLS and mTLS connectivity.
- Update REST API and README docs.

Out of scope for first pass:

- OCPP 2.0.1 `InstallCertificate` persistence.
- OCPP 2.0.1 `DeleteCertificate` persistence.
- OCPP 2.0.1 `GetInstalledCertificateIds` real inventory.
- CSR generation.
- Certificate rotation.
- OCSP/CRL validation.

## Config Changes

Add fields to `internal/config/config.go`:

```go
SecurityProfile   int    `json:"security_profile"`
TLSCAPath         string `json:"tls_ca_path,omitempty"`
TLSClientCertPath string `json:"tls_client_cert_path,omitempty"`
TLSClientKeyPath  string `json:"tls_client_key_path,omitempty"`
```

Expected semantics:

- `security_profile = 0`: no transport auth requirement; normal `ws://` or `wss://`.
- `security_profile = 1`: Basic Auth using existing keyring/password flow.
- `security_profile = 2`: `wss://` with TLS server verification and optional client cert/key.
- `skip_tls_verify = true`: disables server certificate verification only for development/test; log a strong warning.

Defaults:

- Keep current behavior by defaulting `security_profile` to `1` only if an OCPP password exists, otherwise `0`.
- Alternatively default the field to `0` and let Basic Auth remain enabled opportunistically when a password exists. Choose the less surprising path during implementation.

## API Changes

Update `internal/api/dto.go` `PatchConfigRequest`:

```go
SecurityProfile   *int    `json:"security_profile"`
TLSCAPath         *string `json:"tls_ca_path"`
TLSClientCertPath *string `json:"tls_client_cert_path"`
TLSClientKeyPath  *string `json:"tls_client_key_path"`
```

Update `internal/api/handlers.go`:

- Apply the new fields in `PatchConfig`.
- Mark all four fields as restart-required.
- Keep credentials redaction behavior unchanged.

Update docs:

- `REST_API.md`
- `docs/REST_API.md`
- `README.md`

## Shared TLS Helper

Add `internal/ocpp/ws_client.go` or `internal/ocpp/tls.go` with a shared constructor:

```go
func NewWebSocketClient(cfg *config.Config) (*ws.Client, error)
```

Responsibilities:

- Parse `cfg.ConnectionURL`.
- If `security_profile == 2`, require `wss://`.
- If URL scheme is `wss`, build a TLS config even when security profile is not explicitly 2.
- If `TLSCAPath` is set, load PEM CA certificates into `RootCAs`.
- If either client cert or client key is set, require both and load with `tls.LoadX509KeyPair`.
- If `SkipTLSVerify` is true, set `InsecureSkipVerify: true` and log a warning.
- Use `ws.NewTLSClient(tlsConfig)` for TLS connections.
- Use `ws.NewClient()` for plain `ws://` connections.
- Apply Basic Auth when appropriate using the existing keyring/password lookup.

Suggested helper split:

```go
func NewWebSocketClient(cfg *config.Config) (*ws.Client, error)
func TLSConfigFromConfig(cfg *config.Config) (*tls.Config, error)
func applyBasicAuth(client *ws.Client, cfg *config.Config)
```

## Bridge Wiring

Update both bridge constructors:

- `internal/ocpp/v16/bridge.go`
- `internal/ocpp/v201/bridge.go`

Replace:

```go
wsClient := ws.NewClient()
```

with:

```go
wsClient, err := ocpp.NewWebSocketClient(cfg)
```

Because constructors currently do not return errors, choose one implementation approach:

- Preferred: add `NewBridge(... ) (*Bridge16, error)` and `NewBridge(... ) (*Bridge201, error)`, then update call sites and tests.
- Minimal alternative: keep constructors as-is, store a `startupErr error` on the bridge, and return it from `Start(ctx)` before connecting.

Prefer the explicit constructor error if the call-site churn is manageable.

## Validation Rules

Fail fast with actionable errors:

- Security Profile 2 requires `wss://`.
- `tls_client_cert_path` and `tls_client_key_path` must be provided together.
- Client certificate/key must parse successfully.
- CA file must exist and contain at least one valid certificate when configured.
- `security_profile` must be one of `0`, `1`, or `2`.

Log but allow:

- `skip_tls_verify = true` with Profile 2, with a clear warning.
- `wss://` with no custom CA path, using system trust.

## Tests

Add tests in `internal/ocpp`:

- Profile 2 rejects non-`wss://` URLs.
- `tls_ca_path` loads a PEM CA bundle.
- invalid CA path fails.
- client cert without key fails.
- client key without cert fails.
- valid client cert/key loads.
- `skip_tls_verify` maps to `tls.Config.InsecureSkipVerify`.
- Basic Auth is still applied for Profile 1.
- Basic Auth is not accidentally removed for existing configs that rely on password presence.

Add bridge-level TLS tests:

- OCPP 1.6 connects to a TLS websocket server with a custom trusted CA.
- OCPP 2.0.1 connects to a TLS websocket server with a custom trusted CA.
- OCPP 1.6 connects when the server requires a valid client certificate.
- OCPP 2.0.1 connects when the server requires a valid client certificate.
- Both versions fail when the server requires a client certificate and none is configured.

Use the existing upstream `ocpp-go/ws` TLS test patterns as reference for generating test certificates.

## Manual Smoke Test

Test Profile 2 against a real CSMS:

1. Configure `connection_url` with `wss://`.
2. Configure `security_profile = 2`.
3. Configure `tls_ca_path` if the CSMS uses a private CA.
4. Configure `tls_client_cert_path` and `tls_client_key_path` if the CSMS requires mTLS.
5. Start ChargeGhost.
6. Verify websocket connect succeeds.
7. Verify BootNotification succeeds.
8. Verify a start/stop transaction still works.

## Follow-Up Work

After transport-level Profile 2 is working, consider a second phase for certificate lifecycle management:

- Persist installed CSMS/root certificates.
- Implement OCPP 2.0.1 `InstallCertificate`.
- Implement OCPP 2.0.1 `DeleteCertificate`.
- Implement OCPP 2.0.1 `GetInstalledCertificateIds`.
- Add certificate status reporting and rotation workflows.
