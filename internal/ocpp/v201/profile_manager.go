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
	profiles map[int]managedProfile // keyed by profile ID
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
