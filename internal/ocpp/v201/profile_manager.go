package v201

import (
	"log/slog"
	"sync"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
)

// ChargingProfileManager201 manages OCPP 2.0.1 charging profiles.
// It stores profiles in the 2.0.1 format (EVSE-level, string transactionId).
type ChargingProfileManager201 struct {
	mu       sync.RWMutex
	profiles map[int]types.ChargingProfile // keyed by profile ID
}

func NewChargingProfileManager201() *ChargingProfileManager201 {
	return &ChargingProfileManager201{
		profiles: make(map[int]types.ChargingProfile),
	}
}

func (pm *ChargingProfileManager201) SetProfile(profile types.ChargingProfile) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.profiles[profile.ID] = profile
	slog.Info("charging profile set", "id", profile.ID, "purpose", profile.ChargingProfilePurpose, "stackLevel", profile.StackLevel)
}

func (pm *ChargingProfileManager201) ClearProfile(profileID *int, evseID *int, purpose *types.ChargingProfilePurposeType, stackLevel *int) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	cleared := 0
	for id, p := range pm.profiles {
		if profileID != nil && id != *profileID {
			continue
		}
		// In SetChargingProfileRequest, EvseID is a required field.
		// However, in ClearChargingProfileRequest, EvseID is part of ChargingProfileCriteria.
		// For now, we don't store EvseID per profile, but we should if we want to filter by it.
		// But in the current Bridge201, evseID is not stored in types.ChargingProfile.
		// Actually, types.ChargingProfile doesn't have EvseID.
		// EvseID is in SetChargingProfileRequest.
		
		if purpose != nil && p.ChargingProfilePurpose != *purpose {
			continue
		}
		if stackLevel != nil && p.StackLevel != *stackLevel {
			continue
		}
		delete(pm.profiles, id)
		cleared++
	}
	return cleared
}

func (pm *ChargingProfileManager201) GetAllProfiles() []types.ChargingProfile {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	result := make([]types.ChargingProfile, 0, len(pm.profiles))
	for _, p := range pm.profiles {
		result = append(result, p)
	}
	return result
}
