package main

import (
	"strconv"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"

	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	v16 "github.com/chargeghost/engine/internal/ocpp/v16"
	v201 "github.com/chargeghost/engine/internal/ocpp/v201"
)

func newV16LocalSessionAdmission(configKeys *v16.ConfigKeyManager, localAuth ocpppkg.LocalAuthManager, authCache ocpppkg.AuthorizationCacheStore, connected func() bool) func(*string) error {
	return func(idTag *string) error {
		return ocpppkg.AdmitLocalSession(idTag, connected(), configKeys.GetLocalAuthListEnabled(), localAuth, configKeys.GetAuthorizationCacheEnabled(), authCache, time.Now())
	}
}

func newV201LocalSessionAdmission(deviceModel *v201.DeviceModel, localAuth ocpppkg.LocalAuthManager, authCache ocpppkg.AuthorizationCacheStore, connected func() bool) func(*string) error {
	return func(idTag *string) error {
		authEnabled := deviceModelBool(deviceModel, "AuthCtrlr", "Enabled", true)
		localOfflineEnabled := deviceModelBool(deviceModel, "AuthCtrlr", "LocalAuthorizeOffline", true)
		return ocpppkg.AdmitLocalSession(idTag, connected(), authEnabled && localOfflineEnabled, localAuth, authEnabled && localOfflineEnabled, authCache, time.Now())
	}
}

func deviceModelBool(deviceModel *v201.DeviceModel, component, variable string, fallback bool) bool {
	result := deviceModel.GetVariable(component, "", 0, variable)
	if result.Status != provisioning.GetVariableStatusAccepted {
		return fallback
	}
	value, err := strconv.ParseBool(result.Value)
	if err != nil {
		return fallback
	}
	return value
}
