package dora_test

import (
	"slices"
	"testing"

	"github.com/ianflanagan1/dora-mcp/internal/dora"
)

func ofService(service string, i dora.Incident) dora.Incident {
	i.Service = service
	return i
}

func incidentGroupNames(groups []dora.IncidentGroup) []string {
	var names []string
	for _, g := range groups {
		names = append(names, g.Name)
	}
	return names
}

func TestIncidentsBySeverityListEverySeverityIncludingThoseWithNone(t *testing.T) {
	groups := dora.SummariseIncidents(services, dora.IncidentsBySeverity, incidents)

	if got, want := incidentGroupNames(groups), []string{"sev1", "sev2", "sev3"}; !slices.Equal(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	if groups[0].Incidents != 1 || groups[1].Incidents != 2 || groups[2].Incidents != 1 {
		t.Errorf("counts = %d, %d, %d, want 1, 2, 1", groups[0].Incidents, groups[1].Incidents, groups[2].Incidents)
	}

	none := dora.SummariseIncidents(services, dora.IncidentsBySeverity, nil)
	if len(none) != 3 || none[0].Incidents != 0 || none[0].RecoveryMedianHours != nil {
		t.Errorf("groups with no incidents = %+v, want three, empty", none)
	}
}

func TestIncidentsByTeamCountOpenAndDeploymentCausedOnesAndTakeTheMedianRecovery(t *testing.T) {
	groups := dora.SummariseIncidents(services, dora.IncidentsByTeam, []dora.Incident{
		ofService("api", incidents[0]),
		ofService("worker", incidents[2]),
		ofService("worker", incidents[1]),
		ofService("web", incidents[3]),
	})

	if got, want := incidentGroupNames(groups), []string{"Core", "Front", "Quiet"}; !slices.Equal(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	core := groups[0]
	if core.Incidents != 3 || core.Open != 1 || core.CausedByDeployment != 2 {
		t.Errorf("Core = %+v, want 3 incidents, 1 open, 2 caused by a deployment", core)
	}
	// Resolved in 3 and 8 hours.
	assertFloat(t, "Core recovery median", core.RecoveryMedianHours, 5.5)
}
