package dora_test

import (
	"testing"
	"time"

	"github.com/ianflanagan1/dora-mcp/internal/dora"
)

var week = dora.Window{
	From: at("2026-03-02T00:00:00Z"),
	To:   at("2026-03-09T00:00:00Z"),
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr[T any](v T) *T { return &v }

func deployment(id int64, deployedAt string) dora.Deployment {
	return dora.Deployment{ID: id, Service: "api", DeployedAt: at(deployedAt)}
}

func incident(causedBy int64, startedAt, resolvedAt string) dora.Incident {
	i := dora.Incident{CausedByDeploymentID: &causedBy, StartedAt: at(startedAt)}
	if resolvedAt != "" {
		i.ResolvedAt = ptr(at(resolvedAt))
	}
	return i
}

func assertFloat(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if *got != want {
		t.Errorf("%s = %v, want %v", name, *got, want)
	}
}

func TestOnlyDeploymentsInsideTheHalfOpenWindowAreCounted(t *testing.T) {
	m := dora.Calculate(week, []dora.Deployment{
		deployment(1, "2026-03-01T23:59:59Z"),
		deployment(2, "2026-03-02T00:00:00Z"),
		deployment(3, "2026-03-08T23:59:59Z"),
		deployment(4, "2026-03-09T00:00:00Z"),
	}, nil, nil)

	if m.Deployments != 2 {
		t.Errorf("Deployments = %d, want 2", m.Deployments)
	}
}

func TestDeploymentFrequencyIsDeploymentsPerWeekOfTheWindow(t *testing.T) {
	fortnight := dora.Window{From: week.From, To: week.From.AddDate(0, 0, 14)}

	m := dora.Calculate(fortnight, []dora.Deployment{
		deployment(1, "2026-03-02T10:00:00Z"),
		deployment(2, "2026-03-05T10:00:00Z"),
		deployment(3, "2026-03-12T10:00:00Z"),
	}, nil, nil)

	if m.DeploymentsPerWeek != 1.5 {
		t.Errorf("DeploymentsPerWeek = %v, want 1.5", m.DeploymentsPerWeek)
	}
}

func TestLeadTimeIsMeasuredFromCommitToTheDeploymentThatShippedIt(t *testing.T) {
	deployments := []dora.Deployment{deployment(1, "2026-03-04T12:00:00Z")}
	changes := []dora.Change{
		{DeploymentID: 1, CommittedAt: at("2026-03-04T10:00:00Z")},
		{DeploymentID: 1, CommittedAt: at("2026-03-04T08:00:00Z")},
		{DeploymentID: 1, CommittedAt: at("2026-03-03T12:00:00Z")},
	}

	m := dora.Calculate(week, deployments, changes, nil)

	if m.ChangesDeployed != 3 {
		t.Errorf("ChangesDeployed = %d, want 3", m.ChangesDeployed)
	}
	assertFloat(t, "LeadTimeMedianHours", m.LeadTimeMedianHours, 4)
	assertFloat(t, "LeadTimeP90Hours", m.LeadTimeP90Hours, 20)
}

func TestChangesShippedByDeploymentsOutsideTheWindowAreIgnored(t *testing.T) {
	deployments := []dora.Deployment{deployment(1, "2026-03-10T12:00:00Z")}
	changes := []dora.Change{{DeploymentID: 1, CommittedAt: at("2026-03-04T10:00:00Z")}}

	m := dora.Calculate(week, deployments, changes, nil)

	if m.ChangesDeployed != 0 || m.LeadTimeMedianHours != nil {
		t.Errorf("ChangesDeployed = %d, LeadTimeMedianHours = %v, want 0 and nil",
			m.ChangesDeployed, m.LeadTimeMedianHours)
	}
}

func TestChangeFailureRateIsTheShareOfDeploymentsThatCausedAnIncident(t *testing.T) {
	deployments := []dora.Deployment{
		deployment(1, "2026-03-02T10:00:00Z"),
		deployment(2, "2026-03-03T10:00:00Z"),
		deployment(3, "2026-03-04T10:00:00Z"),
		deployment(4, "2026-03-05T10:00:00Z"),
	}
	incidents := []dora.Incident{
		incident(1, "2026-03-02T11:00:00Z", "2026-03-02T12:00:00Z"),
		incident(1, "2026-03-02T13:00:00Z", "2026-03-02T14:00:00Z"),
	}

	m := dora.Calculate(week, deployments, nil, incidents)

	if m.FailedDeployments != 1 {
		t.Errorf("FailedDeployments = %d, want 1", m.FailedDeployments)
	}
	assertFloat(t, "ChangeFailureRate", m.ChangeFailureRate, 0.25)
}

func TestIncidentsNoDeploymentCausedDoNotCountAsFailures(t *testing.T) {
	deployments := []dora.Deployment{deployment(1, "2026-03-02T10:00:00Z")}
	incidents := []dora.Incident{{StartedAt: at("2026-03-02T11:00:00Z")}}

	m := dora.Calculate(week, deployments, nil, incidents)

	assertFloat(t, "ChangeFailureRate", m.ChangeFailureRate, 0)
	if m.RecoveryMedianHours != nil {
		t.Errorf("RecoveryMedianHours = %v, want nil", *m.RecoveryMedianHours)
	}
}

func TestRecoveryTimeIsTheMedianOfResolvedIncidentsAndOpenOnesAreCountedSeparately(t *testing.T) {
	deployments := []dora.Deployment{
		deployment(1, "2026-03-02T10:00:00Z"),
		deployment(2, "2026-03-03T10:00:00Z"),
	}
	incidents := []dora.Incident{
		incident(1, "2026-03-02T11:00:00Z", "2026-03-02T12:00:00Z"),
		incident(1, "2026-03-02T13:00:00Z", "2026-03-02T16:00:00Z"),
		incident(2, "2026-03-03T11:00:00Z", ""),
	}

	m := dora.Calculate(week, deployments, nil, incidents)

	if m.RecoveredIncidents != 2 || m.UnresolvedIncidents != 1 {
		t.Errorf("RecoveredIncidents = %d, UnresolvedIncidents = %d, want 2 and 1",
			m.RecoveredIncidents, m.UnresolvedIncidents)
	}
	assertFloat(t, "RecoveryMedianHours", m.RecoveryMedianHours, 2)
}

func TestReworkRateIsTheShareOfDeploymentsThatWereRemediation(t *testing.T) {
	remediation := deployment(2, "2026-03-03T10:00:00Z")
	remediation.Remediation = true

	m := dora.Calculate(week, []dora.Deployment{deployment(1, "2026-03-02T10:00:00Z"), remediation}, nil, nil)

	if m.RemediationDeployments != 1 {
		t.Errorf("RemediationDeployments = %d, want 1", m.RemediationDeployments)
	}
	assertFloat(t, "ReworkRate", m.ReworkRate, 0.5)
}

func TestRatesAreAbsentRatherThanZeroWhenThereWereNoDeployments(t *testing.T) {
	m := dora.Calculate(week, nil, nil, nil)

	if m.ChangeFailureRate != nil || m.ReworkRate != nil || m.LeadTimeMedianHours != nil {
		t.Errorf("rates = %v, %v, %v, want all nil", m.ChangeFailureRate, m.ReworkRate, m.LeadTimeMedianHours)
	}
	if m.DeploymentsPerWeek != 0 {
		t.Errorf("DeploymentsPerWeek = %v, want 0", m.DeploymentsPerWeek)
	}
}
