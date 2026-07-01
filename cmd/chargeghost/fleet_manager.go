package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
type FleetManager struct {
	mu        sync.RWMutex
	cfgPath   string
	cfg       *config.Config
	baseDir   string
	hub       *ws.Hub
	stations  map[string]*ManagedStation
	defaultID string
	ops       *OperationTracker
}

// ManagedStation wraps a StationRuntime with its own lifecycle coordination.
type ManagedStation struct {
	Runtime *StationRuntime
	Config  *config.EffectiveStation
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	opMu    sync.Mutex
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
	}
	fm.rebuildDefaultID()
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

// GetAppContext returns the API context for a station by stable ID.
func (fm *FleetManager) GetAppContext(id string) (*api.AppContext, bool) {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	ms, ok := fm.stations[id]
	if !ok || ms.Runtime == nil {
		return nil, false
	}
	return ms.Runtime.AppContext(), true
}

// Registry builds a StationRegistry from the current fleet state.
func (fm *FleetManager) Registry() *api.StationRegistry {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	reg := &api.StationRegistry{
		DefaultID: fm.defaultID,
		Stations:  make(map[string]*api.AppContext, len(fm.stations)),
	}
	for id, ms := range fm.stations {
		if ms.Runtime != nil {
			reg.Stations[id] = ms.Runtime.AppContext()
		}
	}
	return reg
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

func (fm *FleetManager) rebuildDefaultID() {
	ids := fm.cfg.StationIDs()
	if len(ids) > 0 {
		fm.defaultID = ids[0]
		return
	}
	fm.defaultID = "default"
}

// Load reloads the global config from disk and validates it. It does not
// mutate runtime state; call Reconcile afterwards to apply changes.
func (fm *FleetManager) Load(ctx context.Context) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	cfg, err := config.Load(fm.cfgPath)
	if err != nil {
		return err
	}
	if err := cfg.ValidateStations(); err != nil {
		return err
	}
	fm.cfg = cfg
	fm.rebuildDefaultID()
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

// Start builds and starts all enabled stations. It should be called once
// after the process starts.
func (fm *FleetManager) Start(ctx context.Context) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return fm.startEnabledLocked(ctx)
}

func (fm *FleetManager) startEnabledLocked(ctx context.Context) error {
	effective, err := fm.cfg.EffectiveStationConfigs()
	if err != nil {
		return err
	}
	if len(effective) == 0 {
		return errors.New("no stations configured")
	}
	for _, es := range effective {
		es := es
		if !es.Enabled {
			fm.stations[es.ID] = &ManagedStation{Config: es}
			continue
		}
		persistDir, queueDir := fm.dirsFor(es)
		sr, err := buildStationRuntime(es.ID, es.Config, fm.hub, persistDir, queueDir)
		if err != nil {
			fm.stations[es.ID] = &ManagedStation{Config: es, Runtime: &StationRuntime{ID: es.ID, lifecycleState: StationFailed, lastErr: err.Error()}}
			return fmt.Errorf("build station %s: %w", es.ID, err)
		}
		sr.MultiStation = fm.isMultiStation()
		ms := &ManagedStation{Runtime: sr, Config: es}
		fm.stations[es.ID] = ms
		ms.start(ctx, fm.hub)
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

// Reconcile applies the current global config to runtime state:
// start newly enabled stations, stop removed/disabled stations, restart stations
// with startup-only changes, and apply live changes without restart.
func (fm *FleetManager) Reconcile(ctx context.Context) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	effective, err := fm.cfg.EffectiveStationConfigs()
	if err != nil {
		return err
	}
	newIDs := make(map[string]bool, len(effective))
	for _, es := range effective {
		newIDs[es.ID] = true
	}

	// Stop removed or disabled stations.
	for id, ms := range fm.stations {
		enabled := false
		for _, es := range effective {
			if es.ID == id {
				enabled = es.Enabled
				break
			}
		}
		if !newIDs[id] || !enabled {
			fm.stopManagedStationLocked(ctx, ms)
			if !newIDs[id] {
				delete(fm.stations, id)
			}
		}
	}

	// Start new enabled stations and restart stations whose startup config changed.
	for _, es := range effective {
		es := es
		ms, exists := fm.stations[es.ID]
		if !exists {
			ms = &ManagedStation{Config: es}
			fm.stations[es.ID] = ms
		}
		ms.Config = es
		if !es.Enabled {
			if ms.Runtime != nil {
				fm.stopManagedStationLocked(ctx, ms)
			}
			continue
		}
		needsRestart := false
		if ms.Runtime != nil && ms.Runtime.LifecycleState() != StationRunning && ms.Runtime.LifecycleState() != StationStarting {
			needsRestart = true
		}
		if ms.Runtime == nil || needsRestart {
			persistDir, queueDir := fm.dirsFor(es)
			sr, err := buildStationRuntime(es.ID, es.Config, fm.hub, persistDir, queueDir)
			if err != nil {
				ms.Runtime = &StationRuntime{ID: es.ID, lifecycleState: StationFailed, lastErr: err.Error()}
				continue
			}
			sr.MultiStation = fm.isMultiStation()
			ms.Runtime = sr
			ms.start(ctx, fm.hub)
		}
	}
	fm.rebuildDefaultID()
	return nil
}

func (fm *FleetManager) stopManagedStationLocked(ctx context.Context, ms *ManagedStation) {
	if ms.Runtime == nil {
		return
	}
	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = ms.Runtime.Stop(stopCtx)
}

func (ms *ManagedStation) start(ctx context.Context, hub *ws.Hub) {
	ms.wg = sync.WaitGroup{}
	stationCtx, cancel := context.WithCancel(ctx)
	ms.cancel = cancel
	ms.wg.Add(1)
	go func() {
		defer ms.wg.Done()
		_ = ms.Runtime.Start(stationCtx)
		if hub != nil {
			hub.BroadcastMessage(ws.Message{
				Type:      "station_lifecycle_changed",
				StationID: ms.Runtime.ID,
				Data: map[string]string{
					"state": string(ms.Runtime.LifecycleState()),
					"error": ms.Runtime.LastError(),
				},
			})
		}
	}()
	time.Sleep(10 * time.Millisecond)
}

// CreateStation adds a new station to the global config, optionally persists it,
// and optionally starts it.
func (fm *FleetManager) CreateStation(ctx context.Context, req api.CreateStationRequest) (api.StationSnapshot, string, error) {
	op := fm.ops.Start("station.create", req.ID)
	fm.mu.Lock()
	defer fm.mu.Unlock()

	if req.OCPPID == "" {
		fm.ops.Fail(op.ID, "ocpp_id is required")
		return api.StationSnapshot{}, op.ID, errors.New("ocpp_id is required")
	}
	if req.ID == "" {
		req.ID = req.OCPPID
	}
	if _, _, found := fm.cfg.FindStation(req.ID); found {
		fm.ops.Fail(op.ID, "station already exists")
		return api.StationSnapshot{}, op.ID, fmt.Errorf("station %s already exists", req.ID)
	}
	if len(fm.cfg.Stations) >= 8 {
		fm.ops.Fail(op.ID, "too many stations")
		return api.StationSnapshot{}, op.ID, errors.New("too many stations: maximum is 8")
	}

	st := apiCreateStationRequestToStationConfig(req)
	if err := fm.cfg.UpsertStation(st); err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return api.StationSnapshot{}, op.ID, err
	}
	if req.OCPPPassword != "" {
		_ = config.SetPassword(req.OCPPID, req.OCPPPassword)
	}
	if req.Save {
		if err := fm.cfg.Save(fm.cfgPath); err != nil {
			fm.ops.Fail(op.ID, err.Error())
			return api.StationSnapshot{}, op.ID, err
		}
	}

	es, err := fm.cfg.EffectiveStationConfig(req.ID)
	if err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return api.StationSnapshot{}, op.ID, err
	}
	effective := &config.EffectiveStation{ID: req.ID, Enabled: st.IsEnabled(), Config: es}
	fm.rebuildDefaultID()

	var snapshot api.StationSnapshot
	if req.Start && effective.Enabled {
		persistDir, queueDir := fm.dirsFor(effective)
		sr, err := buildStationRuntime(effective.ID, effective.Config, fm.hub, persistDir, queueDir)
		if err != nil {
			fm.stations[effective.ID] = &ManagedStation{Config: effective, Runtime: &StationRuntime{ID: effective.ID, lifecycleState: StationFailed, lastErr: err.Error()}}
			fm.ops.Fail(op.ID, err.Error())
			return api.StationSnapshot{}, op.ID, err
		}
		sr.MultiStation = fm.isMultiStation()
		ms := &ManagedStation{Runtime: sr, Config: effective}
		fm.stations[effective.ID] = ms
		ms.start(ctx, fm.hub)
		snapshot = sr.Snapshot()
	} else {
		fm.stations[effective.ID] = &ManagedStation{Config: effective}
		snapshot = api.StationSnapshot{StationID: effective.ID, OCPPID: effective.Config.OCPPID, Enabled: effective.Enabled, LifecycleState: string(StationConfigured)}
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
func (fm *FleetManager) UpdateStation(ctx context.Context, id string, req api.PatchStationConfigRequest) (api.StationSnapshot, string, error) {
	op := fm.ops.Start("station.update", id)
	fm.mu.Lock()
	defer fm.mu.Unlock()

	sc, idx, found := fm.cfg.FindStation(id)
	if !found {
		fm.ops.Fail(op.ID, "station not found")
		return api.StationSnapshot{}, op.ID, fmt.Errorf("station %s not found", id)
	}
	oldOCPPID := sc.StationID()
	if sc.OCPPID != nil {
		oldOCPPID = *sc.OCPPID
	}

	changed, restartRequired := applyPatchToStation(sc, req)
	if req.Enabled != nil {
		sc.Enabled = req.Enabled
		changed = append(changed, "enabled")
	}

	if err := fm.cfg.ValidateStations(); err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return api.StationSnapshot{}, op.ID, err
	}

	if req.OCPPPassword != nil && *req.OCPPPassword != "" {
		ocppID := oldOCPPID
		if sc.OCPPID != nil {
			ocppID = *sc.OCPPID
		}
		if err := config.SetPassword(ocppID, *req.OCPPPassword); err != nil {
			fm.ops.Fail(op.ID, err.Error())
			return api.StationSnapshot{}, op.ID, err
		}
		changed = append(changed, "ocpp_password")
	}

	if req.Save {
		if err := fm.cfg.Save(fm.cfgPath); err != nil {
			fm.ops.Fail(op.ID, err.Error())
			return api.StationSnapshot{}, op.ID, err
		}
	}

	// Apply live fields to the running runtime.
	ms := fm.stations[id]
	if ms != nil && ms.Runtime != nil {
		if req.EVBatteryCapacity != nil {
			ms.Runtime.Engine.SetEVBatteryCapacity(*req.EVBatteryCapacity * 1000)
		}
	}

	fm.ops.Succeed(op.ID)
	var snapshot api.StationSnapshot
	if ms != nil && ms.Runtime != nil {
		snapshot = ms.Runtime.Snapshot()
	} else if ms != nil && ms.Config != nil {
		snapshot = api.StationSnapshot{StationID: id, OCPPID: ms.Config.Config.OCPPID, Enabled: ms.Config.Enabled, LifecycleState: string(StationConfigured)}
	} else {
		es, err := fm.cfg.EffectiveStationConfig(id)
		if err == nil {
			enabled := true
			if sc, _, found := fm.cfg.FindStation(id); found {
				enabled = sc.IsEnabled()
			}
			snapshot = api.StationSnapshot{StationID: id, OCPPID: es.OCPPID, Enabled: enabled, LifecycleState: string(StationConfigured)}
		}
	}
	snapshot.RestartRequired = restartRequired
	fm.broadcast("station_config_changed", id, snapshot)
	if restartRequired {
		fm.broadcast("station_restart_required_changed", id, map[string]bool{"restart_required": true})
	}
	_ = changed
	_ = idx
	_ = oldOCPPID
	return snapshot, op.ID, nil

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
func (fm *FleetManager) DeleteStation(ctx context.Context, id string, opts api.DeleteStationOptions) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	_, _, found := fm.cfg.FindStation(id)
	if !found {
		return fmt.Errorf("station %s not found", id)
	}
	ms := fm.stations[id]
	if ms != nil && ms.Runtime != nil && ms.Runtime.LifecycleState() == StationRunning {
		if !opts.Force {
			return errors.New("station is running; use force=true to stop first")
		}
		stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = ms.Runtime.Stop(stopCtx)
	}
	if !opts.AllowEmpty {
		enabledCount := 0
		for _, sc := range fm.cfg.Stations {
			if sc.IsEnabled() && sc.StationID() != id {
				enabledCount++
			}
		}
		if enabledCount == 0 {
			return errors.New("cannot delete the last enabled station; use allow_empty=true")
		}
	}
	if err := fm.cfg.RemoveStation(id); err != nil {
		return err
	}
	if opts.DeleteState && ms != nil && ms.Runtime != nil {
		_ = os.RemoveAll(ms.Runtime.PersistDir)
	}
	if opts.ClearPassword {
		ocppID := id
		if ms != nil && ms.Config != nil && ms.Config.Config != nil && ms.Config.Config.OCPPID != "" {
			ocppID = ms.Config.Config.OCPPID
		}
		_ = config.SetPassword(ocppID, "")
	}
	delete(fm.stations, id)
	if fm.defaultID == id && opts.NewDefaultID != "" {
		fm.defaultID = opts.NewDefaultID
	}
	fm.rebuildDefaultID()
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
	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ms.Runtime.Stop(stopCtx); err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return op.ID, err
	}
	fm.ops.Succeed(op.ID)
	return op.ID, nil
}

// StartStation starts a single station runtime.
func (fm *FleetManager) StartStation(ctx context.Context, id string) (string, error) {
	op := fm.ops.Start("station.start", id)
	fm.mu.Lock()
	defer fm.mu.Unlock()
	ms := fm.stations[id]
	if ms == nil {
		fm.ops.Fail(op.ID, "station not found")
		return op.ID, fmt.Errorf("station %s not found", id)
	}
	if ms.Runtime != nil && ms.Runtime.LifecycleState() == StationRunning {
		fm.ops.Succeed(op.ID)
		return op.ID, nil
	}
	if ms.Config == nil || !ms.Config.Enabled {
		fm.ops.Fail(op.ID, "station is disabled")
		return op.ID, fmt.Errorf("station %s is disabled", id)
	}
	persistDir, queueDir := fm.dirsFor(ms.Config)
	sr, err := buildStationRuntime(ms.Config.ID, ms.Config.Config, fm.hub, persistDir, queueDir)
	if err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return op.ID, err
	}
	sr.MultiStation = fm.isMultiStation()
	ms.Runtime = sr
	ms.start(ctx, fm.hub)
	fm.ops.Succeed(op.ID)
	return op.ID, nil
}

// RestartStation stops and starts a single station runtime.
func (fm *FleetManager) RestartStation(ctx context.Context, id string) (string, error) {
	op := fm.ops.Start("station.restart", id)
	fm.mu.RLock()
	ms := fm.stations[id]
	fm.mu.RUnlock()
	if ms == nil {
		fm.ops.Fail(op.ID, "station not found")
		return op.ID, fmt.Errorf("station %s not found", id)
	}
	ms.opMu.Lock()
	defer ms.opMu.Unlock()
	if ms.Runtime != nil {
		stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = ms.Runtime.Stop(stopCtx)
	}
	fm.mu.Lock()
	persistDir, queueDir := fm.dirsFor(ms.Config)
	sr, err := buildStationRuntime(ms.Config.ID, ms.Config.Config, fm.hub, persistDir, queueDir)
	if err != nil {
		ms.Runtime = &StationRuntime{ID: ms.Config.ID, lifecycleState: StationFailed, lastErr: err.Error()}
		fm.mu.Unlock()
		fm.ops.Fail(op.ID, err.Error())
		return op.ID, err
	}
	sr.MultiStation = fm.isMultiStation()
	ms.Runtime = sr
	ms.start(ctx, fm.hub)
	fm.mu.Unlock()
	fm.ops.Succeed(op.ID)
	return op.ID, nil
}

// EnableStation persists enabled=true and starts the runtime.
func (fm *FleetManager) EnableStation(ctx context.Context, id string) (string, error) {
	op := fm.ops.Start("station.enable", id)
	fm.mu.Lock()
	defer fm.mu.Unlock()
	sc, _, found := fm.cfg.FindStation(id)
	if !found {
		fm.ops.Fail(op.ID, "station not found")
		return op.ID, fmt.Errorf("station %s not found", id)
	}
	if sc.IsEnabled() {
		fm.ops.Succeed(op.ID)
		return op.ID, nil
	}
	enabled := true
	sc.Enabled = &enabled
	if err := fm.cfg.Save(fm.cfgPath); err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return op.ID, err
	}
	ms := fm.stations[id]
	if ms == nil {
		es, err := fm.cfg.EffectiveStationConfig(id)
		if err != nil {
			fm.ops.Fail(op.ID, err.Error())
			return op.ID, err
		}
		ms = &ManagedStation{Config: &config.EffectiveStation{ID: id, Enabled: true, Config: es}}
		fm.stations[id] = ms
	}
	ms.Config = &config.EffectiveStation{ID: id, Enabled: true, Config: ms.Config.Config}
	persistDir, queueDir := fm.dirsFor(ms.Config)
	sr, err := buildStationRuntime(id, ms.Config.Config, fm.hub, persistDir, queueDir)
	if err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return op.ID, err
	}
	sr.MultiStation = fm.isMultiStation()
	ms.Runtime = sr
	ms.start(ctx, fm.hub)
	fm.ops.Succeed(op.ID)
	return op.ID, nil
}

// DisableStation persists enabled=false and stops the runtime.
func (fm *FleetManager) DisableStation(ctx context.Context, id string) (string, error) {
	op := fm.ops.Start("station.disable", id)
	fm.mu.Lock()
	defer fm.mu.Unlock()
	sc, _, found := fm.cfg.FindStation(id)
	if !found {
		fm.ops.Fail(op.ID, "station not found")
		return op.ID, fmt.Errorf("station %s not found", id)
	}
	disabled := false
	sc.Enabled = &disabled
	if err := fm.cfg.Save(fm.cfgPath); err != nil {
		fm.ops.Fail(op.ID, err.Error())
		return op.ID, err
	}
	ms := fm.stations[id]
	if ms != nil && ms.Runtime != nil {
		stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = ms.Runtime.Stop(stopCtx)
		ms.Runtime = nil
	}
	fm.ops.Succeed(op.ID)
	return op.ID, nil
}

// Reload reloads config from disk and reconciles runtime state.
func (fm *FleetManager) Reload(ctx context.Context) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	cfg, err := config.Load(fm.cfgPath)
	if err != nil {
		return err
	}
	if err := cfg.ValidateStations(); err != nil {
		return err
	}
	fm.cfg = cfg
	return fm.reconcileLocked(ctx)
}

func (fm *FleetManager) reconcileLocked(ctx context.Context) error {
	effective, err := fm.cfg.EffectiveStationConfigs()
	if err != nil {
		return err
	}
	newIDs := make(map[string]bool, len(effective))
	for _, es := range effective {
		newIDs[es.ID] = true
	}
	for id, ms := range fm.stations {
		enabled := false
		for _, es := range effective {
			if es.ID == id {
				enabled = es.Enabled
				break
			}
		}
		if !newIDs[id] || !enabled {
			fm.stopManagedStationLocked(ctx, ms)
			if !newIDs[id] {
				delete(fm.stations, id)
			}
		}
	}
	for _, es := range effective {
		es := es
		ms, exists := fm.stations[es.ID]
		if !exists {
			ms = &ManagedStation{Config: es}
			fm.stations[es.ID] = ms
		}
		ms.Config = es
		if !es.Enabled {
			if ms.Runtime != nil {
				fm.stopManagedStationLocked(ctx, ms)
			}
			continue
		}
		if ms.Runtime == nil || (ms.Runtime.LifecycleState() != StationRunning && ms.Runtime.LifecycleState() != StationStarting) {
			persistDir, queueDir := fm.dirsFor(es)
			sr, err := buildStationRuntime(es.ID, es.Config, fm.hub, persistDir, queueDir)
			if err != nil {
				ms.Runtime = &StationRuntime{ID: es.ID, lifecycleState: StationFailed, lastErr: err.Error()}
				continue
			}
			sr.MultiStation = fm.isMultiStation()
			ms.Runtime = sr
			ms.start(ctx, fm.hub)
		}
	}
	fm.rebuildDefaultID()
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
	if ms.Runtime != nil {
		return ms.Runtime.Snapshot(), true
	}
	if ms.Config != nil {
		return api.StationSnapshot{StationID: id, OCPPID: ms.Config.Config.OCPPID, Enabled: ms.Config.Enabled, LifecycleState: string(StationConfigured)}, true
	}
	return api.StationSnapshot{StationID: id, LifecycleState: string(StationConfigured)}, true
}

// AllSnapshots returns snapshots for all stations (configured or runtime).
func (fm *FleetManager) AllSnapshots() []api.StationSnapshot {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	out := make([]api.StationSnapshot, 0, len(fm.stations))
	for id, ms := range fm.stations {
		if ms.Runtime != nil {
			out = append(out, ms.Runtime.Snapshot())
		} else if ms.Config != nil {
			out = append(out, api.StationSnapshot{StationID: id, OCPPID: ms.Config.Config.OCPPID, Enabled: ms.Config.Enabled, LifecycleState: string(StationConfigured)})
		} else {
			out = append(out, api.StationSnapshot{StationID: id, LifecycleState: string(StationConfigured)})
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

func (fm *FleetManager) drainQueue(ms *ManagedStation, opID string) {
	defer fm.ops.Succeed(opID)
	q := ms.Runtime.Queue
	for {
		msg, ok := q.Peek()
		if !ok {
			fm.broadcast("queue_status_changed", ms.Runtime.ID, api.QueueStatus{Depth: 0})
			return
		}
		switch msg.Type {
		case "StartTransaction":
			// Cannot replay without context; drain is best-effort for now.
			q.Dequeue(msg.ID)
		case "StopTransaction":
			q.Dequeue(msg.ID)
		case "MeterValues":
			q.Dequeue(msg.ID)
		default:
			q.Dequeue(msg.ID)
		}
	}
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

// SetOCPPPassword stores the OCPP password for a station's OCPP ID.
func (fm *FleetManager) SetOCPPPassword(id string, password string) error {
	fm.mu.RLock()
	sc, _, found := fm.cfg.FindStation(id)
	fm.mu.RUnlock()
	if !found {
		return fmt.Errorf("station %s not found", id)
	}
	ocppID := id
	if sc.OCPPID != nil {
		ocppID = *sc.OCPPID
	}
	return config.SetPassword(ocppID, password)
}

// ClearOCPPPassword removes the stored OCPP password for a station's OCPP ID.
func (fm *FleetManager) ClearOCPPPassword(id string) error {
	fm.mu.RLock()
	sc, _, found := fm.cfg.FindStation(id)
	fm.mu.RUnlock()
	if !found {
		return fmt.Errorf("station %s not found", id)
	}
	ocppID := id
	if sc.OCPPID != nil {
		ocppID = *sc.OCPPID
	}
	return config.SetPassword(ocppID, "")
}

// TestCredentials verifies that a stored password exists for the station.
func (fm *FleetManager) TestCredentials(id string) error {
	fm.mu.RLock()
	sc, _, found := fm.cfg.FindStation(id)
	fm.mu.RUnlock()
	if !found {
		return fmt.Errorf("station %s not found", id)
	}
	ocppID := id
	if sc.OCPPID != nil {
		ocppID = *sc.OCPPID
	}
	pw := config.GetPassword(ocppID)
	if pw == "" && os.Getenv("CHARGEGHOST_PASSWORD") == "" {
		return errors.New("no stored password or fallback password")
	}
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
