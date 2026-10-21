// Package dora calculates the DORA software delivery metrics from records of
// deployments, the changes they shipped and the incidents they caused.
package dora

import (
	"math"
	"slices"
	"time"
)

// Service is a deployable service and the team that owns it.
type Service struct {
	Name string `json:"name"`
	Team string `json:"team"`
}

// Records is a complete set of DORA records, such as a database holds.
// Deployments and incidents name their service, which must be in Services.
type Records struct {
	Services    []Service
	Deployments []Deployment
	Changes     []Change
	Incidents   []Incident
}

// Deployment is a release of a service to production.
type Deployment struct {
	ID         int64     `json:"id"`
	Service    string    `json:"service"`
	DeployedAt time.Time `json:"deployed_at"`
	// Remediation marks unplanned work shipped to fix a user-facing problem.
	Remediation bool `json:"remediation"`
}

// LeadTimeHours returns the hours from the change's commit to the deployment.
func (d Deployment) LeadTimeHours(c Change) float64 {
	return d.DeployedAt.Sub(c.CommittedAt).Hours()
}

// Change is a commit that reached production in a deployment.
type Change struct {
	ID           int64     `json:"id"`
	DeploymentID int64     `json:"deployment_id"`
	SHA          string    `json:"sha"`
	CommittedAt  time.Time `json:"committed_at"`
}

// Incident is a production incident. CausedByDeploymentID is nil when no
// deployment caused it, and ResolvedAt is nil while it is open.
type Incident struct {
	ID                   int64      `json:"id"`
	Service              string     `json:"service"`
	CausedByDeploymentID *int64     `json:"caused_by_deployment_id"`
	Severity             string     `json:"severity"`
	Title                string     `json:"title"`
	StartedAt            time.Time  `json:"started_at"`
	ResolvedAt           *time.Time `json:"resolved_at"`
}

// Window is the half-open period [From, To) that metrics are calculated over.
type Window struct {
	From time.Time
	To   time.Time
}

// Metrics are the DORA metrics for a window. A nil field means there was no
// data to calculate it from, which is different from a value of zero.
type Metrics struct {
	Deployments            int      `json:"deployments" jsonschema:"number of production deployments in the window"`
	DeploymentsPerWeek     float64  `json:"deployments_per_week" jsonschema:"deployment frequency"`
	ChangesDeployed        int      `json:"changes_deployed" jsonschema:"number of commits shipped by those deployments"`
	LeadTimeMedianHours    *float64 `json:"lead_time_median_hours" jsonschema:"median time from commit to running in production"`
	LeadTimeP90Hours       *float64 `json:"lead_time_p90_hours" jsonschema:"90th percentile time from commit to running in production"`
	FailedDeployments      int      `json:"failed_deployments" jsonschema:"deployments that caused at least one incident"`
	ChangeFailureRate      *float64 `json:"change_failure_rate" jsonschema:"share of deployments that caused an incident, from 0 to 1"`
	RecoveredIncidents     int      `json:"recovered_incidents" jsonschema:"resolved incidents caused by deployments in the window"`
	UnresolvedIncidents    int      `json:"unresolved_incidents" jsonschema:"open incidents caused by deployments in the window"`
	RecoveryMedianHours    *float64 `json:"recovery_median_hours" jsonschema:"failed deployment recovery time: median hours from incident start to resolution"`
	RemediationDeployments int      `json:"remediation_deployments" jsonschema:"unplanned deployments that fixed a user-facing problem"`
	ReworkRate             *float64 `json:"rework_rate" jsonschema:"share of deployments that were remediation, from 0 to 1"`
}

// Calculate returns the metrics for the deployments made in the window, the
// changes those deployments shipped and the incidents they caused. Records
// outside that scope are ignored, so callers may pass a superset.
func Calculate(w Window, deployments []Deployment, changes []Change, incidents []Incident) Metrics {
	var m Metrics
	inWindow := map[int64]Deployment{}
	for _, d := range deployments {
		if d.DeployedAt.Before(w.From) || !d.DeployedAt.Before(w.To) {
			continue
		}
		inWindow[d.ID] = d
		m.Deployments++
		if d.Remediation {
			m.RemediationDeployments++
		}
	}
	m.DeploymentsPerWeek = float64(m.Deployments) / (w.To.Sub(w.From).Hours() / 24 / 7)

	var leadTimes []float64
	for _, c := range changes {
		if d, ok := inWindow[c.DeploymentID]; ok {
			leadTimes = append(leadTimes, d.LeadTimeHours(c))
		}
	}
	m.ChangesDeployed = len(leadTimes)
	m.LeadTimeMedianHours = percentile(leadTimes, 50)
	m.LeadTimeP90Hours = percentile(leadTimes, 90)

	failed := map[int64]bool{}
	var recoveryTimes []float64
	for _, i := range incidents {
		if i.CausedByDeploymentID == nil {
			continue
		}
		if _, ok := inWindow[*i.CausedByDeploymentID]; !ok {
			continue
		}
		failed[*i.CausedByDeploymentID] = true
		if i.ResolvedAt == nil {
			m.UnresolvedIncidents++
			continue
		}
		recoveryTimes = append(recoveryTimes, *i.RecoveryHours())
	}
	m.FailedDeployments = len(failed)
	m.RecoveredIncidents = len(recoveryTimes)
	m.RecoveryMedianHours = percentile(recoveryTimes, 50)

	if m.Deployments > 0 {
		m.ChangeFailureRate = ratio(m.FailedDeployments, m.Deployments)
		m.ReworkRate = ratio(m.RemediationDeployments, m.Deployments)
	}
	return m
}

// percentile returns the p-th percentile of values by linear interpolation
// between closest ranks, or nil when there are no values.
func percentile(values []float64, p float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := slices.Sorted(slices.Values(values))
	rank := p / 100 * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	v := sorted[lower] + (sorted[upper]-sorted[lower])*(rank-float64(lower))
	return &v
}

func ratio(n, d int) *float64 {
	v := float64(n) / float64(d)
	return &v
}
