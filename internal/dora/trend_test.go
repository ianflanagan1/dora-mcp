package dora_test

import (
	"testing"
	"time"

	"github.com/ianflanagan1/dora-mcp/internal/dora"
)

type span struct {
	from, to string
	partial  bool
}

func assertBuckets(t *testing.T, got []dora.Bucket, want []span) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d buckets, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if !g.From.Equal(at(w.from)) || !g.To.Equal(at(w.to)) || g.Partial != w.partial {
			t.Errorf("bucket %d = %s to %s partial %v, want %s to %s partial %v",
				i, g.From.Format(time.RFC3339), g.To.Format(time.RFC3339), g.Partial, w.from, w.to, w.partial)
		}
	}
}

func window(from, to string) dora.Window {
	return dora.Window{From: at(from), To: at(to)}
}

func TestWeeksStartOnMondayAndABucketCutShortByThePeriodIsMarkedPartial(t *testing.T) {
	// From a Wednesday to the end of a Thursday.
	buckets := dora.Buckets(window("2026-03-04T00:00:00Z", "2026-03-20T00:00:00Z"), dora.Weekly)

	assertBuckets(t, buckets, []span{
		{"2026-03-04T00:00:00Z", "2026-03-09T00:00:00Z", true},
		{"2026-03-09T00:00:00Z", "2026-03-16T00:00:00Z", false},
		{"2026-03-16T00:00:00Z", "2026-03-20T00:00:00Z", true},
	})
}

func TestMonthsFollowTheCalendar(t *testing.T) {
	buckets := dora.Buckets(window("2026-01-01T00:00:00Z", "2026-04-01T00:00:00Z"), dora.Monthly)

	assertBuckets(t, buckets, []span{
		{"2026-01-01T00:00:00Z", "2026-02-01T00:00:00Z", false},
		{"2026-02-01T00:00:00Z", "2026-03-01T00:00:00Z", false},
		{"2026-03-01T00:00:00Z", "2026-04-01T00:00:00Z", false},
	})
}

func TestQuartersFollowTheCalendar(t *testing.T) {
	buckets := dora.Buckets(window("2026-02-15T00:00:00Z", "2026-10-01T00:00:00Z"), dora.Quarterly)

	assertBuckets(t, buckets, []span{
		{"2026-02-15T00:00:00Z", "2026-04-01T00:00:00Z", true},
		{"2026-04-01T00:00:00Z", "2026-07-01T00:00:00Z", false},
		{"2026-07-01T00:00:00Z", "2026-10-01T00:00:00Z", false},
	})
}

func TestEachPointOfATrendCoversOnlyTheDeploymentsInItsBucket(t *testing.T) {
	trend := dora.CalculateTrend(window("2026-03-02T00:00:00Z", "2026-03-16T00:00:00Z"), dora.Weekly,
		[]dora.Deployment{
			deployment(1, "2026-03-03T10:00:00Z"),
			deployment(2, "2026-03-04T10:00:00Z"),
			deployment(3, "2026-03-10T10:00:00Z"),
		}, nil, []dora.Incident{incident(3, "2026-03-10T11:00:00Z", "")})

	if len(trend) != 2 {
		t.Fatalf("got %d points, want 2", len(trend))
	}
	if trend[0].Deployments != 2 || trend[1].Deployments != 1 {
		t.Errorf("deployments = %d and %d, want 2 and 1", trend[0].Deployments, trend[1].Deployments)
	}
	assertFloat(t, "first week change failure rate", trend[0].ChangeFailureRate, 0)
	assertFloat(t, "second week change failure rate", trend[1].ChangeFailureRate, 1)
}
