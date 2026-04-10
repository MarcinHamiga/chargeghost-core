package v201

import (
	"errors"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"

	engine "github.com/chargeghost/engine/internal/engine"
)

// ChargingProfileManager201 manages OCPP 2.0.1 charging profiles.
// It stores profiles in the 2.0.1 format (EVSE-level, string transactionId).
type ChargingProfileManager201 struct {
	mu         sync.RWMutex
	profiles   map[int]managedProfile // keyed by profile ID
	persistDir string
}

type managedProfile struct {
	evseID  int
	profile types.ChargingProfile
}

func NewChargingProfileManager201() *ChargingProfileManager201 {
	return &ChargingProfileManager201{
		profiles: make(map[int]managedProfile),
	}
}

func (pm *ChargingProfileManager201) SetProfile(evseID int, profile types.ChargingProfile) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.profiles[profile.ID] = managedProfile{
		evseID:  evseID,
		profile: profile,
	}
	go pm.autoSave()
	slog.Info("charging profile set", "id", profile.ID, "evseId", evseID, "purpose", profile.ChargingProfilePurpose, "stackLevel", profile.StackLevel)
}

func (pm *ChargingProfileManager201) ClearProfile(profileID *int, evseID *int, purpose *types.ChargingProfilePurposeType, stackLevel *int) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	cleared := 0
	for id, mp := range pm.profiles {
		if profileID != nil && id != *profileID {
			continue
		}
		if evseID != nil && mp.evseID != *evseID {
			continue
		}
		if purpose != nil && mp.profile.ChargingProfilePurpose != *purpose {
			continue
		}
		if stackLevel != nil && mp.profile.StackLevel != *stackLevel {
			continue
		}
		delete(pm.profiles, id)
		cleared++
	}
	if cleared > 0 {
		go pm.autoSave()
	}
	return cleared
}

func (pm *ChargingProfileManager201) GetFilteredProfiles(evseID *int, profileIDs []int, purpose *types.ChargingProfilePurposeType, stackLevel *int) []types.ChargingProfile {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	result := make([]types.ChargingProfile, 0)
	for _, mp := range pm.profiles {
		if evseID != nil && mp.evseID != *evseID {
			continue
		}
		if len(profileIDs) > 0 {
			found := false
			for _, id := range profileIDs {
				if mp.profile.ID == id {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if purpose != nil && mp.profile.ChargingProfilePurpose != *purpose {
			continue
		}
		if stackLevel != nil && mp.profile.StackLevel != *stackLevel {
			continue
		}
		result = append(result, mp.profile)
	}
	return result
}

func (pm *ChargingProfileManager201) GetAllProfiles() []types.ChargingProfile {
	return pm.GetFilteredProfiles(nil, nil, nil, nil)
}

// GetCompositeLimit returns the effective current limit in Amps for the given evseID
// at the current time, or nil if no profiles apply. Used to wire engine.GetLimit.
func (pm *ChargingProfileManager201) GetCompositeLimit(evseID int, now time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	maxLimit := pm.resolveLimit(types.ChargingProfilePurposeChargingStationMaxProfile, evseID, now, voltage, txStart, phases)
	txLimit := pm.resolveTxLimit(evseID, now, voltage, txStart, phases)

	if maxLimit == nil && txLimit == nil {
		return nil
	}
	if maxLimit == nil {
		return txLimit
	}
	if txLimit == nil {
		return maxLimit
	}
	v := min(*maxLimit, *txLimit)
	return &v
}

func (pm *ChargingProfileManager201) resolveTxLimit(evseID int, now time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
	if l := pm.resolveLimit(types.ChargingProfilePurposeTxProfile, evseID, now, voltage, txStart, phases); l != nil {
		return l
	}
	return pm.resolveLimit(types.ChargingProfilePurposeTxDefaultProfile, evseID, now, voltage, txStart, phases)
}

func (pm *ChargingProfileManager201) resolveLimit(purpose types.ChargingProfilePurposeType, evseID int, now time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
	var best *managedProfile
	for i := range pm.profiles {
		mp := pm.profiles[i]
		if mp.profile.ChargingProfilePurpose != purpose {
			continue
		}
		if mp.evseID != evseID && mp.evseID != 0 {
			continue
		}
		if mp.profile.ValidFrom != nil && now.Before(mp.profile.ValidFrom.Time) {
			continue
		}
		if mp.profile.ValidTo != nil && now.After(mp.profile.ValidTo.Time) {
			continue
		}
		bmp := mp
		if best == nil || mp.profile.StackLevel > best.profile.StackLevel {
			best = &bmp
		}
	}
	if best == nil {
		return nil
	}
	return pm.limitFromProfile(best.profile, now, voltage, txStart, phases)
}

func (pm *ChargingProfileManager201) limitFromProfile(p types.ChargingProfile, now time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
	if len(p.ChargingSchedule) == 0 {
		return nil
	}
	sched := p.ChargingSchedule[0]
	schedStart := pm.scheduleStart(p, sched, txStart)
	if schedStart == nil {
		return nil
	}
	elapsed := now.Sub(*schedStart).Seconds()
	if elapsed < 0 {
		return nil
	}
	if p.ChargingProfileKind == types.ChargingProfileKindRecurring {
		cycleLen := 86400.0
		if p.RecurrencyKind == types.RecurrencyKindWeekly {
			cycleLen = 604800.0
		}
		elapsed = math.Mod(elapsed, cycleLen)
	}
	var active *types.ChargingSchedulePeriod
	for i := range sched.ChargingSchedulePeriod {
		if float64(sched.ChargingSchedulePeriod[i].StartPeriod) <= elapsed {
			active = &sched.ChargingSchedulePeriod[i]
		}
	}
	if active == nil {
		return nil
	}
	limit := active.Limit
	if sched.ChargingRateUnit == types.ChargingRateUnitWatts && voltage > 0 && phases > 0 {
		limit = limit / (voltage * float64(phases))
	}
	return &limit
}

func (pm *ChargingProfileManager201) scheduleStart(p types.ChargingProfile, sched types.ChargingSchedule, txStart *time.Time) *time.Time {
	switch p.ChargingProfileKind {
	case types.ChargingProfileKindAbsolute, types.ChargingProfileKindRecurring:
		if sched.StartSchedule != nil {
			t := sched.StartSchedule.Time
			return &t
		}
	case types.ChargingProfileKindRelative:
		return txStart
	}
	return nil
}

// --- ChargingProfileManagerAPI implementation (version-agnostic REST API surface) ---

// toEngineProfile converts a v201 managedProfile to the engine's ChargingProfile type.
func toEngineProfile(mp managedProfile) engine.ChargingProfile {
	ep := engine.ChargingProfile{
		ProfileID:   mp.profile.ID,
		ConnectorID: mp.evseID,
		StackLevel:  mp.profile.StackLevel,
		Purpose:     string(mp.profile.ChargingProfilePurpose),
		Kind:        string(mp.profile.ChargingProfileKind),
	}
	if mp.profile.RecurrencyKind != "" {
		ep.RecurrencyKind = string(mp.profile.RecurrencyKind)
	}
	if mp.profile.ValidFrom != nil {
		t := mp.profile.ValidFrom.Time
		ep.ValidFrom = &t
	}
	if mp.profile.ValidTo != nil {
		t := mp.profile.ValidTo.Time
		ep.ValidTo = &t
	}
	if len(mp.profile.ChargingSchedule) > 0 {
		sched := mp.profile.ChargingSchedule[0]
		ep.Schedule = engine.ChargingSchedule{
			ChargingRateUnit: string(sched.ChargingRateUnit),
		}
		if sched.Duration != nil {
			ep.Schedule.Duration = *sched.Duration
		}
		if sched.StartSchedule != nil {
			t := sched.StartSchedule.Time
			ep.StartSchedule = &t
			ep.Schedule.StartSchedule = &t
		}
		for _, sp := range sched.ChargingSchedulePeriod {
			ep.Schedule.Periods = append(ep.Schedule.Periods, engine.ChargingSchedulePeriod{
				StartPeriod:  sp.StartPeriod,
				Limit:        sp.Limit,
				NumberPhases: sp.NumberPhases,
			})
		}
	}
	return ep
}

func (pm *ChargingProfileManager201) GetChargingProfiles() []engine.ChargingProfile {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	result := make([]engine.ChargingProfile, 0, len(pm.profiles))
	for _, mp := range pm.profiles {
		result = append(result, toEngineProfile(mp))
	}
	return result
}

func (pm *ChargingProfileManager201) GetChargingProfile(connectorID, profileID int) (engine.ChargingProfile, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	mp, ok := pm.profiles[profileID]
	if !ok || mp.evseID != connectorID {
		return engine.ChargingProfile{}, false
	}
	return toEngineProfile(mp), true
}

func (pm *ChargingProfileManager201) SetChargingProfile(connectorID int, profile engine.ChargingProfile) error {
	p := types.ChargingProfile{
		ID:                     profile.ProfileID,
		StackLevel:             profile.StackLevel,
		ChargingProfilePurpose: types.ChargingProfilePurposeType(profile.Purpose),
		ChargingProfileKind:    types.ChargingProfileKindType(profile.Kind),
	}
	if profile.RecurrencyKind != "" {
		p.RecurrencyKind = types.RecurrencyKindType(profile.RecurrencyKind)
	}
	if profile.ValidFrom != nil {
		p.ValidFrom = types.NewDateTime(*profile.ValidFrom)
	}
	if profile.ValidTo != nil {
		p.ValidTo = types.NewDateTime(*profile.ValidTo)
	}
	sched := types.ChargingSchedule{
		ID:               profile.ProfileID,
		ChargingRateUnit: types.ChargingRateUnitType(profile.Schedule.ChargingRateUnit),
	}
	if profile.Schedule.Duration > 0 {
		sched.Duration = &profile.Schedule.Duration
	}
	if profile.StartSchedule != nil {
		sched.StartSchedule = types.NewDateTime(*profile.StartSchedule)
	}
	for _, sp := range profile.Schedule.Periods {
		sched.ChargingSchedulePeriod = append(sched.ChargingSchedulePeriod, types.ChargingSchedulePeriod{
			StartPeriod:  sp.StartPeriod,
			Limit:        sp.Limit,
			NumberPhases: sp.NumberPhases,
		})
	}
	p.ChargingSchedule = []types.ChargingSchedule{sched}
	pm.SetProfile(connectorID, p)
	return nil
}

func (pm *ChargingProfileManager201) ClearChargingProfile(connectorID, profileID *int, purpose, stackLevel *string) error {
	var purpose201 *types.ChargingProfilePurposeType
	if purpose != nil {
		p := types.ChargingProfilePurposeType(*purpose)
		purpose201 = &p
	}
	var sl *int
	if stackLevel != nil {
		if n, err := strconv.Atoi(*stackLevel); err == nil {
			sl = &n
		}
	}
	pm.ClearProfile(profileID, connectorID, purpose201, sl)
	return nil
}

func (pm *ChargingProfileManager201) GetCompositeSchedule(connectorID, txID int, now time.Time, duration int, voltage float64, txStart *time.Time, phases int) ([]engine.ChargingSchedulePeriod, error) {
	return nil, errors.New("composite schedule is not supported for OCPP 2.0.1")
}
