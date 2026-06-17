package v201

import (
	"log/slog"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"

	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
)

// maxNotifyReportParts caps the number of ReportData entries sent per
// NotifyReport message.  OCPP 2.0.1 §3.21 doesn't enforce a hard limit
// but practical implementations chunk to ~100 entries.
const maxNotifyReportParts = 100

// sendFullReport sends a NotifyReport for all device model variables,
// chunking across multiple messages when the report exceeds the part
// size.  Per OCPP 2.0.1 §3.21:
//
//   - requestID echoes the GetBaseReport/GetReport requestId (0 if nil).
//   - reportData is filtered by componentCriteria and componentVariable
//     when provided (i.e. when invoked from GetReport).
//   - tbc=true on every chunk except the last.
func (b *Bridge201) sendFullReport(requestID int, _ provisioning.ReportBaseType, criteria []provisioning.ComponentCriterion, componentVars []ocpp201types.ComponentVariable) {
	all := b.deviceModel.BuildNotifyReportData()
	filtered := filterReportData(all, criteria, componentVars)
	if len(filtered) == 0 {
		// Per OCPP 2.0.1 §3.21 we must still send a NotifyReport
		// (with an empty reportData) so the CSMS knows the request
		// was processed.
		b.sendNotifyReportChunk(requestID, 0, false, nil)
		return
	}

	seq := 0
	for start := 0; start < len(filtered); start += maxNotifyReportParts {
		end := start + maxNotifyReportParts
		if end > len(filtered) {
			end = len(filtered)
		}
		chunk := filtered[start:end]
		tbc := end < len(filtered)
		b.sendNotifyReportChunk(requestID, seq, tbc, chunk)
		seq++
	}
}

// sendNotifyReportChunk enqueues a single NotifyReportRequest via the
// command dispatcher.  Errors are logged; the OCPP client will retry
// from the offline queue if delivery fails.
func (b *Bridge201) sendNotifyReportChunk(requestID, seqNo int, tbc bool, reportData []provisioning.ReportData) {
	now := ocpp201types.NewDateTime(time.Now().UTC())
	req := provisioning.NewNotifyReportRequest(requestID, now, seqNo)
	req.Tbc = tbc
	req.ReportData = reportData
	b.dispatcher.Enqueue(ocpppkg.OCPPCommand{
		Description: "NotifyReport",
		Execute: func() error {
			_, err := b.cs.SendRequest(req)
			return err
		},
	})
	slog.Info("OCPP 2.0.1 NotifyReport queued", "requestId", requestID, "seqNo", seqNo, "tbc", tbc, "entries", len(reportData))
}

// filterReportData applies ComponentCriteria (Active / Available /
// Enabled / Problem) and the ComponentVariable list to the report.
//
// ComponentCriteria values are advisory and the OCPP 2.0.1 spec allows
// us to return all matching components when the criteria don't fully
// apply to our model.  We implement them as follows:
//   - Active / Enabled: include all components (we have no notion of
//     disabled components other than the availability flag).
//   - Available: same.
//   - Problem: include components whose current value indicates an
//     error / faulted state.  We surface the EvCharger/Availability
//     variable when it is Faulted.
//
// ComponentVariable is a precise filter: only variables whose
// (component.name, component.instance, component.evse.id, variable.name)
// match a requested entry are included.
func filterReportData(all []provisioning.ReportData, criteria []provisioning.ComponentCriterion, componentVars []ocpp201types.ComponentVariable) []provisioning.ReportData {
	if len(componentVars) == 0 && len(criteria) == 0 {
		return all
	}

	var componentMatch, variableMatch func(ocpp201types.Component, ocpp201types.Variable) bool
	if len(componentVars) > 0 {
		componentMatch = func(c ocpp201types.Component, _ ocpp201types.Variable) bool {
			for _, cv := range componentVars {
				if componentMatches(c, cv.Component) {
					return true
				}
			}
			return false
		}
		variableMatch = func(_ ocpp201types.Component, v ocpp201types.Variable) bool {
			for _, cv := range componentVars {
				if cv.Variable.Name == "" || cv.Variable.Name == v.Name {
					return true
				}
			}
			return false
		}
	}

	filtered := make([]provisioning.ReportData, 0, len(all))
	for _, rd := range all {
		if componentMatch != nil && !componentMatch(rd.Component, rd.Variable) {
			continue
		}
		if variableMatch != nil && !variableMatch(rd.Component, rd.Variable) {
			continue
		}
		filtered = append(filtered, rd)
	}

	if len(criteria) == 0 {
		return filtered
	}

	// Apply problem filter: only include components reporting a problem.
	problemOnly := false
	for _, c := range criteria {
		if c == provisioning.ComponentCriterionProblem {
			problemOnly = true
			break
		}
	}
	if !problemOnly {
		return filtered
	}

	problemOnlyFiltered := make([]provisioning.ReportData, 0, len(filtered))
	for _, rd := range filtered {
		if rd.Component.Name == "EvCharger" && rd.Variable.Name == "Availability" {
			for _, attr := range rd.VariableAttribute {
				if attr.Value == "Faulted" {
					problemOnlyFiltered = append(problemOnlyFiltered, rd)
					break
				}
			}
			continue
		}
		problemOnlyFiltered = append(problemOnlyFiltered, rd)
	}
	return problemOnlyFiltered
}

// componentMatches returns true when two Components refer to the same
// logical component (matching Name, Instance, and EVSE id when set).
func componentMatches(a, b ocpp201types.Component) bool {
	if a.Name != b.Name {
		return false
	}
	if a.Instance != b.Instance {
		return false
	}
	if (a.EVSE == nil) != (b.EVSE == nil) {
		return false
	}
	if a.EVSE != nil && b.EVSE != nil && a.EVSE.ID != b.EVSE.ID {
		return false
	}
	return true
}
