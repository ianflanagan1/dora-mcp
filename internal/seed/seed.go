// Package seed makes up plausible DORA records.
//
// Each service has a delivery profile, so the services differ the way real
// teams do: some ship small changes several times a day and recover quickly,
// others batch work into rare, riskier releases. The same seed always
// produces the same records.
package seed

import (
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/ianflanagan1/dora-mcp/internal/dora"
)

type profile struct {
	name, team string
	// deploysPerWeekday is the mean number of planned deployments each weekday.
	deploysPerWeekday float64
	leadTimeHours     float64 // median, commit to production
	failureChance     float64 // chance a deployment causes an incident
	recoveryHours     float64 // median, incident start to resolution
}

var profiles = []profile{
	{"payments-api", "Payments", 3.0, 6, 0.04, 0.8},
	{"checkout-web", "Payments", 1.8, 18, 0.08, 1.5},
	{"auth-service", "Platform", 1.2, 12, 0.06, 1},
	{"search-service", "Discovery", 0.8, 40, 0.12, 4},
	{"recommendations", "Discovery", 0.35, 90, 0.18, 10},
	{"billing-batch", "Finance", 0.12, 240, 0.3, 30},
}

var titles = []string{
	"Elevated 5xx error rate",
	"Latency above SLO",
	"Failed requests after release",
	"Data not updating",
	"Login failures for some users",
	"Background jobs stuck",
}

// Generate returns made-up records of deployments, changes and incidents in
// the window.
func Generate(seed uint64, w dora.Window) dora.Records {
	g := &generator{
		rng: rand.New(rand.NewPCG(seed, seed)), //nolint:gosec // fake data, not security
		end: w.To,
	}
	for _, p := range profiles {
		g.records.Services = append(g.records.Services, dora.Service{Name: p.name, Team: p.team})
		g.service(p, w)
	}
	return g.records
}

type generator struct {
	rng     *rand.Rand
	end     time.Time
	records dora.Records
}

func (g *generator) service(p profile, w dora.Window) {
	for day := w.From; day.Before(w.To); day = day.AddDate(0, 0, 1) {
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		for range g.poisson(p.deploysPerWeekday) {
			g.deployment(p, day.Add(g.between(9*time.Hour, 18*time.Hour)), false)
		}
		// Roughly one incident a quarter that no deployment caused.
		if g.rng.Float64() < 1.0/65 {
			g.incident(p, nil, day.Add(g.between(0, 24*time.Hour)))
		}
	}
}

func (g *generator) deployment(p profile, deployedAt time.Time, remediation bool) {
	if !deployedAt.Before(g.end) {
		return
	}
	id := int64(len(g.records.Deployments) + 1)
	g.records.Deployments = append(g.records.Deployments, dora.Deployment{
		ID: id, Service: p.name, DeployedAt: deployedAt, Remediation: remediation,
	})

	changes, leadTime := 1+g.rng.IntN(5), p.leadTimeHours
	if remediation {
		changes, leadTime = 1, p.recoveryHours/2
	}
	for range changes {
		g.records.Changes = append(g.records.Changes, dora.Change{
			ID:           int64(len(g.records.Changes) + 1),
			DeploymentID: id,
			SHA:          fmt.Sprintf("%040x", g.rng.Uint64()),
			CommittedAt:  deployedAt.Add(-g.logNormal(leadTime, 0.8)),
		})
	}

	if !remediation && g.rng.Float64() < p.failureChance {
		g.incident(p, &id, deployedAt.Add(g.between(5*time.Minute, 6*time.Hour)))
	}
}

func (g *generator) incident(p profile, causedBy *int64, startedAt time.Time) {
	if !startedAt.Before(g.end) {
		return
	}
	recovery := g.logNormal(p.recoveryHours, 0.9)
	var resolvedAt *time.Time
	if end := startedAt.Add(recovery); end.Before(g.end) {
		resolvedAt = &end
	}
	g.records.Incidents = append(g.records.Incidents, dora.Incident{
		ID:                   int64(len(g.records.Incidents) + 1),
		Service:              p.name,
		CausedByDeploymentID: causedBy,
		Severity:             []string{"sev1", "sev2", "sev2", "sev3", "sev3", "sev3"}[g.rng.IntN(6)],
		Title:                titles[g.rng.IntN(len(titles))],
		StartedAt:            startedAt,
		ResolvedAt:           resolvedAt,
	})

	// Most failed deployments are fixed forward with a remediation deployment;
	// the rest are rolled back, which ships nothing new.
	if causedBy != nil && g.rng.Float64() < 0.6 {
		g.deployment(p, startedAt.Add(recovery*3/4), true)
	}
}

// poisson returns a Poisson-distributed count with the given mean.
func (g *generator) poisson(mean float64) int {
	limit, product, n := math.Exp(-mean), g.rng.Float64(), 0
	for product > limit {
		product *= g.rng.Float64()
		n++
	}
	return n
}

// logNormal returns a right-skewed duration with the given median in hours,
// the usual shape of lead and recovery times.
func (g *generator) logNormal(medianHours, spread float64) time.Duration {
	hours := math.Exp(math.Log(medianHours) + spread*g.rng.NormFloat64())
	return time.Duration(hours * float64(time.Hour)).Truncate(time.Second)
}

func (g *generator) between(lo, hi time.Duration) time.Duration {
	return (lo + time.Duration(g.rng.Int64N(int64(hi-lo)))).Truncate(time.Second)
}
