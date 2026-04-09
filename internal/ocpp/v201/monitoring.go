package v201

import (
	"fmt"
	"sync"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
)

type MonitorType string

const (
	MonitorTypeUpperThreshold MonitorType = "UpperThreshold"
	MonitorTypeLowerThreshold MonitorType = "LowerThreshold"
	MonitorTypeDelta          MonitorType = "Delta"
	MonitorTypePeriodic       MonitorType = "Periodic"
)

type Monitor struct {
	ID        int
	Component string
	Instance  string
	EVSEID    int
	Variable  string
	Type      MonitorType
	Value     float64
	Severity  int
}

type MonitoringManager struct {
	mu         sync.RWMutex
	monitors   map[int]*Monitor
	nextID     int
	model      *DeviceModel
	persistDir string
}

func NewMonitoringManager(model *DeviceModel) *MonitoringManager {
	return &MonitoringManager{
		monitors: make(map[int]*Monitor),
		model:    model,
	}
}

func (mm *MonitoringManager) AddMonitor(component, instance string, evseID int, variable string, monitorType MonitorType, value float64, severity int) (int, error) {
	result := mm.model.GetVariable(component, instance, evseID, variable)
	if result.Status != provisioning.GetVariableStatusAccepted {
		return 0, fmt.Errorf("unknown variable %s.%s (evse=%d).%s", component, instance, evseID, variable)
	}

	mm.mu.Lock()
	defer mm.mu.Unlock()

	mm.nextID++
	m := &Monitor{
		ID:        mm.nextID,
		Component: component,
		Instance:  instance,
		EVSEID:    evseID,
		Variable:  variable,
		Type:      monitorType,
		Value:     value,
		Severity:  severity,
	}
	mm.monitors[mm.nextID] = m
	go mm.autoSave()
	return mm.nextID, nil
}

func (mm *MonitoringManager) ClearMonitor(id int) bool {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	_, ok := mm.monitors[id]
	if ok {
		delete(mm.monitors, id)
		go mm.autoSave()
	}
	return ok
}

func (mm *MonitoringManager) GetAllMonitors() []*Monitor {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	result := make([]*Monitor, 0, len(mm.monitors))
	for _, m := range mm.monitors {
		result = append(result, m)
	}
	return result
}
