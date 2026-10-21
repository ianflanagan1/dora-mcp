package dora

import (
	"fmt"
	"time"
)

// Interval is the length of the buckets a trend is split into.
type Interval string

// The intervals a trend can be split into. Each is a calendar period in UTC;
// weeks start on Monday.
const (
	Weekly    Interval = "week"
	Monthly   Interval = "month"
	Quarterly Interval = "quarter"
)

// Bucket is one interval of a trend. Partial marks a bucket that the window
// cuts short, so it covers less than the whole calendar period.
type Bucket struct {
	Window
	Partial bool
}

// TrendPoint is the metrics of one bucket of a trend.
type TrendPoint struct {
	Bucket
	Metrics
}

// Buckets splits the window into calendar intervals. The first and last
// buckets are clipped to the window.
func Buckets(w Window, iv Interval) []Bucket {
	var buckets []Bucket
	for start := iv.start(w.From); start.Before(w.To); {
		end := iv.next(start)
		b := Bucket{Window: Window{From: start, To: end}}
		if b.From.Before(w.From) {
			b.From, b.Partial = w.From, true
		}
		if b.To.After(w.To) {
			b.To, b.Partial = w.To, true
		}
		buckets = append(buckets, b)
		start = end
	}
	return buckets
}

// CalculateTrend returns the metrics of each bucket the window splits into,
// for the deployments made in that bucket.
func CalculateTrend(w Window, iv Interval, deployments []Deployment, changes []Change, incidents []Incident) []TrendPoint {
	var trend []TrendPoint
	for _, b := range Buckets(w, iv) {
		trend = append(trend, TrendPoint{Bucket: b, Metrics: Calculate(b.Window, deployments, changes, incidents)})
	}
	return trend
}

// start returns the start of the calendar interval that t falls in.
func (iv Interval) start(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	switch iv {
	case Weekly:
		day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		return day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
	case Monthly:
		return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	case Quarterly:
		return time.Date(y, (m-1)/3*3+1, 1, 0, 0, 0, 0, time.UTC)
	default:
		panic(fmt.Sprintf("unknown interval %q", iv))
	}
}

// next returns the start of the interval after the one starting at start.
func (iv Interval) next(start time.Time) time.Time {
	switch iv {
	case Weekly:
		return start.AddDate(0, 0, 7)
	case Monthly:
		return start.AddDate(0, 1, 0)
	case Quarterly:
		return start.AddDate(0, 3, 0)
	default:
		panic(fmt.Sprintf("unknown interval %q", iv))
	}
}
