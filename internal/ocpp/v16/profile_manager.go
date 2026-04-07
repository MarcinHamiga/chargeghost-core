package v16

import (
	"errors"
	"math"
	"sync"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
)

const maxProfiles = 20

type profileKey struct {
	connectorID int
	profileID   int
}

// ChargingProfileManager computes effective charging limits using OCPP smart charging rules.
// Thread-safe via sync.RWMutex.
type ChargingProfileManager struct {
	mu       sync.RWMutex
	profiles map[profileKey]engine.ChargingProfile
}

// NewChargingProfileManager creates an empty manager.
func NewChargingProfileManager() *ChargingProfileManager {
	return &ChargingProfileManager{
		profiles: make(map[profileKey]engine.ChargingProfile),
	}
}

// SetChargingProfile stores a profile. Replaces any existing profile with same (connectorID, profileID).
func (m *ChargingProfileManager) SetChargingProfile(connectorID int, profile engine.ChargingProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := profileKey{connectorID, profile.ProfileID}
	if _, exists := m.profiles[key]; !exists && len(m.profiles) >= maxProfiles {
		return errors.New("max profiles reached")
	}
	profile.ConnectorID = connectorID
	m.profiles[key] = profile
	return nil
}

// ClearChargingProfile removes profiles matching the optional filters.
// nil filter fields match everything.
func (m *ChargingProfileManager) ClearChargingProfile(connectorID, profileID *int, purpose, stackLevel *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, p := range m.profiles {
		if profileID != nil && p.ProfileID != *profileID {
			continue
		}
		if connectorID != nil && p.ConnectorID != *connectorID {
			continue
		}
		if purpose != nil && p.Purpose != *purpose {
			continue
		}
		_ = stackLevel // stackLevel filter unused for now
		delete(m.profiles, k)
	}
	return nil
}

// GetChargingProfiles returns all stored profiles for the REST API.
func (m *ChargingProfileManager) GetChargingProfiles() []engine.ChargingProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]engine.ChargingProfile, 0, len(m.profiles))
	for _, p := range m.profiles {
		result = append(result, p)
	}
	return result
}

// GetChargingProfile returns a single profile by connectorID and profileID.
func (m *ChargingProfileManager) GetChargingProfile(connectorID, profileID int) (engine.ChargingProfile, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.profiles[profileKey{connectorID, profileID}]
	return p, ok
}

// GetCompositeLimit computes the effective current limit (Amps) for a connector at now.
// Returns nil when no profiles apply.
func (m *ChargingProfileManager) GetCompositeLimit(
	connectorID, transactionID int,
	now time.Time,
	connectorVoltage float64,
	transactionStart *time.Time,
	phases int,
) *float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	maxLimit := m.resolveLimit("ChargePointMaxProfile", connectorID, transactionID, now, connectorVoltage, transactionStart, phases)
	txLimit := m.resolveTxLimit(connectorID, transactionID, now, connectorVoltage, transactionStart, phases)

	if maxLimit == nil && txLimit == nil {
		return nil
	}
	if maxLimit == nil {
		return txLimit
	}
	if txLimit == nil {
		return maxLimit
	}
	composite := min(*maxLimit, *txLimit)
	return &composite
}

// GetCompositeSchedule builds the effective schedule for a connector over a duration.
func (m *ChargingProfileManager) GetCompositeSchedule(
	connectorID, transactionID int,
	startTime time.Time,
	duration int,
	connectorVoltage float64,
	transactionStart *time.Time,
	phases int,
) ([]engine.ChargingSchedulePeriod, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	endTime := startTime.Add(time.Duration(duration) * time.Second)
	boundaries := []time.Time{startTime}

	for _, p := range m.profiles {
		if !m.profileAppliesToConnector(p, connectorID) {
			continue
		}
		if !m.isProfileValid(p, startTime) {
			continue
		}
		for _, period := range p.Schedule.Periods {
			schedStart := m.getScheduleStart(p, transactionStart)
			if schedStart == nil {
				continue
			}
			t := schedStart.Add(time.Duration(period.StartPeriod) * time.Second)
			if t.After(startTime) && t.Before(endTime) {
				boundaries = append(boundaries, t)
			}
		}
	}

	sortTimes(boundaries)
	periods := make([]engine.ChargingSchedulePeriod, 0, len(boundaries))
	for _, t := range boundaries {
		limit := m.compositeLimitAt(connectorID, transactionID, t, connectorVoltage, transactionStart, phases)
		startPeriod := int(t.Sub(startTime).Seconds())
		limitVal := 0.0
		if limit != nil {
			limitVal = *limit
		}
		periods = append(periods, engine.ChargingSchedulePeriod{
			StartPeriod: startPeriod,
			Limit:       limitVal,
		})
	}
	return periods, nil
}

// --- private helpers ---

func (m *ChargingProfileManager) resolveTxLimit(connectorID, transactionID int, now time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
	limit := m.resolveLimit("TxProfile", connectorID, transactionID, now, voltage, txStart, phases)
	if limit != nil {
		return limit
	}
	return m.resolveLimit("TxDefaultProfile", connectorID, transactionID, now, voltage, txStart, phases)
}

func (m *ChargingProfileManager) resolveLimit(purpose string, connectorID, transactionID int, now time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
	var best *engine.ChargingProfile
	for _, p := range m.profiles {
		pCopy := p
		if pCopy.Purpose != purpose {
			continue
		}
		if !m.profileAppliesToConnector(pCopy, connectorID) {
			continue
		}
		if !m.isProfileValid(pCopy, now) {
			continue
		}
		if best == nil || pCopy.StackLevel > best.StackLevel {
			best = &pCopy
		}
	}
	if best == nil {
		return nil
	}
	return m.limitFromProfile(*best, now, voltage, txStart, phases)
}

func (m *ChargingProfileManager) limitFromProfile(p engine.ChargingProfile, now time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
	schedStart := m.getScheduleStart(p, txStart)
	if schedStart == nil {
		return nil
	}

	elapsed := elapsedSeconds(p, now, *schedStart)
	if elapsed < 0 {
		return nil
	}

	period := activePeriod(p.Schedule.Periods, elapsed)
	if period == nil {
		return nil
	}

	limitA := period.Limit
	if p.Schedule.ChargingRateUnit == "W" {
		if voltage > 0 && phases > 0 {
			limitA = period.Limit / (voltage * float64(phases))
		}
	}
	return &limitA
}

func (m *ChargingProfileManager) getScheduleStart(p engine.ChargingProfile, txStart *time.Time) *time.Time {
	switch p.Kind {
	case "Absolute":
		return p.StartSchedule
	case "Relative":
		return txStart
	case "Recurring":
		return p.StartSchedule
	}
	return nil
}

func elapsedSeconds(p engine.ChargingProfile, now time.Time, schedStart time.Time) float64 {
	switch p.Kind {
	case "Absolute", "Relative":
		return now.Sub(schedStart).Seconds()
	case "Recurring":
		var cycleLen float64
		switch p.RecurrencyKind {
		case "Weekly":
			cycleLen = 604800
		default:
			cycleLen = 86400
		}
		elapsed := now.Sub(schedStart).Seconds()
		if elapsed < 0 {
			return -1
		}
		return math.Mod(elapsed, cycleLen)
	}
	return -1
}

func activePeriod(periods []engine.ChargingSchedulePeriod, elapsedSecs float64) *engine.ChargingSchedulePeriod {
	var active *engine.ChargingSchedulePeriod
	for i := range periods {
		if float64(periods[i].StartPeriod) <= elapsedSecs {
			active = &periods[i]
		}
	}
	return active
}

func (m *ChargingProfileManager) profileAppliesToConnector(p engine.ChargingProfile, connectorID int) bool {
	return p.ConnectorID == connectorID || p.ConnectorID == 0
}

func (m *ChargingProfileManager) isProfileValid(p engine.ChargingProfile, now time.Time) bool {
	if p.ValidFrom != nil && now.Before(*p.ValidFrom) {
		return false
	}
	if p.ValidTo != nil && now.After(*p.ValidTo) {
		return false
	}
	return true
}

func (m *ChargingProfileManager) compositeLimitAt(connectorID, transactionID int, t time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
	maxL := m.resolveLimit("ChargePointMaxProfile", connectorID, transactionID, t, voltage, txStart, phases)
	txL := m.resolveTxLimit(connectorID, transactionID, t, voltage, txStart, phases)
	if maxL == nil && txL == nil {
		return nil
	}
	if maxL == nil {
		return txL
	}
	if txL == nil {
		return maxL
	}
	v := min(*maxL, *txL)
	return &v
}

func sortTimes(times []time.Time) {
	for i := 1; i < len(times); i++ {
		for j := i; j > 0 && times[j].Before(times[j-1]); j-- {
			times[j], times[j-1] = times[j-1], times[j]
		}
	}
}
