package dora

import (
	"cmp"
	"slices"
)

// DeploymentSummary is a deployment with what it shipped and what it broke.
type DeploymentSummary struct {
	Deployment
	Changes              int      `json:"changes" jsonschema:"number of commits the deployment shipped"`
	LongestLeadTimeHours *float64 `json:"longest_lead_time_hours" jsonschema:"hours from the oldest commit it shipped to the deployment; null when it shipped none"`
	Failed               bool     `json:"failed" jsonschema:"true when the deployment caused at least one incident"`
	CausedIncidentIDs    []int64  `json:"caused_incident_ids" jsonschema:"the incidents the deployment caused"`
}

// DeploymentFilter selects deployments. A nil field does not narrow the
// selection.
type DeploymentFilter struct {
	Failed      *bool
	Remediation *bool
}

// DeploymentOrder is the order deployments are listed in.
type DeploymentOrder string

// The orders deployments can be listed in. The zero value is oldest first.
const (
	DeploymentsOldestFirst DeploymentOrder = "oldest_first"
	DeploymentsNewestFirst DeploymentOrder = "newest_first"
	LongestLeadTime        DeploymentOrder = "longest_lead_time"
)

// SummariseDeployments returns a summary of each deployment, in the order
// given, from the changes it shipped and the incidents it caused. Changes and
// incidents of other deployments are ignored.
func SummariseDeployments(deployments []Deployment, changes []Change, incidents []Incident) []DeploymentSummary {
	summaries := make([]DeploymentSummary, len(deployments))
	index := map[int64]int{}
	for i, d := range deployments {
		summaries[i] = DeploymentSummary{Deployment: d, CausedIncidentIDs: []int64{}}
		index[d.ID] = i
	}
	for _, c := range changes {
		i, ok := index[c.DeploymentID]
		if !ok {
			continue
		}
		s := &summaries[i]
		s.Changes++
		if h := s.LeadTimeHours(c); s.LongestLeadTimeHours == nil || h > *s.LongestLeadTimeHours {
			s.LongestLeadTimeHours = &h
		}
	}
	for _, inc := range incidents {
		if inc.CausedByDeploymentID == nil {
			continue
		}
		if i, ok := index[*inc.CausedByDeploymentID]; ok {
			summaries[i].Failed = true
			summaries[i].CausedIncidentIDs = append(summaries[i].CausedIncidentIDs, inc.ID)
		}
	}
	return summaries
}

// SelectDeployments returns the deployments that match the filter, in the
// order given.
func SelectDeployments(summaries []DeploymentSummary, f DeploymentFilter) []DeploymentSummary {
	return slices.DeleteFunc(slices.Clone(summaries), func(s DeploymentSummary) bool {
		return (f.Failed != nil && s.Failed != *f.Failed) ||
			(f.Remediation != nil && s.Remediation != *f.Remediation)
	})
}

// SortDeployments returns the deployments in the order asked for, leaving the
// given slice as it was. Longest lead time lists deployments that shipped no
// changes last. Ties are broken by deployment time, oldest first.
func SortDeployments(summaries []DeploymentSummary, o DeploymentOrder) []DeploymentSummary {
	sorted := slices.Clone(summaries)
	oldestFirst := func(a, b DeploymentSummary) int {
		return cmp.Or(a.DeployedAt.Compare(b.DeployedAt), cmp.Compare(a.ID, b.ID))
	}
	switch o {
	case DeploymentsNewestFirst:
		slices.SortStableFunc(sorted, func(a, b DeploymentSummary) int { return oldestFirst(b, a) })
	case LongestLeadTime:
		slices.SortStableFunc(sorted, largestFirst(func(s DeploymentSummary) *float64 { return s.LongestLeadTimeHours }, oldestFirst))
	default:
		slices.SortStableFunc(sorted, oldestFirst)
	}
	return sorted
}
