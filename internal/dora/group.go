package dora

import (
	"fmt"
	"maps"
	"slices"
)

// Grouping is what records are grouped by when they are compared.
type Grouping string

// The groupings records can be compared by.
const (
	GroupByService Grouping = "service"
	GroupByTeam    Grouping = "team"
)

// GroupMetrics are the metrics of one group of services.
type GroupMetrics struct {
	Name string `json:"name" jsonschema:"the service or team"`
	Metrics
}

// CalculateByGroup returns the metrics of each group the services fall into,
// ordered by name. A team's metrics pool the deployments of all its services,
// rather than averaging the services' rates. Every group is listed, including
// those with no deployments in the window.
func CalculateByGroup(w Window, services []Service, by Grouping, deployments []Deployment, changes []Change, incidents []Incident) []GroupMetrics {
	groupOf := map[string]string{}
	for _, s := range services {
		groupOf[s.Name] = s.groupName(by)
	}
	byGroup := map[string][]Deployment{}
	for _, d := range deployments {
		byGroup[groupOf[d.Service]] = append(byGroup[groupOf[d.Service]], d)
	}

	groups := []GroupMetrics{}
	for _, name := range slices.Compact(slices.Sorted(maps.Values(groupOf))) {
		// Calculate ignores changes and incidents of deployments it is not
		// given, so every group can be passed all of them.
		groups = append(groups, GroupMetrics{Name: name, Metrics: Calculate(w, byGroup[name], changes, incidents)})
	}
	return groups
}

func (s Service) groupName(by Grouping) string {
	switch by {
	case GroupByService:
		return s.Name
	case GroupByTeam:
		return s.Team
	default:
		panic(fmt.Sprintf("unknown grouping %q", by))
	}
}
