package dora_test

import (
	"slices"
	"testing"

	"github.com/ianflanagan1/dora-mcp/internal/dora"
)

func change(deploymentID int64, committedAt string) dora.Change {
	return dora.Change{DeploymentID: deploymentID, CommittedAt: at(committedAt)}
}

// summaries has deployment 1 ship two changes and cause two incidents,
// deployment 2 ship one change and cause nothing, and remediation
// deployment 3 ship nothing.
func summaries() []dora.DeploymentSummary {
	d3 := deployment(3, "2026-03-04T10:00:00Z")
	d3.Remediation = true
	causedBy1 := func(id int64) dora.Incident {
		i := incident(1, "2026-03-02T11:00:00Z", "")
		i.ID = id
		return i
	}
	return dora.SummariseDeployments(
		[]dora.Deployment{deployment(1, "2026-03-02T10:00:00Z"), deployment(2, "2026-03-03T10:00:00Z"), d3},
		[]dora.Change{
			change(1, "2026-03-02T09:00:00Z"),
			change(1, "2026-03-01T22:00:00Z"),
			change(2, "2026-03-01T10:00:00Z"),
		},
		[]dora.Incident{causedBy1(7), causedBy1(8), {ID: 9, StartedAt: at("2026-03-03T11:00:00Z")}})
}

func deploymentIDs(ds []dora.DeploymentSummary) []int64 {
	ids := []int64{}
	for _, d := range ds {
		ids = append(ids, d.ID)
	}
	return ids
}

func TestADeploymentSummaryCountsItsChangesAndNamesTheIncidentsItCaused(t *testing.T) {
	first := summaries()[0]

	if first.Changes != 2 || !first.Failed || !slices.Equal(first.CausedIncidentIDs, []int64{7, 8}) {
		t.Errorf("summary = %+v, want 2 changes, failed, incidents [7 8]", first)
	}
	if second := summaries()[1]; second.Failed || second.CausedIncidentIDs == nil {
		t.Errorf("summary = %+v, want not failed and an empty incident list", second)
	}
}

func TestALongestLeadTimeIsFromTheOldestCommitAndAbsentWithoutChanges(t *testing.T) {
	s := summaries()

	assertFloat(t, "longest lead time", s[0].LongestLeadTimeHours, 12)
	if s[2].LongestLeadTimeHours != nil {
		t.Errorf("longest lead time without changes = %v, want nil", *s[2].LongestLeadTimeHours)
	}
}

func TestDeploymentsAreSelectedByWhetherTheyFailedAndWhetherTheyWereRemediation(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name   string
		filter dora.DeploymentFilter
		want   []int64
	}{
		{"an empty filter selects every deployment", dora.DeploymentFilter{}, []int64{1, 2, 3}},
		{"failed selects those that caused an incident", dora.DeploymentFilter{Failed: &yes}, []int64{1}},
		{"not failed selects those that caused none", dora.DeploymentFilter{Failed: &no}, []int64{2, 3}},
		{"remediation selects remediation", dora.DeploymentFilter{Remediation: &yes}, []int64{3}},
		{"not remediation selects planned work", dora.DeploymentFilter{Remediation: &no}, []int64{1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deploymentIDs(dora.SelectDeployments(summaries(), tc.filter)); !slices.Equal(got, tc.want) {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeploymentsCanBeSortedNewestFirstOrByLongestLeadTimeWithThoseWithoutChangesLast(t *testing.T) {
	if got := deploymentIDs(dora.SortDeployments(summaries(), dora.DeploymentsNewestFirst)); !slices.Equal(got, []int64{3, 2, 1}) {
		t.Errorf("newest first = %v", got)
	}
	if got := deploymentIDs(dora.SortDeployments(summaries(), dora.LongestLeadTime)); !slices.Equal(got, []int64{2, 1, 3}) {
		t.Errorf("longest lead time = %v", got)
	}
}
