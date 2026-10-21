package seed_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/ianflanagan1/dora-mcp/internal/dora"
	"github.com/ianflanagan1/dora-mcp/internal/seed"
)

var year = dora.Window{
	From: time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC),
	To:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
}

func TestTheSameSeedProducesTheSameRecords(t *testing.T) {
	if !reflect.DeepEqual(seed.Generate(1, year), seed.Generate(1, year)) {
		t.Error("two sets of records from seed 1 differ")
	}
}

func TestDifferentSeedsProduceDifferentRecords(t *testing.T) {
	if reflect.DeepEqual(seed.Generate(1, year), seed.Generate(2, year)) {
		t.Error("records from seeds 1 and 2 are identical")
	}
}

func TestRecordsFallInsideTheWindow(t *testing.T) {
	r := seed.Generate(1, year)

	inside := func(at time.Time) bool { return !at.Before(year.From) && at.Before(year.To) }
	for _, d := range r.Deployments {
		if !inside(d.DeployedAt) {
			t.Errorf("deployment %d at %v is outside the window", d.ID, d.DeployedAt)
		}
	}
	for _, i := range r.Incidents {
		if !inside(i.StartedAt) || (i.ResolvedAt != nil && !inside(*i.ResolvedAt)) {
			t.Errorf("incident %d runs outside the window", i.ID)
		}
	}
}

func TestRecordsReferToEachOtherConsistently(t *testing.T) {
	r := seed.Generate(1, year)
	services := map[string]bool{}
	for _, s := range r.Services {
		services[s.Name] = true
	}
	deployments := map[int64]dora.Deployment{}
	for _, d := range r.Deployments {
		if _, dup := deployments[d.ID]; dup {
			t.Errorf("deployment id %d is used twice", d.ID)
		}
		if !services[d.Service] {
			t.Errorf("deployment %d is for unknown service %q", d.ID, d.Service)
		}
		deployments[d.ID] = d
	}

	for _, c := range r.Changes {
		d, ok := deployments[c.DeploymentID]
		if !ok {
			t.Errorf("change %d refers to unknown deployment %d", c.ID, c.DeploymentID)
		} else if !c.CommittedAt.Before(d.DeployedAt) {
			t.Errorf("change %d was committed after its deployment", c.ID)
		}
	}
	for _, i := range r.Incidents {
		if !services[i.Service] {
			t.Errorf("incident %d is for unknown service %q", i.ID, i.Service)
		}
		if i.ResolvedAt != nil && !i.ResolvedAt.After(i.StartedAt) {
			t.Errorf("incident %d was resolved before it started", i.ID)
		}
		if i.CausedByDeploymentID == nil {
			continue
		}
		d, ok := deployments[*i.CausedByDeploymentID]
		if !ok {
			t.Errorf("incident %d refers to unknown deployment %d", i.ID, *i.CausedByDeploymentID)
		} else if d.Service != i.Service || !i.StartedAt.After(d.DeployedAt) {
			t.Errorf("incident %d does not follow the deployment that caused it", i.ID)
		}
	}
}

func TestEveryServiceHasDeploymentsChangesAndIncidents(t *testing.T) {
	r := seed.Generate(1, year)
	if len(r.Services) < 4 {
		t.Fatalf("got %d services, want several", len(r.Services))
	}

	for _, s := range r.Services {
		m := metrics(r, s.Name)
		incidents := 0
		for _, i := range r.Incidents {
			if i.Service == s.Name {
				incidents++
			}
		}
		if m.Deployments == 0 || m.ChangesDeployed == 0 || incidents == 0 {
			t.Errorf("%s has %d deployments, %d changes, %d incidents, want some of each",
				s.Name, m.Deployments, m.ChangesDeployed, incidents)
		}
	}
}

func TestFrequentSmallReleasesFailLessAndRecoverFasterThanRareLargeOnes(t *testing.T) {
	r := seed.Generate(1, year)

	fast, slow := metrics(r, "payments-api"), metrics(r, "billing-batch")

	if fast.DeploymentsPerWeek <= slow.DeploymentsPerWeek {
		t.Errorf("deployments per week: %v vs %v", fast.DeploymentsPerWeek, slow.DeploymentsPerWeek)
	}
	if *fast.LeadTimeMedianHours >= *slow.LeadTimeMedianHours {
		t.Errorf("lead time: %v vs %v", *fast.LeadTimeMedianHours, *slow.LeadTimeMedianHours)
	}
	if *fast.ChangeFailureRate >= *slow.ChangeFailureRate {
		t.Errorf("change failure rate: %v vs %v", *fast.ChangeFailureRate, *slow.ChangeFailureRate)
	}
	if *fast.RecoveryMedianHours >= *slow.RecoveryMedianHours {
		t.Errorf("recovery time: %v vs %v", *fast.RecoveryMedianHours, *slow.RecoveryMedianHours)
	}
}

// metrics calculates one service's metrics over the year. Calculate keeps
// only the changes and incidents of the deployments it is given.
func metrics(r dora.Records, service string) dora.Metrics {
	var deployments []dora.Deployment
	for _, d := range r.Deployments {
		if d.Service == service {
			deployments = append(deployments, d)
		}
	}
	return dora.Calculate(year, deployments, r.Changes, r.Incidents)
}
