package dora_test

import (
	"slices"
	"testing"

	"github.com/ianflanagan1/dora-mcp/internal/dora"
)

var incidents = []dora.Incident{
	{ID: 1, CausedByDeploymentID: ptr[int64](1), Severity: "sev2",
		StartedAt: at("2026-03-02T10:00:00Z"), ResolvedAt: ptr(at("2026-03-02T13:00:00Z"))},
	{ID: 2, Severity: "sev3", StartedAt: at("2026-03-03T10:00:00Z")},
	{ID: 3, CausedByDeploymentID: ptr[int64](2), Severity: "sev1",
		StartedAt: at("2026-03-04T10:00:00Z"), ResolvedAt: ptr(at("2026-03-04T18:00:00Z"))},
	{ID: 4, CausedByDeploymentID: ptr[int64](3), Severity: "sev2", StartedAt: at("2026-03-05T10:00:00Z")},
}

func incidentIDs(incidents []dora.Incident) []int64 {
	ids := []int64{}
	for _, i := range incidents {
		ids = append(ids, i.ID)
	}
	return ids
}

func TestIncidentsAreSelectedBySeverityStatusAndCause(t *testing.T) {
	for _, tc := range []struct {
		name   string
		filter dora.IncidentFilter
		want   []int64
	}{
		{"an empty filter selects every incident", dora.IncidentFilter{}, []int64{1, 2, 3, 4}},
		{"a severity selects only that severity", dora.IncidentFilter{Severity: dora.Sev2}, []int64{1, 4}},
		{"open selects the unresolved", dora.IncidentFilter{Status: dora.Open}, []int64{2, 4}},
		{"resolved selects the resolved", dora.IncidentFilter{Status: dora.Resolved}, []int64{1, 3}},
		{"a deployment cause selects those a deployment caused", dora.IncidentFilter{CausedBy: dora.CausedByDeployment}, []int64{1, 3, 4}},
		{"no cause selects those no deployment caused", dora.IncidentFilter{CausedBy: dora.CausedByNothing}, []int64{2}},
		{"the conditions combine", dora.IncidentFilter{Severity: dora.Sev2, Status: dora.Open}, []int64{4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := incidentIDs(dora.SelectIncidents(incidents, tc.filter)); !slices.Equal(got, tc.want) {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIncidentsAreSortedOldestFirstUnlessAskedOtherwise(t *testing.T) {
	shuffled := []dora.Incident{incidents[2], incidents[0], incidents[3], incidents[1]}

	if got := incidentIDs(dora.SortIncidents(shuffled, "")); !slices.Equal(got, []int64{1, 2, 3, 4}) {
		t.Errorf("default order = %v, want oldest first", got)
	}
	if got := incidentIDs(dora.SortIncidents(shuffled, dora.IncidentsNewestFirst)); !slices.Equal(got, []int64{4, 3, 2, 1}) {
		t.Errorf("newest first = %v", got)
	}
}

func TestLongestRecoveryPutsTheSlowestResolvedFirstAndOpenIncidentsLast(t *testing.T) {
	got := incidentIDs(dora.SortIncidents(incidents, dora.LongestRecovery))

	if want := []int64{3, 1, 2, 4}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestSortingLeavesTheGivenIncidentsInTheirOrder(t *testing.T) {
	given := slices.Clone(incidents)
	dora.SortIncidents(given, dora.IncidentsNewestFirst)

	if got := incidentIDs(given); !slices.Equal(got, []int64{1, 2, 3, 4}) {
		t.Errorf("given incidents reordered to %v", got)
	}
}

func TestRecoveryTimeIsHoursFromStartToResolutionAndAbsentWhileOpen(t *testing.T) {
	assertFloat(t, "recovery", incidents[2].RecoveryHours(), 8)
	if got := incidents[1].RecoveryHours(); got != nil {
		t.Errorf("open incident recovery = %v, want nil", *got)
	}
}
