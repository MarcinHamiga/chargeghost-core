package v16

import (
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/persistence"
)

const profilesFile = "charging_profiles.json"

type profileJSON struct {
	ProfileID      int                `json:"profile_id"`
	ConnectorID    int                `json:"connector_id"`
	StackLevel     int                `json:"stack_level"`
	Purpose        string             `json:"purpose"`
	Kind           string             `json:"kind"`
	RecurrencyKind string             `json:"recurrency_kind,omitempty"`
	ValidFrom      *time.Time         `json:"valid_from,omitempty"`
	ValidTo        *time.Time         `json:"valid_to,omitempty"`
	StartSchedule  *time.Time         `json:"start_schedule,omitempty"`
	Schedule       scheduleJSON       `json:"schedule"`
}

type scheduleJSON struct {
	Duration         int          `json:"duration"`
	StartSchedule    *time.Time   `json:"start_schedule,omitempty"`
	ChargingRateUnit string       `json:"charging_rate_unit"`
	MinChargingRate  float64      `json:"min_charging_rate"`
	Periods          []periodJSON `json:"periods"`
}

type periodJSON struct {
	StartPeriod  int     `json:"start_period"`
	Limit        float64 `json:"limit"`
	NumberPhases *int    `json:"number_phases,omitempty"`
}

// SetPersistDir enables auto-save. Pass "" to disable.
func (m *ChargingProfileManager) SetPersistDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.persistDir = dir
}

func (m *ChargingProfileManager) SaveState(dir string) error {
	m.mu.RLock()
	profiles := make([]profileJSON, 0, len(m.profiles))
	for _, p := range m.profiles {
		profiles = append(profiles, engineProfileToJSON(p))
	}
	m.mu.RUnlock()
	return persistence.WriteJSON(dir, profilesFile, profiles)
}

func (m *ChargingProfileManager) LoadState(dir string) error {
	var profiles []profileJSON
	if err := persistence.ReadJSON(dir, profilesFile, &profiles); err != nil {
		return err
	}
	if profiles == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.profiles = make(map[profileKey]engine.ChargingProfile, len(profiles))
	for _, pj := range profiles {
		ep := jsonToEngineProfile(pj)
		m.profiles[profileKey{ep.ConnectorID, ep.ProfileID}] = ep
	}
	return nil
}

func (m *ChargingProfileManager) autoSave() {
	if m.persistDir != "" {
		_ = m.SaveState(m.persistDir)
	}
}

func engineProfileToJSON(p engine.ChargingProfile) profileJSON {
	periods := make([]periodJSON, len(p.Schedule.Periods))
	for i, sp := range p.Schedule.Periods {
		periods[i] = periodJSON{StartPeriod: sp.StartPeriod, Limit: sp.Limit, NumberPhases: sp.NumberPhases}
	}
	return profileJSON{
		ProfileID: p.ProfileID, ConnectorID: p.ConnectorID,
		StackLevel: p.StackLevel, Purpose: p.Purpose,
		Kind: p.Kind, RecurrencyKind: p.RecurrencyKind,
		ValidFrom: p.ValidFrom, ValidTo: p.ValidTo,
		StartSchedule: p.StartSchedule,
		Schedule: scheduleJSON{
			Duration: p.Schedule.Duration, StartSchedule: p.Schedule.StartSchedule,
			ChargingRateUnit: p.Schedule.ChargingRateUnit,
			MinChargingRate:  p.Schedule.MinChargingRate,
			Periods:          periods,
		},
	}
}

func jsonToEngineProfile(pj profileJSON) engine.ChargingProfile {
	periods := make([]engine.ChargingSchedulePeriod, len(pj.Schedule.Periods))
	for i, sp := range pj.Schedule.Periods {
		periods[i] = engine.ChargingSchedulePeriod{StartPeriod: sp.StartPeriod, Limit: sp.Limit, NumberPhases: sp.NumberPhases}
	}
	return engine.ChargingProfile{
		ProfileID: pj.ProfileID, ConnectorID: pj.ConnectorID,
		StackLevel: pj.StackLevel, Purpose: pj.Purpose,
		Kind: pj.Kind, RecurrencyKind: pj.RecurrencyKind,
		ValidFrom: pj.ValidFrom, ValidTo: pj.ValidTo,
		StartSchedule: pj.StartSchedule,
		Schedule: engine.ChargingSchedule{
			Duration: pj.Schedule.Duration, StartSchedule: pj.Schedule.StartSchedule,
			ChargingRateUnit: pj.Schedule.ChargingRateUnit,
			MinChargingRate:  pj.Schedule.MinChargingRate,
			Periods:          periods,
		},
	}
}
