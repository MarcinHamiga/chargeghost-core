package v201

import (
	"testing"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleReportData() []provisioning.ReportData {
	return []provisioning.ReportData{
		{
			Component: ocpp201types.Component{Name: "EvCharger"},
			Variable:  ocpp201types.Variable{Name: "Availability"},
			VariableAttribute: []provisioning.VariableAttribute{
				{Value: "Operative", Mutability: provisioning.MutabilityReadOnly},
			},
		},
		{
			Component: ocpp201types.Component{Name: "OCPPCommCtrlr"},
			Variable:  ocpp201types.Variable{Name: "HeartbeatInterval"},
			VariableAttribute: []provisioning.VariableAttribute{
				{Value: "300", Mutability: provisioning.MutabilityReadWrite},
			},
		},
		{
			Component: ocpp201types.Component{Name: "EvCharger", EVSE: &ocpp201types.EVSE{ID: 1}},
			Variable:  ocpp201types.Variable{Name: "Availability"},
			VariableAttribute: []provisioning.VariableAttribute{
				{Value: "Operative", Mutability: provisioning.MutabilityReadOnly},
			},
		},
	}
}

func TestFilterReportData_NoFilters_ReturnsAll(t *testing.T) {
	data := sampleReportData()
	got := filterReportData(data, nil, nil)
	assert.Len(t, got, 3)
}

func TestFilterReportData_ComponentVariable_FiltersByComponent(t *testing.T) {
	data := sampleReportData()
	cv := []ocpp201types.ComponentVariable{
		{Component: ocpp201types.Component{Name: "EvCharger"}, Variable: ocpp201types.Variable{Name: ""}},
	}
	got := filterReportData(data, nil, cv)
	// Should match only the station-level EvCharger entry (no EVSE).
	// The EVSE-scoped EvCharger entry does not match because the
	// filter does not specify an EVSE.
	require.Len(t, got, 1)
	assert.Equal(t, "EvCharger", got[0].Component.Name)
	assert.Nil(t, got[0].Component.EVSE)
}

func TestFilterReportData_ComponentVariable_FiltersByEVSE(t *testing.T) {
	data := sampleReportData()
	cv := []ocpp201types.ComponentVariable{
		{Component: ocpp201types.Component{Name: "EvCharger", EVSE: &ocpp201types.EVSE{ID: 1}}, Variable: ocpp201types.Variable{Name: "Availability"}},
	}
	got := filterReportData(data, nil, cv)
	require.Len(t, got, 1)
	assert.Equal(t, "EvCharger", got[0].Component.Name)
	require.NotNil(t, got[0].Component.EVSE)
	assert.Equal(t, 1, got[0].Component.EVSE.ID)
}

func TestFilterReportData_ProblemCriteria_IncludesAllWithoutFault(t *testing.T) {
	data := sampleReportData()
	got := filterReportData(data, []provisioning.ComponentCriterion{provisioning.ComponentCriterionProblem}, nil)
	// No Faulted availability values present.  The implementation
	// drops EvCharger/Availability entries that are not Faulted; all
	// other entries are kept.  OCPPCommCtrlr is included; both
	// EvCharger/Availability Operative entries are excluded.
	require.Len(t, got, 1)
	assert.Equal(t, "OCPPCommCtrlr", got[0].Component.Name)
}

func TestFilterReportData_ProblemCriteria_ExcludesNonFaultedEvCharger(t *testing.T) {
	data := []provisioning.ReportData{
		{
			Component: ocpp201types.Component{Name: "EvCharger"},
			Variable:  ocpp201types.Variable{Name: "Availability"},
			VariableAttribute: []provisioning.VariableAttribute{
				{Value: "Operative"},
			},
		},
		{
			Component: ocpp201types.Component{Name: "EvCharger", EVSE: &ocpp201types.EVSE{ID: 1}},
			Variable:  ocpp201types.Variable{Name: "Availability"},
			VariableAttribute: []provisioning.VariableAttribute{
				{Value: "Faulted"},
			},
		},
		{
			Component: ocpp201types.Component{Name: "OCPPCommCtrlr"},
			Variable:  ocpp201types.Variable{Name: "HeartbeatInterval"},
			VariableAttribute: []provisioning.VariableAttribute{
				{Value: "300"},
			},
		},
	}
	got := filterReportData(data, []provisioning.ComponentCriterion{provisioning.ComponentCriterionProblem}, nil)
	require.Len(t, got, 2)
	// First entry: EvCharger (no EVSE) and Availability Operative → excluded.
	// Second entry: EvCharger (EVSE 1) and Availability Faulted → included.
	// Third entry: not an EvCharger/Availability → included by default.
	for _, rd := range got {
		if rd.Component.Name == "EvCharger" {
			require.NotNil(t, rd.Component.EVSE)
			assert.Equal(t, "Faulted", rd.VariableAttribute[0].Value)
		}
	}
}

func TestComponentMatches(t *testing.T) {
	a := ocpp201types.Component{Name: "EvCharger"}
	b := ocpp201types.Component{Name: "EvCharger"}
	assert.True(t, componentMatches(a, b))

	c := ocpp201types.Component{Name: "EvCharger", Instance: "main"}
	d := ocpp201types.Component{Name: "EvCharger", Instance: "spare"}
	assert.False(t, componentMatches(c, d))

	e := ocpp201types.Component{Name: "EvCharger", EVSE: &ocpp201types.EVSE{ID: 1}}
	f := ocpp201types.Component{Name: "EvCharger", EVSE: &ocpp201types.EVSE{ID: 2}}
	assert.False(t, componentMatches(e, f))

	// nil vs non-nil EVSE should not match.
	g := ocpp201types.Component{Name: "EvCharger"}
	h := ocpp201types.Component{Name: "EvCharger", EVSE: &ocpp201types.EVSE{ID: 0}}
	assert.False(t, componentMatches(g, h))
}
