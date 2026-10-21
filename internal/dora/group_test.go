package dora_test

import (
	"slices"
	"testing"

	"github.com/ianflanagan1/dora-mcp/internal/dora"
)

var services = []dora.Service{
	{Name: "web", Team: "Front"},
	{Name: "api", Team: "Core"},
	{Name: "worker", Team: "Core"},
	{Name: "idle", Team: "Quiet"},
}

func deploymentOf(service string, id int64, deployedAt string) dora.Deployment {
	return dora.Deployment{ID: id, Service: service, DeployedAt: at(deployedAt)}
}

// groupRecords has api fail one deployment of two, worker fail its only one
// and web fail none.
var groupDeployments = []dora.Deployment{
	deploymentOf("api", 1, "2026-03-02T10:00:00Z"),
	deploymentOf("api", 2, "2026-03-03T10:00:00Z"),
	deploymentOf("worker", 3, "2026-03-04T10:00:00Z"),
	deploymentOf("web", 4, "2026-03-05T10:00:00Z"),
}

var groupIncidents = []dora.Incident{
	incident(1, "2026-03-02T11:00:00Z", "2026-03-02T12:00:00Z"),
	incident(3, "2026-03-04T11:00:00Z", "2026-03-04T15:00:00Z"),
}

func groupNames(groups []dora.GroupMetrics) []string {
	var names []string
	for _, g := range groups {
		names = append(names, g.Name)
	}
	return names
}

func TestMetricsByServiceAreCalculatedForEachServiceAlone(t *testing.T) {
	groups := dora.CalculateByGroup(week, services, dora.GroupByService, groupDeployments, nil, groupIncidents)

	if got, want := groupNames(groups), []string{"api", "idle", "web", "worker"}; !slices.Equal(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	assertFloat(t, "api change failure rate", groups[0].ChangeFailureRate, 0.5)
	assertFloat(t, "web change failure rate", groups[2].ChangeFailureRate, 0)
	assertFloat(t, "worker recovery", groups[3].RecoveryMedianHours, 4)
}

func TestMetricsByTeamPoolTheDeploymentsOfEachTeamsServices(t *testing.T) {
	groups := dora.CalculateByGroup(week, services, dora.GroupByTeam, groupDeployments, nil, groupIncidents)

	if got, want := groupNames(groups), []string{"Core", "Front", "Quiet"}; !slices.Equal(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	core := groups[0]
	if core.Deployments != 3 || core.FailedDeployments != 2 {
		t.Errorf("Core deployments = %d, failed = %d, want 3 and 2", core.Deployments, core.FailedDeployments)
	}
	assertFloat(t, "Core change failure rate", core.ChangeFailureRate, 2.0/3)
}

func TestAGroupWithNoDeploymentsIsListedWithItsRatesAbsent(t *testing.T) {
	groups := dora.CalculateByGroup(week, services, dora.GroupByTeam, groupDeployments, nil, groupIncidents)

	quiet := groups[2]
	if quiet.Deployments != 0 || quiet.ChangeFailureRate != nil {
		t.Errorf("Quiet deployments = %d, change failure rate = %v, want 0 and nil",
			quiet.Deployments, quiet.ChangeFailureRate)
	}
}
