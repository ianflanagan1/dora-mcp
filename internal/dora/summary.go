package dora

import (
	"maps"
	"slices"
)

// IncidentGrouping is what incidents are grouped by when they are summarised.
type IncidentGrouping string

// The groupings incidents can be summarised by.
const (
	IncidentsByService  IncidentGrouping = "service"
	IncidentsByTeam     IncidentGrouping = "team"
	IncidentsBySeverity IncidentGrouping = "severity"
)

// IncidentGroup summarises the incidents of one service, team or severity.
type IncidentGroup struct {
	Name                string   `json:"name" jsonschema:"the service, team or severity"`
	Incidents           int      `json:"incidents" jsonschema:"number of incidents"`
	Open                int      `json:"open" jsonschema:"number still open"`
	CausedByDeployment  int      `json:"caused_by_deployment" jsonschema:"number a deployment caused; the rest had another cause, such as an infrastructure outage"`
	RecoveryMedianHours *float64 `json:"recovery_median_hours" jsonschema:"median hours from start to resolution of the resolved ones"`
}

// SummariseIncidents summarises the incidents of each group, ordered by name.
// Every group is listed, including those with no incidents: every severity,
// or every service or team among services.
func SummariseIncidents(services []Service, by IncidentGrouping, incidents []Incident) []IncidentGroup {
	groupOf := func(i Incident) string { return i.Severity }
	names := []string{string(Sev1), string(Sev2), string(Sev3)}
	if by != IncidentsBySeverity {
		serviceGroup := map[string]string{}
		for _, s := range services {
			serviceGroup[s.Name] = s.groupName(Grouping(by))
		}
		groupOf = func(i Incident) string { return serviceGroup[i.Service] }
		names = slices.Compact(slices.Sorted(maps.Values(serviceGroup)))
	}

	byGroup := map[string][]Incident{}
	for _, i := range incidents {
		byGroup[groupOf(i)] = append(byGroup[groupOf(i)], i)
	}
	groups := []IncidentGroup{}
	for _, name := range names {
		g := IncidentGroup{Name: name}
		var recoveryTimes []float64
		for _, i := range byGroup[name] {
			g.Incidents++
			if i.CausedByDeploymentID != nil {
				g.CausedByDeployment++
			}
			if r := i.RecoveryHours(); r != nil {
				recoveryTimes = append(recoveryTimes, *r)
			} else {
				g.Open++
			}
		}
		g.RecoveryMedianHours = percentile(recoveryTimes, 50)
		groups = append(groups, g)
	}
	return groups
}
