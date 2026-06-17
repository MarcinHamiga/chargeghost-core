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
	// txEvseResolver maps a transaction id (string) to its EVSE id.  Set via
	// SetTxEvseResolver so the manager can scope TxProfile / TxDefaultProfile
	// entries to the right EVSE even when the request didn't supply one.
	txEvseResolver func(string) (int, bool)
}

// SetTxEvseResolver installs a callback that maps a string OCPP 2.0.1
// transaction id to its owning EVSE id.  When the SetChargingProfile request
// arrives without a non-zero evseId (e.g. CSMS targets a specific transaction
// but the protocol's evseId field is zero), the manager uses this resolver to
// rewrite the profile's evseId so limit resolution can locate the profile.
func (pm *ChargingProfileManager201) SetTxEvseResolver(fn func(string) (int, bool)) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.txEvseResolver = fn
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
	// Validate TxProfile: per OCPP 2.0.1 §3.21, TxProfile MUST carry a
	// TransactionID.  Reject early so the caller can return
	// ChargingProfileStatusRejected instead of accepting a profile that would
	// never fire.  TxDefaultProfile is allowed without a TransactionID — it
	// then applies to any transaction on the EVSE.
	if profile.ChargingProfilePurpose == types.ChargingProfilePurposeTxProfile &&
		profile.TransactionID == "" {
		slog.Warn("charging profile rejected: TxProfile requires TransactionID",
			"id", profile.ID, "purpose", profile.ChargingProfilePurpose, "evseId", evseID)
		return
	}
	// ChargingStation-level profiles use evseID == 0; EVSE-level profiles use a
	// non-zero evseID. When the caller passes evseID==0 with a TxProfile we
	// rewrite it to the EVSE that owns the transaction so that limit
	// resolution can find the profile by EVSE.
	if evseID == 0 && profile.TransactionID != "" {
		// Find the EVSE for the transaction id.
		if pm.txEvseResolver != nil {
			if resolved, ok := pm.txEvseResolver(profile.TransactionID); ok {
				evseID = resolved
			}
		}
	}
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

// ClearTxProfilesForTransaction removes every TxProfile and TxDefaultProfile
// entry whose TransactionID matches the supplied transaction ID.
//
// Callers should invoke this when a transaction ends so that profiles
// scoped to that transaction do not leak into subsequent sessions on the
// same EVSE. TxDefaultProfile entries with an empty TransactionID are
// kept (they apply to any transaction).
func (pm *ChargingProfileManager201) ClearTxProfilesForTransaction(transactionID string) int {
	if transactionID == "" {
		return 0
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	cleared := 0
	for id, mp := range pm.profiles {
		if mp.profile.TransactionID != transactionID {
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
//
// activeTxID is the transaction ID currently active on the EVSE (empty if no
// transaction is running).  Per OCPP 2.0.1 §3.20:
//
//   - TxProfile applies only when activeTxID matches the profile's
//     declared TransactionID.
//   - TxDefaultProfile applies to any transaction; profiles that declare
//     a TransactionID apply to that specific transaction only, profiles
//     without one apply EVSE-wide.
func (pm *ChargingProfileManager201) GetCompositeLimit(evseID int, now time.Time, voltage float64, txStart *time.Time, phases int, activeTxID string) *float64 {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	maxLimit := pm.resolveLimit(types.ChargingProfilePurposeChargingStationMaxProfile, evseID, now, voltage, txStart, phases, "", false)
	txLimit := pm.resolveTxLimit(evseID, now, voltage, txStart, phases, activeTxID)

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

func (pm *ChargingProfileManager201) resolveTxLimit(evseID int, now time.Time, voltage float64, txStart *time.Time, phases int, activeTxID string) *float64 {
	// Per OCPP 2.0.1:
	//   - TxProfile is scoped to a specific transaction id; the profile
	//     MUST declare its TransactionID and it must match activeTxID.
	//   - TxDefaultProfile applies to transactions on the EVSE; it can
	//     either be EVSE-wide (no TransactionID) or scoped to a specific
	//     transaction that matches activeTxID.
	if l := pm.resolveLimit(types.ChargingProfilePurposeTxProfile, evseID, now, voltage, txStart, phases, activeTxID, true); l != nil {
		return l
	}
	return pm.resolveLimit(types.ChargingProfilePurposeTxDefaultProfile, evseID, now, voltage, txStart, phases, activeTxID, false)
}

// resolveLimit iterates over the manager's profiles and returns the limit for
// the given purpose.
//
// activeTxID is the transaction ID currently active on the EVSE (empty if no
// transaction is running).  requireTxIDMatch drives the OCPP 2.0.1 scoping
// rules:
//
//   - true  : TxProfile semantics — only profiles that declare a
//     TransactionID equal to activeTxID are considered.
//   - false : TxDefaultProfile / ChargingStationMaxProfile semantics —
//     profiles with no TransactionID (EVSE-wide) are always considered;
//     profiles that declare a TransactionID apply only when it matches
//     activeTxID.
func (pm *ChargingProfileManager201) resolveLimit(purpose types.ChargingProfilePurposeType, evseID int, now time.Time, voltage float64, txStart *time.Time, phases int, activeTxID string, requireTxIDMatch bool) *float64 {
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
		// Per OCPP 2.0.1 §3.20 scoping:
		//   - TxProfile (requireTxIDMatch=true): profile MUST declare a
		//     TransactionID equal to activeTxID, otherwise it does not
		//     apply to the current session.
		//   - TxDefaultProfile / ChargingStationMaxProfile
		//     (requireTxIDMatch=false): an empty TransactionID means
		//     "any transaction on the EVSE"; a non-empty TransactionID
		//     scopes it to a specific transaction that must match
		//     activeTxID.
		switch {
		case requireTxIDMatch:
			if mp.profile.TransactionID == "" || mp.profile.TransactionID != activeTxID {
				continue
			}
		default:
			if mp.profile.TransactionID != "" && mp.profile.TransactionID != activeTxID {
				continue
			}
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
		ProfileID:     mp.profile.ID,
		ConnectorID:   mp.evseID,
		StackLevel:    mp.profile.StackLevel,
		Purpose:       string(mp.profile.ChargingProfilePurpose),
		Kind:          string(mp.profile.ChargingProfileKind),
		TransactionID: mp.profile.TransactionID,
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
		if sched.MinChargingRate != nil {
			ep.Schedule.MinChargingRate = *sched.MinChargingRate
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
		TransactionID:          profile.TransactionID,
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
	if profile.Schedule.MinChargingRate > 0 {
		sched.MinChargingRate = &profile.Schedule.MinChargingRate
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
		// GetCompositeSchedule has no access to the active transaction id;
		// only EVSE-wide (TxDefaultProfile without TransactionID and
		// ChargingStationMaxProfile) profiles are reflected here.
		limit := pm.getCompositeLimitLocked(connectorID, sample, voltage, txStart, phases, "")
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

func (pm *ChargingProfileManager201) getCompositeLimitLocked(evseID int, now time.Time, voltage float64, txStart *time.Time, phases int, activeTxID string) *float64 {
	maxLimit := pm.resolveLimit(types.ChargingProfilePurposeChargingStationMaxProfile, evseID, now, voltage, txStart, phases, activeTxID, false)
	txLimit := pm.resolveTxLimit(evseID, now, voltage, txStart, phases, activeTxID)

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
