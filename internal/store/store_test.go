package store_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ianflanagan1/dora-mcp/internal/dora"
	"github.com/ianflanagan1/dora-mcp/internal/store"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr[T any](v T) *T { return &v }

var fixture = dora.Records{
	Services: []dora.Service{{Name: "api", Team: "Core"}, {Name: "web", Team: "Front"}},
	Deployments: []dora.Deployment{
		{ID: 1, Service: "api", DeployedAt: at("2026-03-01T10:00:00Z")},
		{ID: 2, Service: "api", DeployedAt: at("2026-03-03T10:00:00Z"), Remediation: true},
		{ID: 3, Service: "web", DeployedAt: at("2026-03-04T10:00:00Z")},
		{ID: 4, Service: "api", DeployedAt: at("2026-03-10T10:00:00Z")},
	},
	Changes: []dora.Change{
		{ID: 1, DeploymentID: 2, SHA: "aaa", CommittedAt: at("2026-03-03T08:00:00Z")},
		{ID: 2, DeploymentID: 4, SHA: "bbb", CommittedAt: at("2026-03-09T08:00:00Z")},
	},
	Incidents: []dora.Incident{
		{ID: 1, Service: "api", CausedByDeploymentID: ptr[int64](1), Severity: "sev2", Title: "Before the week",
			StartedAt: at("2026-03-02T09:00:00Z"), ResolvedAt: ptr(at("2026-03-02T10:00:00Z"))},
		{ID: 3, Service: "web", Severity: "sev3", Title: "Network blip",
			StartedAt: at("2026-03-05T09:00:00Z"), ResolvedAt: ptr(at("2026-03-05T09:30:00Z"))},
		{ID: 2, Service: "api", CausedByDeploymentID: ptr[int64](2), Severity: "sev1", Title: "After the week",
			StartedAt: at("2026-03-09T09:00:00Z")},
	},
}

var week = dora.Window{
	From: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
	To:   time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC),
}

var allTime = dora.Window{
	From: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
	To:   time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
}

func openFixture(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dora.sqlite")
	if err := store.Create(t.Context(), path, fixture); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func ids[T any](items []T, id func(T) int64) []int64 {
	out := []int64{}
	for _, item := range items {
		out = append(out, id(item))
	}
	return out
}

func assertIDs(t *testing.T, got, want []int64) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func deploymentID(d dora.Deployment) int64 { return d.ID }
func incidentID(i dora.Incident) int64     { return i.ID }

func TestOpeningAMissingDatabaseReportsItIsNotFound(t *testing.T) {
	_, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "missing.sqlite"))

	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestACreatedDatabaseHoldsExactlyTheRecordsItWasGiven(t *testing.T) {
	s := openFixture(t)
	all := store.Filter{Window: allTime}

	deployments, err := s.Deployments(t.Context(), all)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := s.Changes(t.Context(), all)
	if err != nil {
		t.Fatal(err)
	}
	incidents, err := s.Incidents(t.Context(), all)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(deployments, fixture.Deployments) {
		t.Errorf("deployments = %+v, want %+v", deployments, fixture.Deployments)
	}
	if !reflect.DeepEqual(changes, fixture.Changes) {
		t.Errorf("changes = %+v, want %+v", changes, fixture.Changes)
	}
	if !reflect.DeepEqual(incidents, fixture.Incidents) {
		t.Errorf("incidents = %+v, want %+v", incidents, fixture.Incidents)
	}
}

func TestCreatingOverAnExistingDatabaseIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dora.sqlite")
	if err := store.Create(t.Context(), path, fixture); err != nil {
		t.Fatal(err)
	}

	if err := store.Create(t.Context(), path, fixture); !errors.Is(err, store.ErrExists) {
		t.Errorf("err = %v, want ErrExists", err)
	}
}

func TestRecordsForAServiceNotListedAreRefusedAndLeaveNoDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dora.sqlite")
	records := fixture
	records.Services = records.Services[:1]

	if err := store.Create(t.Context(), path, records); err == nil {
		t.Fatal("Create succeeded, want an error")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a database was left at %s", path)
	}
}

func TestRecordsReferringToAMissingDeploymentAreRefused(t *testing.T) {
	records := fixture
	records.Changes = []dora.Change{{ID: 1, DeploymentID: 99, SHA: "x", CommittedAt: at("2026-03-01T00:00:00Z")}}

	if err := store.Create(t.Context(), filepath.Join(t.TempDir(), "dora.sqlite"), records); err == nil {
		t.Error("Create succeeded, want an error")
	}
}

func TestServicesAreListedByName(t *testing.T) {
	services, err := openFixture(t).Services(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := []dora.Service{{Name: "api", Team: "Core"}, {Name: "web", Team: "Front"}}
	if len(services) != 2 || services[0] != want[0] || services[1] != want[1] {
		t.Errorf("services = %v, want %v", services, want)
	}
}

func TestDataRangeSpansTheFirstToTheLastDeployment(t *testing.T) {
	first, last, err := openFixture(t).DataRange(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if first.Format(time.RFC3339) != "2026-03-01T10:00:00Z" || last.Format(time.RFC3339) != "2026-03-10T10:00:00Z" {
		t.Errorf("range = %v to %v", first, last)
	}
}

func TestDeploymentsAreSelectedByWindowAndService(t *testing.T) {
	s := openFixture(t)

	all, err := s.Deployments(t.Context(), store.Filter{Window: week})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, ids(all, deploymentID), []int64{2, 3})

	api, err := s.Deployments(t.Context(), store.Filter{Window: week, Service: "api"})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, ids(api, deploymentID), []int64{2})
	if !api[0].Remediation {
		t.Error("deployment 2 is not marked as remediation")
	}
}

func TestRecordsAreSelectedByTeam(t *testing.T) {
	s := openFixture(t)
	core := store.Filter{Window: allTime, Team: "Core"}

	deployments, err := s.Deployments(t.Context(), core)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, ids(deployments, deploymentID), []int64{1, 2, 4})

	changes, err := s.Changes(t.Context(), core)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, ids(changes, func(c dora.Change) int64 { return c.ID }), []int64{1, 2})

	incidents, err := s.Incidents(t.Context(), core)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, ids(incidents, incidentID), []int64{1, 2})

	caused, err := s.IncidentsCausedByDeployments(t.Context(), store.Filter{Window: allTime, Team: "Front"})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, ids(caused, incidentID), []int64{})
}

func TestChangesAreSelectedByWhenTheirDeploymentWasMade(t *testing.T) {
	changes, err := openFixture(t).Changes(t.Context(), store.Filter{Window: week})
	if err != nil {
		t.Fatal(err)
	}

	assertIDs(t, ids(changes, func(c dora.Change) int64 { return c.ID }), []int64{1})
}

func TestIncidentsAreSelectedByWhenTheyStarted(t *testing.T) {
	incidents, err := openFixture(t).Incidents(t.Context(), store.Filter{Window: week})
	if err != nil {
		t.Fatal(err)
	}

	assertIDs(t, ids(incidents, incidentID), []int64{1, 3})
	if incidents[1].CausedByDeploymentID != nil {
		t.Error("incident 3 has a causing deployment, want none")
	}
}

func TestIncidentsCausedByDeploymentsAreSelectedByWhenTheDeploymentWasMade(t *testing.T) {
	incidents, err := openFixture(t).IncidentsCausedByDeployments(t.Context(), store.Filter{Window: week})
	if err != nil {
		t.Fatal(err)
	}

	assertIDs(t, ids(incidents, incidentID), []int64{2})
	if incidents[0].ResolvedAt != nil {
		t.Error("incident 2 is resolved, want open")
	}
}

func TestADeploymentIsFoundByIDWithTheChangesItShippedAndTheIncidentsItCaused(t *testing.T) {
	s := openFixture(t)

	d, ok, err := s.Deployment(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || d.Service != "api" || !d.Remediation {
		t.Errorf("deployment 2 = %+v, found %v", d, ok)
	}
	changes, err := s.ChangesShippedBy(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, ids(changes, func(c dora.Change) int64 { return c.ID }), []int64{1})
	incidents, err := s.IncidentsCausedBy(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, ids(incidents, incidentID), []int64{2})
}

func TestAMissingDeploymentIsReportedAsNotFound(t *testing.T) {
	_, ok, err := openFixture(t).Deployment(t.Context(), 99)
	if err != nil {
		t.Fatal(err)
	}

	if ok {
		t.Error("deployment 99 found, want not found")
	}
}

func TestChangesAreFoundByAPrefixOfTheirSHA(t *testing.T) {
	s := openFixture(t)

	found, err := s.FindChanges(t.Context(), "bb", 10)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, ids(found, func(c dora.Change) int64 { return c.ID }), []int64{2})

	none, err := s.FindChanges(t.Context(), "%", 10)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, ids(none, func(c dora.Change) int64 { return c.ID }), []int64{})
}
