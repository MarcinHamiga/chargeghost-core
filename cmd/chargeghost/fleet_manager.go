package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chargeghost/engine/internal/api"
	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

// FleetManager owns the global configuration and the runtime map for all
// stations. It is the single process-local authority for fleet mutations.
//
// Lock discipline:
//   - ms.opMu serializes lifecycle transitions (start/stop/replace) for one
//     station. It is acquired BEFORE fm.mu when both are needed, and is never
//     held while blocked on fm.mu.
//   - fm.mu guards fm.cfg, fm.stations map membership, fm.defaultID, and the
//     ms.Runtime/ms.Config/ms.buildErr fields. It is held only for short
//     critical sections — never across a StationRuntime.Stop wait or a
//     buildStationRuntime call, both of which can take seconds.
type FleetManager struct {
	mu        sync.RWMutex
	cfgPath   string
	cfg       *config.Config
	baseDir   string
	hub       *ws.Hub
	stations  map[string]*ManagedStation
	defaultID string
	ops       *OperationTracker
	// runCtx is the long-lived context under which every StationRuntime is
	// started. It must NOT be a per-request context — an HTTP handler's
	// context is cancelled when the handler returns, which would kill the
	// station's goroutines moments after a successful start.
	runCtx context.Context
}

// ManagedStation wraps a StationRuntime with its own lifecycle coordination.
// Runtime is nil when the station has never been built, has been stopped and
// not yet restarted, or failed to build (see buildErr).
type ManagedStation struct {
	Runtime  *StationRuntime
	Config   *config.EffectiveStation
	buildErr string
	opMu     sync.Mutex
}

// OperationTracker keeps a bounded history of async operations.
type OperationTracker struct {
	mu   sync.RWMutex
	ops  map[string]*api.Operation
	list []*api.Operation
	max  int
	seq  int64
	hub  *ws.Hub
}

// NewFleetManager loads the global config and prepares an empty fleet.
func NewFleetManager(cfgPath, baseDir string, hub *ws.Hub) (*FleetManager, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := cfg.ValidateStations(); err != nil {
		return nil, fmt.Errorf("validate stations: %w", err)
	}
	fm := &FleetManager{
		cfgPath:  cfgPath,
		cfg:      cfg,
		baseDir:  baseDir,
		hub:      hub,
		stations: make(map[string]*ManagedStation),
		ops:      newOperationTracker(100, hub),
		runCtx:   context.Background(),
	}
	fm.ensureDefaultIDLocked()
	effective, _ := cfg.EffectiveStationConfigs()
	for _, es := range effective {
		fm.stations[es.ID] = &ManagedStation{Config: es}
	}
	return fm, nil
}

// Config returns the current global configuration clone. Callers must not
// mutate the returned value directly; use FleetManager methods for mutations.
func (fm *FleetManager) Config() *config.Config {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	return fm.cfg.Clone()
}

// DefaultStationID returns the stable ID of the default station.
func (fm *FleetManager) DefaultStationID() string {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	return fm.defaultID
}

// AllStationIDs returns all configured station IDs, including disabled ones.
func (fm *FleetManager) AllStationIDs() []string {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	return fm.cfg.StationIDs()
}

// Hub returns the shared WebSocket hub.
func (fm *FleetManager) Hub() *ws.Hub {
	return fm.hub
}

// GetAppContext returns the API context for a station by stable ID. Returns
// false when the station is unknown or has no running runtime. The returned
// *api.AppContext is stable for the lifetime of the underlying StationRuntime
// (see StationRuntime.AppContext), so callers may use pointer identity to
// detect that a station has been replaced by a fresh runtime.
func (fm *FleetManager) GetAppContext(id string) (*api.AppContext, bool) {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	ms, ok := fm.stations[id]
	if !ok || ms.Runtime == nil {
		return nil, false
	}
	return ms.Runtime.AppContext(), true
}

func (fm *FleetManager) broadcast(msgType, stationID string, data any) {
	if fm.hub == nil {
		return
	}
	fm.hub.BroadcastMessage(ws.Message{
		Type:      msgType,
		StationID: stationID,
		Data:      data,
	})
}

// ensureDefaultIDLocked keeps the current default station ID if it still
// names a configured station; otherwise it falls back to the first
// configured ID. Unlike a blind "always pick the first ID" rebuild, this
// never overwrites an explicit, still-valid default (e.g. one just set by
// DeleteStation's NewDefaultID option) with an arbitrary choice — that used
// to clobber the caller's requested new default immediately after setting
// it. Must be called with fm.mu held. Propagates the change to the shared
// hub so default-scope WebSocket subscribers follow the new default
// immediately rather than staying pinned to a station that may no longer
// exist.
func (fm *FleetManager) ensureDefaultIDLocked() {
	ids := fm.cfg.StationIDs()
	valid := make(map[string]bool, len(ids))
	for _, id := range ids {
		valid[id] = true
	}
	next := fm.defaultID
	if next == "" || !valid[next] {
		if len(ids) > 0 {
			next = ids[0]
		} else {
			next = "default"
		}
	}
	if next != fm.defaultID {
		fm.defaultID = next
	}
	if fm.hub != nil {
		fm.hub.SetDefaultStationID(fm.defaultID)
	}
}

// wireStationLifecycleBroadcast registers a hook on sr so every lifecycle
// transition — including asynchronous ones such as a bridge goroutine
// failing mid-run — is broadcast over the WebSocket hub. Must be called
// before sr.Start so the "starting" transition itself is observed.
func (fm *FleetManager) wireStationLifecycleBroadcast(sr *StationRuntime) {
	sr.SetOnStateChange(func(state StationLifecycleState, errStr string) {
		fm.broadcast("station_lifecycle_changed", sr.ID, map[string]string{
			"state": string(state),
			"error": errStr,
		})
	})
}

// snapshotForLocked synthesizes a StationSnapshot for a station, using the
// live runtime's snapshot when one exists, or reflecting the last build
// error / enabled flag when it doesn't. Must be called with fm.mu held.
func snapshotForLocked(id string, ms *ManagedStation) api.StationSnapshot {
	if ms.Runtime != nil {
		return ms.Runtime.Snapshot()
	}
	state := StationConfigured
	if ms.buildErr != "" {
		state = StationFailed
	} else if ms.Config != nil && !ms.Config.Enabled {
		state = StationDisabled
	}
	snap := api.StationSnapshot{StationID: id, LifecycleState: string(state), LastError: ms.buildErr}
	if ms.Config != nil && ms.Config.Config != nil {
		snap.OCPPID = ms.Config.Config.OCPPID
		snap.Enabled = ms.Config.Enabled
		snap.OCPPVersion = ms.Config.Config.OCPPVersion
		snap.ConnectionURL = ms.Config.Config.ConnectionURL
	}
	return snap
}

// effectiveStationLocked recomputes the effective config for id from the
// current fm.cfg. Must be called with fm.mu held (read or write).
func (fm *FleetManager) effectiveStationLocked(id string, enabled bool) (*config.EffectiveStation, error) {
	es, err := fm.cfg.EffectiveStationConfig(id)
	if err != nil {
		return nil, err
	}
	return &config.EffectiveStation{ID: id, Enabled: enabled, Config: es}, nil
}

// stopRuntime stops ms.Runtime if present, for any live lifecycle state
// (including Failed — a failed bridge goroutine does not stop the sim loop,
// dispatcher, or persistence coordinator, which still need a proper
// shutdown). Bounded by ctx. Caller must hold ms.opMu and must NOT hold fm.mu.
func (fm *FleetManager) stopRuntime(ctx context.Context, ms *ManagedStation) error {
	fm.mu.RLock()
	sr := ms.Runtime
	fm.mu.RUnlock()
	if sr == nil {
		return nil
	}
	return sr.Stop(ctx)
}

// buildAndStartRuntime builds a fresh StationRuntime for ms from effective
// and starts it on fm.runCtx, assigning results into ms. Does not stop any
// existing runtime on ms — callers needing that must call stopRuntime first.
// Caller must hold ms.opMu and must NOT hold fm.mu.
func (fm *FleetManager) buildAndStartRuntime(ms *ManagedStation, effective *config.EffectiveStation) error {
	fm.mu.RLock()
	persistDir, queueDir := fm.dirsFor(effective)
	multiStation := fm.isMultiStation()
	legacyOCPPID := fm.cfg.OCPPID
	runCtx := fm.runCtx
	hub := fm.hub
	baseDir := fm.baseDir
	fm.mu.RUnlock()

	// One-time migration: if this station continues the identity of a
	// pre-fleet single-station deployment (same OCPP ID as the top-level
	// config, which never changes when stations are added), move its
	// legacy-location state into the new per-station directory before the
	// runtime loads from persistDir. dirsFor already resolved persistDir to
	// the multi-station path above, so this only fires once isMultiStation()
	// is true — a single-station setup already uses the legacy dir directly.
	if multiStation && effective.Config.OCPPID == legacyOCPPID {
		if migrated, err := migrateLegacySingleStationState(baseDir, persistDir); err != nil {
			slog.Warn("legacy single-station state migration failed; continuing with what could be moved", "station_id", effective.ID, "err", err)
		} else if migrated {
			slog.Info("migrated legacy single-station state", "station_id", effective.ID, "persist_dir", persistDir)
		}
	}

	sr, err := buildStationRuntime(effective.ID, effective.Config, hub, persistDir, queueDir)

	fm.mu.Lock()
	ms.Config = effective
	if err != nil {
		ms.Runtime = nil
		ms.buildErr = err.Error()
		fm.mu.Unlock()
		return err
	}
	sr.MultiStation = multiStation
	ms.Runtime = sr
	ms.buildErr = ""
	fm.mu.Unlock()

	fm.wireStationLifecycleBroadcast(sr)

	if err := sr.Start(runCtx); err != nil {
		fm.mu.Lock()
		ms.buildErr = err.Error()
		fm.mu.Unlock()
		return err
	}
	return nil
}

// startRuntimeFor recomputes id's effective config (as enabled) from the
// current global config and builds+starts a fresh runtime for ms. Caller
// must hold ms.opMu and must NOT hold fm.mu; any previous runtime on ms
// should already be stopped (this does not stop anything itself).
func (fm *FleetManager) startRuntimeFor(id string, ms *ManagedStation) error {
	fm.mu.RLock()
	effective, err := fm.effectiveStationLocked(id, true)
	fm.mu.RUnlock()
	if err != nil {
		fm.mu.Lock()
		ms.buildErr = err.Error()
		fm.mu.Unlock()
		return err
	}
	return fm.buildAndStartRuntime(ms, effective)
}

// replaceRuntime stops any existing runtime for station id (bounded by ctx)
// and, if that succeeds, builds+starts a fresh one from the current global
// config. If the stop times out, the error is returned and no build is
// attempted — building a second runtime while the first is still draining
// would leave two persistence coordinators writing the same directory.
// Looks up and locks the station's opMu itself; callers must not already
// hold it for the same station.
func (fm *FleetManager) replaceRuntime(ctx context.Context, id string) error {
	fm.mu.RLock()
	ms, ok := fm.stations[id]
	fm.mu.RUnlock()
	if !ok {
		return fmt.Errorf("station %s not found", id)
	}
	ms.opMu.Lock()
	defer ms.opMu.Unlock()
	if err := fm.stopRuntime(ctx, ms); err != nil {
		return fmt.Errorf("stop station %s: %w", id, err)
	}
	return fm.startRuntimeFor(id, ms)
}

// Start builds and starts all enabled stations. It should be called once
// after the process starts. A station whose runtime fails to build or start
// is recorded as failed (see ManagedStation.buildErr / StationFailed) rather
// than aborting startup of the rest of the fleet.
//
// ctx becomes fm.runCtx — the parent of every station's lifetime context —
// so it MUST be a context nothing external ever cancels before Shutdown is
// called (e.g. context.Background(), not a signal-handler's cancellable
// ctx). Every station's shutdown must go through Shutdown's orderly
// per-station Stop() calls, which save state before waiting for goroutines
// to drain; if ctx here is cancelled directly instead, station goroutines
// exit on their own and get marked Stopped before Stop() ever runs, so
// Shutdown's later Stop() call sees an already-Stopped runtime and
// short-circuits as a no-op — silently skipping SaveAll and losing every
// station's in-memory state on that "shutdown". See main.go's call site.
func (fm *FleetManager) Start(ctx context.Context) error {
	fm.mu.Lock()
	fm.runCtx = ctx
	effective, err := fm.cfg.EffectiveStationConfigs()
	if err != nil {
		fm.mu.Unlock()
		return err
	}
	if len(effective) == 0 {
		fm.mu.Unlock()
		return errors.New("no stations configured")
	}
	stations := make([]*ManagedStation, len(effective))
	for i, es := range effective {
		ms := &ManagedStation{Config: es}
		fm.stations[es.ID] = ms
		stations[i] = ms
	}
	fm.mu.Unlock()

	for i, es := range effective {
		if !es.Enabled {
			continue
		}
		ms := stations[i]
		ms.opMu.Lock()
		if err := fm.startRuntimeFor(es.ID, ms); err != nil {
			slog.Error("failed to start station", "station_id", es.ID, "err", err)
		}
		ms.opMu.Unlock()
	}
	return nil
}

func (fm *FleetManager) dirsFor(es *config.EffectiveStation) (persistDir, queueDir string) {
	if fm.isMultiStation() {
		persistDir = config.StationPersistDirByID(fm.baseDir, es.ID)
		queueDir = persistDir
	} else {
		persistDir = filepath.Join(fm.baseDir, "engine")
		queueDir = fm.baseDir
	}
	return
}

func (fm *FleetManager) isMultiStation() bool {
	return len(fm.cfg.Stations) > 1 || (len(fm.cfg.Stations) == 1 && fm.cfg.Stations[0].ID != nil)
}

type reconcileAction int

const (
	reconcileStop reconcileAction = iota
	reconcileStart
	reconcileRemove
)

type reconcilePlanItem struct {
	id     string
	ms     *ManagedStation
	action reconcileAction
}

// planReconcileLocked compares the current fm.cfg against fm.stations and
// returns the actions needed to converge, without performing any of them
// (newly-appearing stations are registered into fm.stations so the returned
// items' ms pointers are always valid). Must be called with fm.mu held.
func (fm *FleetManager) planReconcileLocked() ([]reconcilePlanItem, error) {
	effective, err := fm.cfg.EffectiveStationConfigs()
	if err != nil {
		return nil, err
	}
	newIDs := make(map[string]bool, len(effective))
	enabledByID := make(map[string]bool, len(effective))
	for _, es := range effective {
		newIDs[es.ID] = true
		enabledByID[es.ID] = es.Enabled
	}

	var plan []reconcilePlanItem
	for id, ms := range fm.stations {
		if !newIDs[id] {
			plan = append(plan, reconcilePlanItem{id: id, ms: ms, action: reconcileRemove})
			continue
		}
		if !enabledByID[id] && ms.Runtime != nil {
			plan = append(plan, reconcilePlanItem{id: id, ms: ms, action: reconcileStop})
		}
	}
	for _, es := range effective {
		ms, exists := fm.stations[es.ID]
		if !exists {
			ms = &ManagedStation{Config: es}
			fm.stations[es.ID] = ms
		} else {
			ms.Config = es
		}
		if !es.Enabled {
			continue
		}
		needsStart := ms.Runtime == nil
		if ms.Runtime != nil {
			state := ms.Runtime.LifecycleState()
			needsStart = state != StationRunning && state != StationStarting
		}
		if needsStart {
			plan = append(plan, reconcilePlanItem{id: es.ID, ms: ms, action: reconcileStart})
		}
	}
	fm.ensureDefaultIDLocked()
	return plan, nil
}

// executeReconcilePlan runs the plan without fm.mu held; each item is
// serialized against other lifecycle operations on the same station via
// ms.opMu, so a slow Stop on one station never blocks another station's
// reconcile step (or any concurrent REST request against it).
func (fm *FleetManager) executeReconcilePlan(ctx context.Context, plan []reconcilePlanItem) {
	for _, item := range plan {
		item.ms.opMu.Lock()
		switch item.action {
		case reconcileRemove, reconcileStop:
			stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := fm.stopRuntime(stopCtx, item.ms)
			cancel()
			if err != nil {
				slog.Error("reconcile: failed to stop station", "station_id", item.id, "err", err)
			}
			fm.mu.Lock()
			if item.action == reconcileStop && err == nil {
				item.ms.Runtime = nil
			}
			if item.action == reconcileRemove {
				delete(fm.stations, item.id)
			}
			fm.mu.Unlock()
		case reconcileStart:
			if err := fm.startRuntimeFor(item.id, item.ms); err != nil {
				slog.Error("reconcile: failed to start station", "station_id", item.id, "err", err)
			}
		}
		item.ms.opMu.Unlock()
	}
}

// CreateStation adds a new station to the global config, optionally persists it,
// and optionally starts it.
func (fm *FleetManager) CreateStation(ctx context.Context, req api.CreateStationRequest) (api.StationSnapshot, string, error) {
	op := fm.ops.Start("station.create", req.ID)
	fm.mu.Lock()

	if req.OCPPID == "" {
		fm.mu.Unlock()
		fm.ops.Fail(op.ID, "ocpp_id is required")
		return api.StationSnapshot{}, op.ID, errors.New("ocpp_id is required")
	}
	if req.ID == "" {
		req.ID = req.OCPPID
	}
	if _, _, found := fm.cfg.FindStation(req.ID); found {
		fm.mu.Unlock()
		fm.ops.Fail(op.ID, "station already exists")
		return api.StationSnapshot{}, op.ID, fmt.Errorf("station %s already exists", req.ID)
	}
	if len(fm.cfg.Stations) >= 8 {
		fm.mu.Unlock()
		fm.ops.Fail(op.ID, "too many stations")
		return api.StationSnapshot{}, op.ID, errors.New("too many stations: maximum is 8")
	}

	st := apiCreateStationRequestToStationConfig(req)
	if err := fm.cfg.UpsertStation(st); err != nil {
		fm.mu.Unlock()
		fm.ops.Fail(op.ID, err.Error())
		return api.StationSnapshot{}, op.ID, err
	}
	if req.OCPPPassword != "" {
		_ = config.SetPassword(req.OCPPID, req.OCPPPassword)
	}
	if req.Save {
		if err := fm.cfg.Save(fm.cfgPath); err != nil {
			fm.mu.Unlock()
			fm.ops.Fail(op.ID, err.Error())
			return api.StationSnapshot{}, op.ID, err
		}
	}

	es, err := fm.cfg.EffectiveStationConfig(req.ID)
	if err != nil {
		fm.mu.Unlock()
		fm.ops.Fail(op.ID, err.Error())
		return api.StationSnapshot{}, op.ID, err
	}
	effective := &config.EffectiveStation{ID: req.ID, Enabled: st.IsEnabled(), Config: es}
	ms := &ManagedStation{Config: effective}
	fm.stations[effective.ID] = ms
	fm.ensureDefaultIDLocked()
	fm.mu.Unlock()

	var startErr error
	if req.Start && effective.Enabled {
		ms.opMu.Lock()
		startErr = fm.startRuntimeFor(effective.ID, ms)
		ms.opMu.Unlock()
	}

	fm.mu.RLock()
	snapshot := snapshotForLocked(effective.ID, ms)
	fm.mu.RUnlock()

	if startErr != nil {
		fm.ops.Fail(op.ID, startErr.Error())
		return snapshot, op.ID, startErr
	}
	fm.ops.Succeed(op.ID)
	fm.broadcast("station_added", req.ID, snapshot)
	return snapshot, op.ID, nil
}

func apiCreateStationRequestToStationConfig(req api.CreateStationRequest) config.StationConfig {
	id := req.ID
	ocppID := req.OCPPID
	url := req.ConnectionURL
	version := req.OCPPVersion
	enabled := req.Enabled
	security := 0
	return config.StationConfig{
		ID:              &id,
		OCPPID:          &ocppID,
		ConnectionURL:   &url,
		OCPPVersion:     &version,
		Enabled:         &enabled,
		Connectors:      append([]config.ConnectorConfig(nil), req.Connectors...),
		SecurityProfile: &security,
	}
}

// UpdateStation patches a station's global config entry. Startup-only fields
// set restart_required; live fields apply to the running runtime when safe.
// The patch is applied to a clone and validated before being committed to
// fm.cfg — an invalid patch (e.g. one that introduces a duplicate OCPP ID)
// leaves the live config untouched instead of partially applying and then
// failing, which used to persist an invalid in-memory config that a later,
// unrelated Save() call would write to disk.
func (fm *FleetManager) UpdateStation(ctx context.Context, id string, req api.PatchStationConfigRequest) (api.StationUpdateResult, error) {
	op := fm.ops.Start("station.update", id)
	fm.mu.Lock()

	next := fm.cfg.Clone()
	legacyTopLevel := len(next.Stations) == 0 && id == next.OCPPID

	var changed []string
	var restartRequired bool
	if legacyTopLevel {
		changed, restartRequired = applyPatchToTopLevel(next, req)
	} else {
		sc, _, found := next.FindStation(id)
		if !found {
			fm.mu.Unlock()
			fm.ops.Fail(op.ID, "station not found")
			return api.StationUpdateResult{}, fmt.Errorf("station %s not found", id)
		}
		changed, restartRequired = applyPatchToStation(sc, req)
		if req.Enabled != nil {
			sc.Enabled = req.Enabled
			changed = append(changed, "enabled")
		}
	}

	if err := next.ValidateStations(); err != nil {
		fm.mu.Unlock()
		fm.ops.Fail(op.ID, err.Error())
		return api.StationUpdateResult{}, err
	}

	// Commit — every step from here on operates on the validated config.
	fm.cfg = next

	ocppPasswordID := id
	if legacyTopLevel {
		ocppPasswordID = next.OCPPID
	} else if sc, _, found := next.FindStation(id); found && sc.OCPPID != nil {
		ocppPasswordID = *sc.OCPPID
	}
	if req.OCPPPassword != nil {
		// Empty string means "clear the stored password". Either way the
		// running bridge keeps the auth header it was built with, so the
		// change only takes effect after a restart — report that instead of
		// letting the caller believe the new credentials are already live.
		var err error
		if *req.OCPPPassword == "" {
			err = config.DeletePassword(ocppPasswordID)
		} else {
			err = config.SetPassword(ocppPasswordID, *req.OCPPPassword)
		}
		if err != nil {
			fm.mu.Unlock()
			fm.ops.Fail(op.ID, err.Error())
			return api.StationUpdateResult{}, err
		}
		changed = append(changed, "ocpp_password")
		restartRequired = true
	}

	if req.Save {
		if err := fm.cfg.Save(fm.cfgPath); err != nil {
			fm.mu.Unlock()
			fm.ops.Fail(op.ID, err.Error())
			return api.StationUpdateResult{}, err
		}
	}

	enabledAfter := true
	if !legacyTopLevel {
		if sc, _, found := fm.cfg.FindStation(id); found {
			enabledAfter = sc.IsEnabled()
		}
	}

	ms := fm.stations[id]
	if ms == nil {
		ms = &ManagedStation{}
		fm.stations[id] = ms
	}
	if effective, err := fm.effectiveStationLocked(id, enabledAfter); err == nil {
		ms.Config = effective
	}
	if ms.Runtime != nil && req.EVBatteryCapacity != nil {
		ms.Runtime.Engine.SetEVBatteryCapacity(*req.EVBatteryCapacity * 1000)
	}
	fm.mu.Unlock()

	restarted := false
	if req.Restart && enabledAfter {
		if err := fm.replaceRuntime(ctx, id); err != nil {
			fm.ops.Fail(op.ID, err.Error())
			return api.StationUpdateResult{}, err
		}
		restarted = true
	}

	fm.ops.Succeed(op.ID)
	snapshot, _ := fm.Snapshot(id)
	snapshot.RestartRequired = restartRequired
	fm.broadcast("station_config_changed", id, snapshot)
	if restartRequired && !restarted {
		fm.broadcast("station_restart_required_changed", id, map[string]bool{"restart_required": true})
	}
	return api.StationUpdateResult{
		Snapshot:        snapshot,
		ChangedFields:   changed,
		RestartRequired: restartRequired,
		Restarted:       restarted,
		OperationID:     op.ID,
	}, nil
}

// applyPatchToTopLevel applies a PatchStationConfigRequest directly to the
// top-level Config, for legacy single-station setups (no stations array) —
// there is no per-station StationConfig entry to patch, so a PATCH targeting
// the default station's ID writes straight to the global config fields.
func applyPatchToTopLevel(cfg *config.Config, req api.PatchStationConfigRequest) (changed []string, restartRequired bool) {
	restartFields := map[string]bool{
		"charge_point_model":    true,
		"charge_point_vendor":   true,
		"connection_url":        true,
		"log_mode":              true,
		"multi_evse_mode":       true,
		"ocpp_id":               true,
		"ocpp_password":         true,
		"ocpp_version":          true,
		"persist_message_queue": true,
		"security_profile":      true,
		"skip_tls_verify":       true,
		"tls_ca_path":           true,
		"tls_client_cert_path":  true,
		"tls_client_key_path":   true,
		"connector_type":        true,
		"ignored_version":       true,
	}
	mark := func(field string) {
		changed = append(changed, field)
		if restartFields[field] {
			restartRequired = true
		}
	}
	if req.ConnectionURL != nil {
		cfg.ConnectionURL = *req.ConnectionURL
		mark("connection_url")
	}
	if req.OCPPID != nil {
		cfg.OCPPID = *req.OCPPID
		mark("ocpp_id")
	}
	if req.ChargePointModel != nil {
		cfg.ChargePointModel = *req.ChargePointModel
		mark("charge_point_model")
	}
	if req.ChargePointVendor != nil {
		cfg.ChargePointVendor = *req.ChargePointVendor
		mark("charge_point_vendor")
	}
	if req.SecurityProfile != nil {
		cfg.SecurityProfile = *req.SecurityProfile
		mark("security_profile")
	}
	if req.SkipTLSVerify != nil {
		cfg.SkipTLSVerify = *req.SkipTLSVerify
		mark("skip_tls_verify")
	}
	if req.TLSCAPath != nil {
		cfg.TLSCAPath = *req.TLSCAPath
		mark("tls_ca_path")
	}
	if req.TLSClientCertPath != nil {
		cfg.TLSClientCertPath = *req.TLSClientCertPath
		mark("tls_client_cert_path")
	}
	if req.TLSClientKeyPath != nil {
		cfg.TLSClientKeyPath = *req.TLSClientKeyPath
		mark("tls_client_key_path")
	}
	if req.LogMode != nil {
		cfg.LogMode = *req.LogMode
		mark("log_mode")
	}
	if req.MultiEVSEMode != nil {
		cfg.MultiEVSEMode = *req.MultiEVSEMode
		mark("multi_evse_mode")
	}
	if req.EVBatteryCapacity != nil {
		cfg.EVBatteryCapacity = *req.EVBatteryCapacity
		mark("ev_battery_capacity")
	}
	if req.OCPPVersion != nil {
		cfg.OCPPVersion = *req.OCPPVersion
		mark("ocpp_version")
	}
	if req.PersistMessageQueue != nil {
		cfg.PersistMessageQueue = *req.PersistMessageQueue
		mark("persist_message_queue")
	}
	if req.RFIDTag != nil {
		cfg.RFIDTag = req.RFIDTag
		mark("rfid_tag")
	}
	if req.ConnectorType != nil {
		cfg.ConnectorType = *req.ConnectorType
		mark("connector_type")
	}
	if req.IgnoredVersion != nil {
		cfg.IgnoredVersion = req.IgnoredVersion
		mark("ignored_version")
	}
	return
}

func applyPatchToStation(sc *config.StationConfig, req api.PatchStationConfigRequest) (changed []string, restartRequired bool) {
	restartFields := map[string]bool{
		"charge_point_model":    true,
		"charge_point_vendor":   true,
		"connection_url":        true,
		"multi_evse_mode":       true,
		"ocpp_id":               true,
		"ocpp_password":         true,
		"ocpp_version":          true,
		"persist_message_queue": true,
		"security_profile":      true,
		"skip_tls_verify":       true,
		"tls_ca_path":           true,
		"tls_client_cert_path":  true,
		"tls_client_key_path":   true,
		"connector_type":        true,
	}
	mark := func(field string) {
		changed = append(changed, field)
		if restartFields[field] {
			restartRequired = true
		}
	}
	if req.ConnectionURL != nil {
		sc.ConnectionURL = req.ConnectionURL
		mark("connection_url")
	}
	if req.OCPPID != nil {
		sc.OCPPID = req.OCPPID
		mark("ocpp_id")
	}
	if req.ChargePointModel != nil {
		sc.ChargePointModel = req.ChargePointModel
		mark("charge_point_model")
	}
	if req.ChargePointVendor != nil {
		sc.ChargePointVendor = req.ChargePointVendor
		mark("charge_point_vendor")
	}
	if req.SecurityProfile != nil {
		sc.SecurityProfile = req.SecurityProfile
		mark("security_profile")
	}
	if req.SkipTLSVerify != nil {
		sc.SkipTLSVerify = req.SkipTLSVerify
		mark("skip_tls_verify")
	}
	if req.TLSCAPath != nil {
		sc.TLSCAPath = req.TLSCAPath
		mark("tls_ca_path")
	}
	if req.TLSClientCertPath != nil {
		sc.TLSClientCertPath = req.TLSClientCertPath
		mark("tls_client_cert_path")
	}
	if req.TLSClientKeyPath != nil {
		sc.TLSClientKeyPath = req.TLSClientKeyPath
		mark("tls_client_key_path")
	}
	if req.MultiEVSEMode != nil {
		sc.MultiEVSEMode = req.MultiEVSEMode
		mark("multi_evse_mode")
	}
	if req.EVBatteryCapacity != nil {
		sc.EVBatteryCapacity = req.EVBatteryCapacity
		mark("ev_battery_capacity")
	}
	if req.OCPPVersion != nil {
		sc.OCPPVersion = req.OCPPVersion
		mark("ocpp_version")
	}
	if req.PersistMessageQueue != nil {
		sc.PersistMessageQueue = req.PersistMessageQueue
		mark("persist_message_queue")
	}
	if req.RFIDTag != nil {
		sc.RFIDTag = req.RFIDTag
		mark("rfid_tag")
	}
	if req.ConnectorType != nil {
		sc.ConnectorType = req.ConnectorType
		mark("connector_type")
	}
	return
}

// DeleteStation removes a station from the global config. It stops the runtime
// first if force is true.
// DeleteStation removes a station from the global config, stopping its
// runtime first if one exists. Force is only required to stop a Running
// station (the one case where something is actually serving traffic);
// Starting/Stopping/Failed runtimes are stopped unconditionally — they
// aren't doing useful work, but their goroutines (sim loop, dispatcher,
// persistence coordinator) are still live and must be drained, or deleting
// the station would leak them.
func (fm *FleetManager) DeleteStation(ctx context.Context, id string, opts api.DeleteStationOptions) error {
	fm.mu.Lock()
	_, _, found := fm.cfg.FindStation(id)
	if !found {
		fm.mu.Unlock()
		return fmt.Errorf("station %s not found", id)
	}
	ms := fm.stations[id]
	var state StationLifecycleState
	if ms != nil && ms.Runtime != nil {
		state = ms.Runtime.LifecycleState()
	}
	running := state == StationRunning
	needsStop := ms != nil && ms.Runtime != nil && state != StationStopped
	if running && !opts.Force {
		fm.mu.Unlock()
		return errors.New("station is running; use force=true to stop first")
	}
	if !opts.AllowEmpty {
		enabledCount := 0
		for _, sc := range fm.cfg.Stations {
			if sc.IsEnabled() && sc.StationID() != id {
				enabledCount++
			}
		}
		if enabledCount == 0 {
			fm.mu.Unlock()
			return errors.New("cannot delete the last enabled station; use allow_empty=true")
		}
	}
	fm.mu.Unlock()

	if needsStop {
		ms.opMu.Lock()
		stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = fm.stopRuntime(stopCtx, ms)
		cancel()
		ms.opMu.Unlock()
	}

	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Computed before RemoveStation mutates fm.cfg.Stations — dirsFor's
	// multi-station determination must match what was used when this
	// station's persist dir was originally chosen.
	var deleteDir string
	if opts.DeleteState {
		if ms != nil && ms.Runtime != nil {
			deleteDir = ms.Runtime.PersistDir
		} else if ms != nil && ms.Config != nil {
			deleteDir, _ = fm.dirsFor(ms.Config)
		}
	}

	if err := fm.cfg.RemoveStation(id); err != nil {
		return err
	}
	if deleteDir != "" {
		_ = os.RemoveAll(deleteDir)
	}
	if opts.ClearPassword {
		ocppID := id
		if ms != nil && ms.Config != nil && ms.Config.Config != nil && ms.Config.Config.OCPPID != "" {
			ocppID = ms.Config.Config.OCPPID
		}
		_ = config.DeletePassword(ocppID)
	}
	delete(fm.stations, id)
	if fm.defaultID == id && opts.NewDefaultID != "" {
		fm.defaultID = opts.NewDefaultID
	}
	fm.ensureDefaultIDLocked()
	if err := fm.cfg.Save(fm.cfgPath); err != nil {
		return err
	}
	fm.broadcast("station_removed", id, map[string]string{"station_id": id})
	return nil
}

// StopStation stops a single station runtime.
func (fm *FleetManager) StopStation(ctx context.Context, id string) (string, error) {
	op := fm.ops.Start("station.stop", id)
	fm.mu.RLock()
	ms := fm.stations[id]
	fm.mu.RUnlock()
	if ms == nil {
		fm.ops.Fail(op.ID, "station not found")
		return op.ID, fmt.Errorf("station %s not found", id)
	}
	ms.opMu.Lock()
	defer ms.opMu.Unlock()
	if ms.Runtime == nil {
		fm.ops.Fail(op.ID, "station not running")
		return op.ID, fmt.Errorf("station %s not running", id)
	}
	if err := fm.stopRuntime(ctx, ms); err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return op.ID, err
	}
	fm.ops.Succeed(op.ID)
	return op.ID, nil
}

// StartStation starts a single station runtime.
func (fm *FleetManager) StartStation(ctx context.Context, id string) (string, error) {
	op := fm.ops.Start("station.start", id)
	fm.mu.RLock()
	ms := fm.stations[id]
	fm.mu.RUnlock()
	if ms == nil {
		fm.ops.Fail(op.ID, "station not found")
		return op.ID, fmt.Errorf("station %s not found", id)
	}
	fm.mu.RLock()
	enabled := ms.Config != nil && ms.Config.Enabled
	fm.mu.RUnlock()
	if !enabled {
		fm.ops.Fail(op.ID, "station is disabled")
		return op.ID, fmt.Errorf("station %s is disabled", id)
	}

	ms.opMu.Lock()
	defer ms.opMu.Unlock()
	if ms.Runtime != nil {
		if state := ms.Runtime.LifecycleState(); state == StationRunning || state == StationStarting {
			fm.ops.Succeed(op.ID)
			return op.ID, nil
		}
	}
	if err := fm.stopRuntime(ctx, ms); err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return op.ID, err
	}
	if err := fm.startRuntimeFor(id, ms); err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return op.ID, err
	}
	fm.ops.Succeed(op.ID)
	return op.ID, nil
}

// RestartStation stops and starts a single station runtime.
func (fm *FleetManager) RestartStation(ctx context.Context, id string) (string, error) {
	op := fm.ops.Start("station.restart", id)
	if err := fm.replaceRuntime(ctx, id); err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return op.ID, err
	}
	fm.ops.Succeed(op.ID)
	return op.ID, nil
}

// EnableStation persists enabled=true and starts the runtime.
func (fm *FleetManager) EnableStation(ctx context.Context, id string) (string, error) {
	op := fm.ops.Start("station.enable", id)
	fm.mu.Lock()
	sc, _, found := fm.cfg.FindStation(id)
	if !found {
		fm.mu.Unlock()
		fm.ops.Fail(op.ID, "station not found")
		return op.ID, fmt.Errorf("station %s not found", id)
	}
	if sc.IsEnabled() {
		fm.mu.Unlock()
		fm.ops.Succeed(op.ID)
		return op.ID, nil
	}
	enabled := true
	sc.Enabled = &enabled
	saveErr := fm.cfg.Save(fm.cfgPath)
	if saveErr == nil {
		if _, exists := fm.stations[id]; !exists {
			fm.stations[id] = &ManagedStation{}
		}
	}
	fm.mu.Unlock()

	if saveErr != nil {
		fm.ops.Fail(op.ID, saveErr.Error())
		return op.ID, saveErr
	}

	if err := fm.replaceRuntime(ctx, id); err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return op.ID, err
	}
	fm.ops.Succeed(op.ID)
	return op.ID, nil
}

// DisableStation persists enabled=false and stops the runtime.
func (fm *FleetManager) DisableStation(ctx context.Context, id string) (string, error) {
	op := fm.ops.Start("station.disable", id)
	fm.mu.Lock()
	sc, _, found := fm.cfg.FindStation(id)
	if !found {
		fm.mu.Unlock()
		fm.ops.Fail(op.ID, "station not found")
		return op.ID, fmt.Errorf("station %s not found", id)
	}
	disabled := false
	sc.Enabled = &disabled
	saveErr := fm.cfg.Save(fm.cfgPath)
	ms := fm.stations[id]
	fm.mu.Unlock()

	if saveErr != nil {
		fm.ops.Fail(op.ID, saveErr.Error())
		return op.ID, saveErr
	}

	if ms != nil {
		ms.opMu.Lock()
		stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := fm.stopRuntime(stopCtx, ms)
		cancel()
		if err == nil {
			fm.mu.Lock()
			ms.Runtime = nil
			if effective, effErr := fm.effectiveStationLocked(id, false); effErr == nil {
				ms.Config = effective
			}
			fm.mu.Unlock()
		}
		ms.opMu.Unlock()
		if err != nil {
			fm.ops.Fail(op.ID, err.Error())
			return op.ID, err
		}
	}
	fm.ops.Succeed(op.ID)
	return op.ID, nil
}

// Reload reloads config from disk and reconciles runtime state.
func (fm *FleetManager) Reload(ctx context.Context) error {
	fm.mu.Lock()
	cfg, err := config.Load(fm.cfgPath)
	if err != nil {
		fm.mu.Unlock()
		return err
	}
	if err := cfg.ValidateStations(); err != nil {
		fm.mu.Unlock()
		return err
	}
	fm.cfg = cfg
	plan, err := fm.planReconcileLocked()
	fm.mu.Unlock()
	if err != nil {
		return err
	}
	fm.executeReconcilePlan(ctx, plan)
	return nil
}

// Shutdown stops all station runtimes and saves state.
func (fm *FleetManager) Shutdown(ctx context.Context) error {
	fm.mu.RLock()
	stations := make([]*ManagedStation, 0, len(fm.stations))
	for _, ms := range fm.stations {
		stations = append(stations, ms)
	}
	fm.mu.RUnlock()
	var wg sync.WaitGroup
	for _, ms := range stations {
		ms := ms
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms.opMu.Lock()
			defer ms.opMu.Unlock()
			if ms.Runtime == nil {
				return
			}
			stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			_ = ms.Runtime.Stop(stopCtx)
		}()
	}
	wg.Wait()
	return nil
}

// Snapshot returns the runtime snapshot for a station by stable ID.
func (fm *FleetManager) Snapshot(id string) (api.StationSnapshot, bool) {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	ms, ok := fm.stations[id]
	if !ok {
		return api.StationSnapshot{}, false
	}
	return snapshotForLocked(id, ms), true
}

// AllSnapshots returns snapshots for all stations (configured or runtime).
func (fm *FleetManager) AllSnapshots() []api.StationSnapshot {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	out := make([]api.StationSnapshot, 0, len(fm.stations))
	for id, ms := range fm.stations {
		out = append(out, snapshotForLocked(id, ms))
	}
	return out
}

// EngineSnapshotSources returns a snapshot-source for every station with a
// live runtime, keyed by station ID — used by main.go's WebSocket tick
// goroutines to build per-station and fleet-wide status broadcasts without
// reaching into fm.stations/fm.mu directly.
func (fm *FleetManager) EngineSnapshotSources() map[string]*ws.EngineSnapshotSource {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	out := make(map[string]*ws.EngineSnapshotSource, len(fm.stations))
	for id, ms := range fm.stations {
		if ms.Runtime == nil {
			continue
		}
		out[id] = &ws.EngineSnapshotSource{
			Engine:    ms.Runtime.Engine,
			Bridge:    ms.Runtime.Bridge,
			StartTime: ms.Runtime.StartTime,
		}
	}
	return out
}

// QueueStatus returns the current queue state for a station.
func (fm *FleetManager) QueueStatus(id string) (api.QueueStatus, error) {
	fm.mu.RLock()
	ms := fm.stations[id]
	fm.mu.RUnlock()
	if ms == nil || ms.Runtime == nil || ms.Runtime.Queue == nil {
		return api.QueueStatus{}, fmt.Errorf("station %s not found or not running", id)
	}
	q := ms.Runtime.Queue
	dropped := 0
	if dlq, ok := q.(queue.DeadLetterQueue); ok {
		dropped = dlq.Dropped()
	}
	return api.QueueStatus{Depth: q.Len(), Dropped: dropped}, nil
}

// QueueDrain drains the station queue by attempting to send queued messages.
func (fm *FleetManager) QueueDrain(id string) (string, error) {
	op := fm.ops.Start("queue.drain", id)
	fm.mu.RLock()
	ms := fm.stations[id]
	fm.mu.RUnlock()
	if ms == nil || ms.Runtime == nil || ms.Runtime.Queue == nil {
		fm.ops.Fail(op.ID, "station not found or not running")
		return op.ID, fmt.Errorf("station %s not found or not running", id)
	}
	if ms.Runtime.Bridge == nil || !ms.Runtime.Bridge.IsConnected() {
		fm.ops.Fail(op.ID, "OCPP not connected")
		return op.ID, errors.New("OCPP not connected")
	}
	go fm.drainQueue(ms, op.ID)
	return op.ID, nil
}

// drainQueue delegates one replay pass to the station's bridge — the bridge
// actually sends each queued message to the CSMS and applies its retry
// policy (see Bridge16/Bridge201.DrainOfflineQueue). This used to dequeue
// every StartTransaction/StopTransaction/MeterValues message WITHOUT sending
// them, silently discarding offline transactions the CSMS was waiting for.
// "Succeeded" here means the pass completed, not that the queue is empty —
// messages can legitimately remain (backoff pending, or attempts exhausted).
func (fm *FleetManager) drainQueue(ms *ManagedStation, opID string) {
	defer fm.ops.Succeed(opID)
	ms.Runtime.Bridge.DrainOfflineQueue()
	depth := 0
	if ms.Runtime.Queue != nil {
		depth = ms.Runtime.Queue.Len()
	}
	fm.broadcast("queue_status_changed", ms.Runtime.ID, api.QueueStatus{Depth: depth})
}

// QueueClear clears the station queue after confirmation.
func (fm *FleetManager) QueueClear(id string) error {
	fm.mu.RLock()
	ms := fm.stations[id]
	fm.mu.RUnlock()
	if ms == nil || ms.Runtime == nil || ms.Runtime.Queue == nil {
		return fmt.Errorf("station %s not found or not running", id)
	}
	for _, msg := range ms.Runtime.Queue.All() {
		ms.Runtime.Queue.Dequeue(msg.ID)
	}
	fm.broadcast("queue_status_changed", id, api.QueueStatus{Depth: 0, Dropped: 0})
	return nil
}

// QueueDeadLetter returns recent dead-letter entries for a station.
func (fm *FleetManager) QueueDeadLetter(id string) ([]api.DeadLetterEntry, error) {
	fm.mu.RLock()
	ms := fm.stations[id]
	fm.mu.RUnlock()
	if ms == nil || ms.Runtime == nil || ms.Runtime.DeadLetterPath == "" {
		return nil, fmt.Errorf("station %s not found or has no dead-letter queue", id)
	}
	f, err := os.Open(ms.Runtime.DeadLetterPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	const limit = 100
	entries := make([]api.DeadLetterEntry, 0, limit)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() && len(entries) < limit {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry api.DeadLetterEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// QueueDeadLetterClear clears the station dead-letter file.
func (fm *FleetManager) QueueDeadLetterClear(id string) error {
	fm.mu.RLock()
	ms := fm.stations[id]
	fm.mu.RUnlock()
	if ms == nil || ms.Runtime == nil || ms.Runtime.DeadLetterPath == "" {
		return fmt.Errorf("station %s not found or has no dead-letter queue", id)
	}
	return os.WriteFile(ms.Runtime.DeadLetterPath, []byte{}, 0600)
}

// PersistStation saves all station state to disk.
func (fm *FleetManager) PersistStation(id string) error {
	fm.mu.RLock()
	ms := fm.stations[id]
	fm.mu.RUnlock()
	if ms == nil || ms.Runtime == nil {
		return fmt.Errorf("station %s not found or not running", id)
	}
	return ms.Runtime.SaveAll()
}

// resolveStationOCPPID returns the OCPP ID for a station id, resolving both
// multi-station config entries and the default single-station — the latter has
// no entry in cfg.Stations but is a live station addressable by its id, so it
// falls back to the top-level config OCPP ID.
func (fm *FleetManager) resolveStationOCPPID(id string) (string, bool) {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	if sc, _, found := fm.cfg.FindStation(id); found {
		if sc.OCPPID != nil && *sc.OCPPID != "" {
			return *sc.OCPPID, true
		}
		return id, true
	}
	if id == fm.defaultID {
		if fm.cfg.OCPPID != "" {
			return fm.cfg.OCPPID, true
		}
		return id, true
	}
	return "", false
}

// SetOCPPPassword stores the OCPP password for a station's OCPP ID.
func (fm *FleetManager) SetOCPPPassword(id string, password string) error {
	ocppID, found := fm.resolveStationOCPPID(id)
	if !found {
		return fmt.Errorf("station %s not found", id)
	}
	return config.SetPassword(ocppID, password)
}

// ClearOCPPPassword removes the stored OCPP password for a station's OCPP ID.
func (fm *FleetManager) ClearOCPPPassword(id string) error {
	ocppID, found := fm.resolveStationOCPPID(id)
	if !found {
		return fmt.Errorf("station %s not found", id)
	}
	return config.DeletePassword(ocppID)
}

// TestCredentials verifies that a password is available for the station via
// the same lookup the OCPP client uses (keyring, then CHARGEGHOST_PASSWORD),
// so "credentials present" always matches what a connection would send.
func (fm *FleetManager) TestCredentials(id string) error {
	ocppID, found := fm.resolveStationOCPPID(id)
	if !found {
		return fmt.Errorf("station %s not found", id)
	}
	if config.GetPassword(ocppID) == "" {
		return errors.New("no stored password or fallback password")
	}
	return nil
}

// Save persists the current global config atomically.
func (fm *FleetManager) Save() error {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	if err := fm.cfg.Save(fm.cfgPath); err != nil {
		return err
	}
	fm.broadcast("fleet_config_saved", "", map[string]string{"path": fm.cfgPath})
	return nil
}

// Operation tracker methods.

func newOperationTracker(max int, hub *ws.Hub) *OperationTracker {
	if max <= 0 {
		max = 100
	}
	return &OperationTracker{
		ops: make(map[string]*api.Operation),
		max: max,
		hub: hub,
	}
}

func (ot *OperationTracker) broadcast(op *api.Operation, eventType string) {
	if ot.hub == nil {
		return
	}
	ot.hub.BroadcastMessage(ws.Message{
		Type:        eventType,
		StationID:   op.StationID,
		OperationID: op.ID,
		Data:        op,
	})
}

func (ot *OperationTracker) Start(opType, stationID string) *api.Operation {
	ot.mu.Lock()
	defer ot.mu.Unlock()
	ot.seq++
	op := &api.Operation{
		ID:        fmt.Sprintf("op-%d-%d", time.Now().Unix(), ot.seq),
		Type:      opType,
		StationID: stationID,
		State:     "running",
		StartedAt: time.Now(),
	}
	ot.ops[op.ID] = op
	ot.list = append(ot.list, op)
	if len(ot.list) > ot.max {
		old := ot.list[0]
		delete(ot.ops, old.ID)
		ot.list = ot.list[1:]
	}
	ot.broadcast(op, "station_operation_started")
	return op
}

func (ot *OperationTracker) Succeed(id string) {
	ot.mu.Lock()
	defer ot.mu.Unlock()
	op, ok := ot.ops[id]
	if !ok {
		return
	}
	now := time.Now()
	op.State = "succeeded"
	op.EndedAt = &now
	ot.broadcast(op, "station_operation_completed")
}

func (ot *OperationTracker) Fail(id, err string) {
	ot.mu.Lock()
	defer ot.mu.Unlock()
	op, ok := ot.ops[id]
	if !ok {
		return
	}
	now := time.Now()
	op.State = "failed"
	op.Error = err
	op.EndedAt = &now
	ot.broadcast(op, "station_operation_failed")
}

func (ot *OperationTracker) Get(id string) (api.Operation, bool) {
	ot.mu.RLock()
	defer ot.mu.RUnlock()
	op, ok := ot.ops[id]
	if !ok {
		return api.Operation{}, false
	}
	return *op, true
}

func (ot *OperationTracker) List() []api.Operation {
	ot.mu.RLock()
	defer ot.mu.RUnlock()
	out := make([]api.Operation, 0, len(ot.list))
	for _, op := range ot.list {
		out = append(out, *op)
	}
	return out
}

func (fm *FleetManager) Operations() []api.Operation {
	return fm.ops.List()
}

func (fm *FleetManager) Operation(id string) (api.Operation, bool) {
	return fm.ops.Get(id)
}
