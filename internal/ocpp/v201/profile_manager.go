package v201

import (
	"log/slog"
	"math"
	"sort"
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
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if duration <= 0 {
		return nil, nil
	}

	end := now.Add(time.Duration(duration) * time.Second)
	boundaries := []time.Time{now, end}
	for _, mp := range pm.profiles {
		if mp.evseID != connectorID && mp.evseID != 0 {
			continue
		}
		if mp.profile.ValidFrom != nil {
			boundaries = appendBoundary(boundaries, mp.profile.ValidFrom.Time, now, end)
		}
		if mp.profile.ValidTo != nil {
			boundaries = appendBoundary(boundaries, mp.profile.ValidTo.Time, now, end)
		}
		boundaries = append(boundaries, pm.scheduleBoundaries(mp.profile, now, end, txStart)...)
	}

	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Before(boundaries[j]) })
	boundaries = dedupeBoundaries(boundaries)

	periods := make([]engine.ChargingSchedulePeriod, 0)
	var lastLimit *float64
	for i := 0; i < len(boundaries)-1; i++ {
		sample := boundaries[i]
		if !sample.Before(end) {
			continue
		}
		limit := pm.getCompositeLimitLocked(connectorID, sample, voltage, txStart, phases)
		if limit == nil {
			lastLimit = nil
			continue
		}
		if lastLimit != nil && *lastLimit == *limit {
			continue
		}
		value := *limit
		lastLimit = &value
		periods = append(periods, engine.ChargingSchedulePeriod{
			StartPeriod: int(sample.Sub(now).Seconds()),
			Limit:       value,
		})
	}
	return periods, nil
}

func (pm *ChargingProfileManager201) getCompositeLimitLocked(evseID int, now time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
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

func appendBoundary(boundaries []time.Time, candidate, start, end time.Time) []time.Time {
	if candidate.Before(start) || candidate.After(end) {
		return boundaries
	}
	return append(boundaries, candidate)
}

func dedupeBoundaries(boundaries []time.Time) []time.Time {
	if len(boundaries) == 0 {
		return boundaries
	}
	result := []time.Time{boundaries[0]}
	for _, candidate := range boundaries[1:] {
		if candidate.Equal(result[len(result)-1]) {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func (pm *ChargingProfileManager201) scheduleBoundaries(profile types.ChargingProfile, start, end time.Time, txStart *time.Time) []time.Time {
	if len(profile.ChargingSchedule) == 0 {
		return nil
	}
	sched := profile.ChargingSchedule[0]
	schedStart := pm.scheduleStart(profile, sched, txStart)
	if schedStart == nil {
		return nil
	}

	if profile.ChargingProfileKind == types.ChargingProfileKindRecurring {
		return recurringScheduleBoundaries(profile, sched, *schedStart, start, end)
	}
	return oneShotScheduleBoundaries(sched, *schedStart, start, end)
}

func oneShotScheduleBoundaries(sched types.ChargingSchedule, schedStart, start, end time.Time) []time.Time {
	boundaries := make([]time.Time, 0, len(sched.ChargingSchedulePeriod)+1)
	for _, period := range sched.ChargingSchedulePeriod {
		boundary := schedStart.Add(time.Duration(period.StartPeriod) * time.Second)
		boundaries = appendBoundary(boundaries, boundary, start, end)
	}
	if sched.Duration != nil {
		boundary := schedStart.Add(time.Duration(*sched.Duration) * time.Second)
		boundaries = appendBoundary(boundaries, boundary, start, end)
	}
	return boundaries
}

func recurringScheduleBoundaries(profile types.ChargingProfile, sched types.ChargingSchedule, schedStart, start, end time.Time) []time.Time {
	cycle := 24 * time.Hour
	if profile.RecurrencyKind == types.RecurrencyKindWeekly {
		cycle = 7 * 24 * time.Hour
	}

	cycleStart := schedStart
	if cycleStart.Before(start) {
		elapsed := start.Sub(cycleStart)
		cycleStart = cycleStart.Add(time.Duration(int64(elapsed/cycle)) * cycle)
		for cycleStart.Add(cycle).Before(start) || cycleStart.Add(cycle).Equal(start) {
			cycleStart = cycleStart.Add(cycle)
		}
	}
	for cycleStart.After(start) {
		cycleStart = cycleStart.Add(-cycle)
	}

	boundaries := make([]time.Time, 0)
	for current := cycleStart; current.Before(end); current = current.Add(cycle) {
		boundaries = append(boundaries, oneShotScheduleBoundaries(sched, current, start, end)...)
		cycleEnd := current.Add(cycle)
		boundaries = appendBoundary(boundaries, cycleEnd, start, end)
	}
	return boundaries
}
