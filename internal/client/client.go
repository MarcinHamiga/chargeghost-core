package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chargeghost/engine/internal/api"
)

// APIError reports a non-2xx response with the server's {success,message}
// envelope (internal/api.Response).
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API %d: %s", e.Status, e.Message)
}

// Client is a typed REST client over the ChargeGhost HTTP API.
type Client struct {
	baseURL string
	hc      *http.Client // reads
	mutHC   *http.Client // mutations (server ops can take up to 30s server-side)
}

// New creates a client for a base URL like "http://127.0.0.1:8080".
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		hc:      &http.Client{Timeout: 2 * time.Second},
		mutHC:   &http.Client{Timeout: 35 * time.Second},
	}
}

// BaseURL returns the client's base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// get performs a GET and decodes the body into out.
func (c *Client) get(path string, out any) error {
	return c.do(c.hc, http.MethodGet, path, nil, out)
}

// mutate performs a non-GET request and decodes the body into out.
func (c *Client) mutate(method, path string, body any, out any) error {
	return c.do(c.mutHC, method, path, body, out)
}

func (c *Client) do(hc *http.Client, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}

	req, err := http.NewRequest(method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &APIError{Status: resp.StatusCode, Message: resp.Status}
		var envelope struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Message != "" {
			apiErr.Message = envelope.Message
		}
		return apiErr
	}

	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// --- Fleet ---

// ListStations returns all station snapshots (GET /api/v1/stations).
func (c *Client) ListStations() ([]api.StationSnapshot, error) {
	var out []api.StationSnapshot
	err := c.get("/api/v1/stations", &out)
	return out, err
}

// FleetStatus returns the fleet status response (GET /api/v1/fleet/status).
func (c *Client) FleetStatus() (api.FleetStatusResponse, error) {
	var out api.FleetStatusResponse
	err := c.get("/api/v1/fleet/status", &out)
	return out, err
}

// FleetConfig returns the global config (GET /api/v1/fleet/config).
func (c *Client) FleetConfig() (api.FleetConfigResponse, error) {
	var out api.FleetConfigResponse
	err := c.get("/api/v1/fleet/config", &out)
	return out, err
}

// SaveFleetConfig persists the global config (POST /api/v1/fleet/config/save).
func (c *Client) SaveFleetConfig() (api.Response, error) {
	var out api.Response
	err := c.mutate(http.MethodPost, "/api/v1/fleet/config/save", nil, &out)
	return out, err
}

// ReloadFleet reloads stations from disk (POST /api/v1/fleet/reload).
func (c *Client) ReloadFleet() (api.Response, error) {
	var out api.Response
	err := c.mutate(http.MethodPost, "/api/v1/fleet/reload", nil, &out)
	return out, err
}

// Operations lists tracked fleet operations (GET /api/v1/fleet/operations).
func (c *Client) Operations() ([]api.Operation, error) {
	var out []api.Operation
	err := c.get("/api/v1/fleet/operations", &out)
	return out, err
}

// Operation fetches one tracked operation (GET /api/v1/fleet/operations/{id}).
func (c *Client) Operation(id string) (api.Operation, error) {
	var out api.Operation
	err := c.get("/api/v1/fleet/operations/"+url.PathEscape(id), &out)
	return out, err
}

// --- Station lifecycle ---

// StationStatus returns one station snapshot (GET /api/v1/stations/{id}/status).
func (c *Client) StationStatus(id string) (api.StationSnapshot, error) {
	var out api.StationSnapshot
	err := c.get("/api/v1/stations/"+url.PathEscape(id)+"/status", &out)
	return out, err
}

// CreateStation creates a new station (POST /api/v1/stations, 201).
func (c *Client) CreateStation(req api.CreateStationRequest) (api.OperationResponse, error) {
	var out api.OperationResponse
	err := c.mutate(http.MethodPost, "/api/v1/stations", req, &out)
	return out, err
}

// PatchStationConfig patches a station's config (PATCH /api/v1/stations/{id}/config).
func (c *Client) PatchStationConfig(id string, req api.PatchStationConfigRequest) (api.PatchStationResponse, error) {
	var out api.PatchStationResponse
	err := c.mutate(http.MethodPatch, "/api/v1/stations/"+url.PathEscape(id)+"/config", req, &out)
	return out, err
}

// StartStation requests a station start (202 + operation id).
func (c *Client) StartStation(id string) (api.OperationResponse, error) {
	return c.stationLifecycle(http.MethodPost, id, "start")
}

// StopStation requests a station stop (202 + operation id).
func (c *Client) StopStation(id string) (api.OperationResponse, error) {
	return c.stationLifecycle(http.MethodPost, id, "stop")
}

// RestartStation requests a station restart (202 + operation id).
func (c *Client) RestartStation(id string) (api.OperationResponse, error) {
	return c.stationLifecycle(http.MethodPost, id, "restart")
}

// EnableStation enables a station (202 + operation id).
func (c *Client) EnableStation(id string) (api.OperationResponse, error) {
	return c.stationLifecycle(http.MethodPost, id, "enable")
}

// DisableStation disables a station (202 + operation id).
func (c *Client) DisableStation(id string) (api.OperationResponse, error) {
	return c.stationLifecycle(http.MethodPost, id, "disable")
}

// ReconnectStation requests an OCPP reconnect (full restart; 202).
func (c *Client) ReconnectStation(id string) (api.OperationResponse, error) {
	return c.stationLifecycle(http.MethodPost, id, "ocpp/reconnect")
}

func (c *Client) stationLifecycle(method, id, action string) (api.OperationResponse, error) {
	var out api.OperationResponse
	err := c.mutate(method, "/api/v1/stations/"+url.PathEscape(id)+"/"+action, nil, &out)
	return out, err
}

// ReloadStation reloads one station from disk (200 envelope).
func (c *Client) ReloadStation(id string) (api.Response, error) {
	var out api.Response
	err := c.mutate(http.MethodPost, "/api/v1/stations/"+url.PathEscape(id)+"/reload", nil, &out)
	return out, err
}

// PersistStation persists one station's state (200 envelope).
func (c *Client) PersistStation(id string) (api.Response, error) {
	var out api.Response
	err := c.mutate(http.MethodPost, "/api/v1/stations/"+url.PathEscape(id)+"/persist", nil, &out)
	return out, err
}

// DeleteStationOptions carries DeleteStation's query-parameter options.
type DeleteStationOptions struct {
	Force         bool
	DeleteState   bool
	ClearPassword bool
	NewDefaultID  string
	AllowEmpty    bool
}

// DeleteStation deletes a station (DELETE /api/v1/stations/{id}).
func (c *Client) DeleteStation(id string, opts DeleteStationOptions) (api.Response, error) {
	q := url.Values{}
	if opts.Force {
		q.Set("force", "true")
	}
	if opts.DeleteState {
		q.Set("delete_state", "true")
	}
	if opts.ClearPassword {
		q.Set("clear_password", "true")
	}
	if opts.NewDefaultID != "" {
		q.Set("new_default_id", opts.NewDefaultID)
	}
	if opts.AllowEmpty {
		q.Set("allow_empty", "true")
	}
	path := "/api/v1/stations/" + url.PathEscape(id)
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var out api.Response
	err := c.mutate(http.MethodDelete, path, nil, &out)
	return out, err
}
