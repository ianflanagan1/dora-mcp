package dora

import (
	"cmp"
	"slices"
)

// Severity is how serious an incident is, sev1 being the most serious.
type Severity string

// The severities an incident can have.
const (
	Sev1 Severity = "sev1"
	Sev2 Severity = "sev2"
	Sev3 Severity = "sev3"
)

// Status is whether an incident has been resolved.
type Status string

// The statuses incidents can be selected by.
const (
	Open     Status = "open"
	Resolved Status = "resolved"
)

// Cause is whether a deployment caused an incident.
type Cause string

// The causes incidents can be selected by.
const (
	CausedByDeployment Cause = "deployment"
	CausedByNothing    Cause = "none"
)

// IncidentFilter selects incidents. A zero field does not narrow the
// selection.
type IncidentFilter struct {
	Severity Severity
	Status   Status
	CausedBy Cause
}

// IncidentOrder is the order incidents are listed in.
type IncidentOrder string

// The orders incidents can be listed in. The zero value is oldest first.
const (
	IncidentsOldestFirst IncidentOrder = "oldest_first"
	IncidentsNewestFirst IncidentOrder = "newest_first"
	LongestRecovery      IncidentOrder = "longest_recovery"
)

// RecoveryHours returns the hours from the incident's start to its
// resolution, or nil while it is open.
func (i Incident) RecoveryHours() *float64 {
	if i.ResolvedAt == nil {
		return nil
	}
	h := i.ResolvedAt.Sub(i.StartedAt).Hours()
	return &h
}

// SelectIncidents returns the incidents that match the filter, in the order
// given.
func SelectIncidents(incidents []Incident, f IncidentFilter) []Incident {
	return slices.DeleteFunc(slices.Clone(incidents), func(i Incident) bool {
		return (f.Severity != "" && Severity(i.Severity) != f.Severity) ||
			(f.Status == Open && i.ResolvedAt != nil) ||
			(f.Status == Resolved && i.ResolvedAt == nil) ||
			(f.CausedBy == CausedByDeployment && i.CausedByDeploymentID == nil) ||
			(f.CausedBy == CausedByNothing && i.CausedByDeploymentID != nil)
	})
}

// SortIncidents returns the incidents in the order asked for, leaving the
// given slice as it was. Longest recovery lists resolved incidents, slowest
// first, before open ones, whose recovery time is not yet known. Ties are
// broken by start time, oldest first.
func SortIncidents(incidents []Incident, o IncidentOrder) []Incident {
	sorted := slices.Clone(incidents)
	oldestFirst := func(a, b Incident) int {
		return cmp.Or(a.StartedAt.Compare(b.StartedAt), cmp.Compare(a.ID, b.ID))
	}
	switch o {
	case IncidentsNewestFirst:
		slices.SortStableFunc(sorted, func(a, b Incident) int { return oldestFirst(b, a) })
	case LongestRecovery:
		slices.SortStableFunc(sorted, largestFirst(Incident.RecoveryHours, oldestFirst))
	default:
		slices.SortStableFunc(sorted, oldestFirst)
	}
	return sorted
}
