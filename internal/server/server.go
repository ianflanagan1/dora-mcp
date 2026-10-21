// Package server exposes DORA records and metrics as MCP tools. It knows
// nothing about transports: the caller runs the returned server over stdio,
// HTTP or anything else the SDK supports.
package server

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ianflanagan1/dora-mcp/internal/dora"
	"github.com/ianflanagan1/dora-mcp/internal/store"
)

const (
	defaultLimit = 100
	maxLimit     = 500
	maxBuckets   = 104
	maxCommits   = 20
	dateLayout   = time.DateOnly
)

const instructions = `Records of software delivery for a set of services, and the DORA metrics calculated from them.

Call list_services first: it gives the service names, their teams and the dates the data covers.
Use get_dora_metrics for the metrics of a period, compare_dora_metrics to rank services or teams, and get_dora_trend to see how the metrics change over time.
Use list_deployments and list_incidents to see the records behind the metrics, get_deployment for one deployment in full, and find_commit to trace a commit to production.
Use summarise_incidents for incident counts by service, team or severity, including incidents no deployment caused.`

// New returns an MCP server whose tools read from st.
func New(st *store.Store, version string) *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{Name: "dora-mcp", Version: version},
		&mcp.ServerOptions{Instructions: instructions},
	)
	h := handlers{store: st}

	mcp.AddTool(s, readOnly(&mcp.Tool{
		Name:        "list_services",
		Description: "List every service with its owning team, and the first and last dates there is data for.",
	}), h.listServices)
	mcp.AddTool(s, readOnly(&mcp.Tool{
		Name: "get_dora_metrics",
		Description: "Calculate the DORA metrics for deployments made between two dates, for one service, one team or all of them: " +
			"deployment frequency, lead time for changes, change failure rate, failed deployment recovery time and rework rate.",
	}), h.getDoraMetrics)
	mcp.AddTool(s, readOnly(&mcp.Tool{
		Name: "compare_dora_metrics",
		Description: "Calculate the DORA metrics for deployments made between two dates side by side, for each service or each team, " +
			"to rank them or find the best and worst. A team's metrics pool the deployments of all its services. " +
			"Give team to compare only that team's services.",
		InputSchema: inputSchema[CompareInput](),
	}), h.compareDoraMetrics)
	mcp.AddTool(s, readOnly(&mcp.Tool{
		Name: "get_dora_trend",
		Description: "Calculate the DORA metrics for each week, month or quarter between two dates, to see whether they are improving. " +
			"Buckets are calendar periods in UTC, and weeks start on Monday. A bucket the dates cut short is marked partial. " +
			fmt.Sprintf("At most %d buckets.", maxBuckets),
		InputSchema: inputSchema[TrendInput](),
	}), h.getDoraTrend)
	mcp.AddTool(s, readOnly(&mcp.Tool{
		Name: "list_deployments",
		Description: "List production deployments made between two dates, oldest first unless sorted otherwise, " +
			"each with the number of commits it shipped, its longest lead time and the incidents it caused. " +
			"Filter by failed to see the deployments behind a change failure rate, or by remediation for those behind a rework rate.",
		InputSchema: inputSchema[DeploymentsInput](),
	}), h.listDeployments)
	mcp.AddTool(s, readOnly(&mcp.Tool{
		Name: "list_incidents",
		Description: "List production incidents, oldest first unless sorted otherwise. By default it lists those that started between two dates; " +
			"with window_applies_to=causing_deployment it lists those caused by deployments made between them, " +
			"which are the incidents behind that period's change failure rate and recovery time. " +
			"caused_by_deployment_id is null for incidents no deployment caused, and resolved_at and recovery_hours are null for open incidents.",
		InputSchema: inputSchema[IncidentsInput](),
	}), h.listIncidents)
	mcp.AddTool(s, readOnly(&mcp.Tool{
		Name: "summarise_incidents",
		Description: "Count the incidents that started between two dates for each service, team or severity: how many, how many are open, " +
			"how many a deployment caused, and the median recovery time. Unlike the DORA metrics, it includes incidents no deployment caused.",
		InputSchema: inputSchema[SummariseInput](),
	}), h.summariseIncidents)
	mcp.AddTool(s, readOnly(&mcp.Tool{
		Name:        "get_deployment",
		Description: "Show one deployment: the commits it shipped with their lead times, and the incidents it caused.",
	}), h.getDeployment)
	mcp.AddTool(s, readOnly(&mcp.Tool{
		Name: "find_commit",
		Description: "Find when a commit reached production, and in which deployment, from its SHA or the first 7 or more characters of it. " +
			"An empty list means no deployment has shipped it.",
	}), h.findCommit)
	return s
}

// choices gives the schema of each argument type that takes one of a fixed
// set of values, so that the model is shown the values it may use.
var choices = map[reflect.Type]*jsonschema.Schema{
	reflect.TypeFor[dora.Grouping]():         enum(dora.GroupByService, dora.GroupByTeam),
	reflect.TypeFor[dora.Interval]():         enum(dora.Weekly, dora.Monthly, dora.Quarterly),
	reflect.TypeFor[dora.Severity]():         enum(dora.Sev1, dora.Sev2, dora.Sev3),
	reflect.TypeFor[dora.Status]():           enum(dora.Open, dora.Resolved),
	reflect.TypeFor[dora.Cause]():            enum(dora.CausedByDeployment, dora.CausedByNothing),
	reflect.TypeFor[dora.IncidentOrder]():    enum(dora.IncidentsOldestFirst, dora.IncidentsNewestFirst, dora.LongestRecovery),
	reflect.TypeFor[dora.IncidentGrouping](): enum(dora.IncidentsByService, dora.IncidentsByTeam, dora.IncidentsBySeverity),
	reflect.TypeFor[IncidentWindow]():        enum(ByIncidentStart, ByCausingDeployment),
	reflect.TypeFor[dora.DeploymentOrder]():  enum(dora.DeploymentsOldestFirst, dora.DeploymentsNewestFirst, dora.LongestLeadTime),
}

func enum[T ~string](values ...T) *jsonschema.Schema {
	s := &jsonschema.Schema{Type: "string"}
	for _, v := range values {
		s.Enum = append(s.Enum, string(v))
	}
	return s
}

// inputSchema is the schema of a tool's input, with its choices filled in.
func inputSchema[In any]() *jsonschema.Schema {
	s, err := jsonschema.For[In](&jsonschema.ForOptions{TypeSchemas: choices})
	if err != nil {
		panic(fmt.Sprintf("input schema: %v", err))
	}
	return s
}

func readOnly(t *mcp.Tool) *mcp.Tool {
	closed := false
	t.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closed}
	return t
}

type handlers struct {
	store *store.Store
}

// Period is the date range a tool covers, both ends inclusive.
type Period struct {
	From    string `json:"from" jsonschema:"first day, as YYYY-MM-DD"`
	To      string `json:"to" jsonschema:"last day, as YYYY-MM-DD; the period includes this whole day"`
	Service string `json:"service,omitempty" jsonschema:"a service name from list_services; omit for every service"`
	Team    string `json:"team,omitempty" jsonschema:"a team name from list_services, for all of its services together; give this or service, not both"`
}

// filter checks the period and turns it into a store filter. Its error is
// written for the model, which can correct its arguments and try again.
func (h handlers) filter(ctx context.Context, p Period) (store.Filter, error) {
	from, err := time.Parse(dateLayout, p.From)
	if err != nil {
		return store.Filter{}, fmt.Errorf("from must be a date as YYYY-MM-DD, got %q", p.From)
	}
	to, err := time.Parse(dateLayout, p.To)
	if err != nil {
		return store.Filter{}, fmt.Errorf("to must be a date as YYYY-MM-DD, got %q", p.To)
	}
	if to.Before(from) {
		return store.Filter{}, fmt.Errorf("to (%s) is before from (%s)", p.To, p.From)
	}
	if p.Service != "" && p.Team != "" {
		return store.Filter{}, fmt.Errorf("give service or team, not both")
	}
	if p.Service != "" || p.Team != "" {
		services, err := h.store.Services(ctx)
		if err != nil {
			return store.Filter{}, err
		}
		if p.Service != "" && !slices.ContainsFunc(services, func(s dora.Service) bool { return s.Name == p.Service }) {
			return store.Filter{}, fmt.Errorf("there is no service %q; call list_services for the names", p.Service)
		}
		if p.Team != "" && !slices.ContainsFunc(services, func(s dora.Service) bool { return s.Team == p.Team }) {
			return store.Filter{}, fmt.Errorf("there is no team %q; call list_services for the names", p.Team)
		}
	}
	return store.Filter{
		Service: p.Service,
		Team:    p.Team,
		Window:  dora.Window{From: from, To: to.AddDate(0, 0, 1)},
	}, nil
}

// ServicesResult is the output of list_services.
type ServicesResult struct {
	Services []dora.Service `json:"services"`
	DataFrom string         `json:"data_from,omitempty" jsonschema:"date of the first deployment, as YYYY-MM-DD"`
	DataTo   string         `json:"data_to,omitempty" jsonschema:"date of the last deployment, as YYYY-MM-DD"`
}

func (h handlers) listServices(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ServicesResult, error) {
	services, err := h.store.Services(ctx)
	if err != nil {
		return nil, ServicesResult{}, err
	}
	first, last, err := h.store.DataRange(ctx)
	if err != nil {
		return nil, ServicesResult{}, err
	}
	result := ServicesResult{Services: services}
	if !first.IsZero() {
		result.DataFrom, result.DataTo = first.Format(dateLayout), last.Format(dateLayout)
	}
	return nil, result, nil
}

// MetricsResult is the output of get_dora_metrics.
type MetricsResult struct {
	Period
	dora.Metrics
}

func (h handlers) getDoraMetrics(ctx context.Context, _ *mcp.CallToolRequest, p Period) (*mcp.CallToolResult, MetricsResult, error) {
	f, err := h.filter(ctx, p)
	if err != nil {
		return nil, MetricsResult{}, err
	}
	r, err := h.metricsRecords(ctx, f)
	if err != nil {
		return nil, MetricsResult{}, err
	}
	return nil, MetricsResult{Period: p, Metrics: dora.Calculate(f.Window, r.Deployments, r.Changes, r.Incidents)}, nil
}

// CompareInput selects the metrics compare_dora_metrics compares.
type CompareInput struct {
	From    string        `json:"from" jsonschema:"first day, as YYYY-MM-DD"`
	To      string        `json:"to" jsonschema:"last day, as YYYY-MM-DD; the period includes this whole day"`
	GroupBy dora.Grouping `json:"group_by" jsonschema:"whether to compare services or teams"`
	Team    string        `json:"team,omitempty" jsonschema:"a team name from list_services, to compare only its services; omit for every team"`
}

// CompareResult is the output of compare_dora_metrics.
type CompareResult struct {
	From    string              `json:"from"`
	To      string              `json:"to"`
	GroupBy dora.Grouping       `json:"group_by"`
	Groups  []dora.GroupMetrics `json:"groups" jsonschema:"one entry per service or team, ordered by name"`
}

func (h handlers) compareDoraMetrics(ctx context.Context, _ *mcp.CallToolRequest, in CompareInput) (*mcp.CallToolResult, CompareResult, error) {
	f, err := h.filter(ctx, Period{From: in.From, To: in.To, Team: in.Team})
	if err != nil {
		return nil, CompareResult{}, err
	}
	r, err := h.metricsRecords(ctx, f)
	if err != nil {
		return nil, CompareResult{}, err
	}
	groups := dora.CalculateByGroup(f.Window, r.Services, in.GroupBy, r.Deployments, r.Changes, r.Incidents)
	return nil, CompareResult{From: in.From, To: in.To, GroupBy: in.GroupBy, Groups: groups}, nil
}

// TrendInput selects the metrics get_dora_trend calculates.
type TrendInput struct {
	Period
	Interval dora.Interval `json:"interval" jsonschema:"the length of each bucket"`
}

// TrendResult is the output of get_dora_trend.
type TrendResult struct {
	Period
	Interval dora.Interval `json:"interval"`
	Points   []TrendPoint  `json:"points" jsonschema:"one entry per bucket, oldest first"`
}

// TrendPoint is the metrics of one bucket of a trend.
type TrendPoint struct {
	From    string `json:"from" jsonschema:"first day of the bucket, as YYYY-MM-DD"`
	To      string `json:"to" jsonschema:"last day of the bucket, as YYYY-MM-DD; the bucket includes this whole day"`
	Partial bool   `json:"partial" jsonschema:"true when the period cuts the bucket short, so it covers less than a whole week, month or quarter"`
	dora.Metrics
}

func (h handlers) getDoraTrend(ctx context.Context, _ *mcp.CallToolRequest, in TrendInput) (*mcp.CallToolResult, TrendResult, error) {
	f, err := h.filter(ctx, in.Period)
	if err != nil {
		return nil, TrendResult{}, err
	}
	if n := len(dora.Buckets(f.Window, in.Interval)); n > maxBuckets {
		return nil, TrendResult{}, fmt.Errorf("that is %d buckets, more than the %d allowed; use a longer interval or a shorter period",
			n, maxBuckets)
	}
	r, err := h.metricsRecords(ctx, f)
	if err != nil {
		return nil, TrendResult{}, err
	}
	points := []TrendPoint{}
	for _, p := range dora.CalculateTrend(f.Window, in.Interval, r.Deployments, r.Changes, r.Incidents) {
		points = append(points, TrendPoint{
			From:    p.From.Format(dateLayout),
			To:      p.To.AddDate(0, 0, -1).Format(dateLayout),
			Partial: p.Partial,
			Metrics: p.Metrics,
		})
	}
	return nil, TrendResult{Period: in.Period, Interval: in.Interval, Points: points}, nil
}

// SummariseInput selects the incidents summarise_incidents summarises.
type SummariseInput struct {
	Period
	GroupBy dora.IncidentGrouping `json:"group_by" jsonschema:"whether to summarise by service, team or severity"`
}

// SummariseResult is the output of summarise_incidents.
type SummariseResult struct {
	Period
	GroupBy dora.IncidentGrouping `json:"group_by"`
	Groups  []dora.IncidentGroup  `json:"groups" jsonschema:"one entry per service, team or severity, ordered by name"`
}

func (h handlers) summariseIncidents(ctx context.Context, _ *mcp.CallToolRequest, in SummariseInput) (*mcp.CallToolResult, SummariseResult, error) {
	f, err := h.filter(ctx, in.Period)
	if err != nil {
		return nil, SummariseResult{}, err
	}
	services, err := h.servicesIn(ctx, f)
	if err != nil {
		return nil, SummariseResult{}, err
	}
	incidents, err := h.store.Incidents(ctx, f)
	if err != nil {
		return nil, SummariseResult{}, err
	}
	return nil, SummariseResult{Period: in.Period, GroupBy: in.GroupBy, Groups: dora.SummariseIncidents(services, in.GroupBy, incidents)}, nil
}

// DeploymentInput selects the deployment get_deployment shows.
type DeploymentInput struct {
	ID int64 `json:"id" jsonschema:"a deployment id, as list_deployments and list_incidents give them"`
}

// DeploymentResult is the output of get_deployment.
type DeploymentResult struct {
	Deployment dora.DeploymentSummary `json:"deployment"`
	Commits    []Commit               `json:"commits" jsonschema:"the commits it shipped, oldest first"`
	Incidents  []IncidentRow          `json:"incidents" jsonschema:"the incidents it caused, oldest first"`
}

// Commit is a commit a deployment shipped.
type Commit struct {
	SHA           string    `json:"sha"`
	CommittedAt   time.Time `json:"committed_at"`
	LeadTimeHours float64   `json:"lead_time_hours" jsonschema:"hours from the commit to the deployment"`
}

func (h handlers) getDeployment(ctx context.Context, _ *mcp.CallToolRequest, in DeploymentInput) (*mcp.CallToolResult, DeploymentResult, error) {
	d, ok, err := h.store.Deployment(ctx, in.ID)
	if err != nil {
		return nil, DeploymentResult{}, err
	}
	if !ok {
		return nil, DeploymentResult{}, fmt.Errorf("there is no deployment %d; list_deployments gives their ids", in.ID)
	}
	changes, err := h.store.ChangesShippedBy(ctx, d.ID)
	if err != nil {
		return nil, DeploymentResult{}, err
	}
	incidents, err := h.store.IncidentsCausedBy(ctx, d.ID)
	if err != nil {
		return nil, DeploymentResult{}, err
	}
	result := DeploymentResult{
		Deployment: dora.SummariseDeployments([]dora.Deployment{d}, changes, incidents)[0],
		Commits:    []Commit{},
		Incidents:  []IncidentRow{},
	}
	for _, c := range changes {
		result.Commits = append(result.Commits, Commit{SHA: c.SHA, CommittedAt: c.CommittedAt, LeadTimeHours: d.LeadTimeHours(c)})
	}
	for _, i := range incidents {
		result.Incidents = append(result.Incidents, IncidentRow{Incident: i, RecoveryHours: i.RecoveryHours()})
	}
	return nil, result, nil
}

// FindCommitInput is the commit find_commit looks for.
type FindCommitInput struct {
	SHA string `json:"sha" jsonschema:"the commit's SHA, or at least its first 7 characters"`
}

// FindCommitResult is the output of find_commit.
type FindCommitResult struct {
	Commits   []ShippedCommit `json:"commits" jsonschema:"the commits whose SHA starts with the one given, oldest first; more than one means the prefix is ambiguous"`
	Truncated bool            `json:"truncated" jsonschema:"true when more commits matched than are listed"`
}

// ShippedCommit is a commit and the deployment that shipped it.
type ShippedCommit struct {
	Commit
	DeploymentID int64     `json:"deployment_id"`
	Service      string    `json:"service"`
	DeployedAt   time.Time `json:"deployed_at"`
}

var shaPrefix = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func (h handlers) findCommit(ctx context.Context, _ *mcp.CallToolRequest, in FindCommitInput) (*mcp.CallToolResult, FindCommitResult, error) {
	sha := strings.ToLower(in.SHA)
	if !shaPrefix.MatchString(sha) {
		return nil, FindCommitResult{}, fmt.Errorf("sha must be 7 to 40 hexadecimal characters, got %q", in.SHA)
	}
	changes, err := h.store.FindChanges(ctx, sha, maxCommits+1)
	if err != nil {
		return nil, FindCommitResult{}, err
	}
	changes, truncated := truncate(changes, maxCommits)
	result := FindCommitResult{Commits: []ShippedCommit{}, Truncated: truncated}
	for _, c := range changes {
		d, ok, err := h.store.Deployment(ctx, c.DeploymentID)
		if err != nil {
			return nil, FindCommitResult{}, err
		}
		if !ok {
			return nil, FindCommitResult{}, fmt.Errorf("change %d refers to missing deployment %d", c.ID, c.DeploymentID)
		}
		result.Commits = append(result.Commits, ShippedCommit{
			Commit:       Commit{SHA: c.SHA, CommittedAt: c.CommittedAt, LeadTimeHours: d.LeadTimeHours(c)},
			DeploymentID: d.ID,
			Service:      d.Service,
			DeployedAt:   d.DeployedAt,
		})
	}
	return nil, result, nil
}

// metricsRecords gathers the records the metrics for f are calculated from:
// the services in scope, the deployments made in the window, the changes
// they shipped and the incidents they caused.
func (h handlers) metricsRecords(ctx context.Context, f store.Filter) (dora.Records, error) {
	services, err := h.servicesIn(ctx, f)
	if err != nil {
		return dora.Records{}, err
	}
	deployments, err := h.store.Deployments(ctx, f)
	if err != nil {
		return dora.Records{}, err
	}
	changes, err := h.store.Changes(ctx, f)
	if err != nil {
		return dora.Records{}, err
	}
	incidents, err := h.store.IncidentsCausedByDeployments(ctx, f)
	if err != nil {
		return dora.Records{}, err
	}
	return dora.Records{Services: services, Deployments: deployments, Changes: changes, Incidents: incidents}, nil
}

// servicesIn returns the services f covers.
func (h handlers) servicesIn(ctx context.Context, f store.Filter) ([]dora.Service, error) {
	services, err := h.store.Services(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(services, func(s dora.Service) bool {
		return (f.Service != "" && s.Name != f.Service) || (f.Team != "" && s.Team != f.Team)
	}), nil
}

// ListInput selects the records list_deployments and list_incidents return.
type ListInput struct {
	Period
	Limit int `json:"limit,omitempty" jsonschema:"most records to return; default 100, at most 500"`
}

// DeploymentsInput selects the records list_deployments returns.
type DeploymentsInput struct {
	ListInput
	Failed      *bool                `json:"failed,omitempty" jsonschema:"true for only deployments that caused an incident, false for only those that caused none; omit for both"`
	Remediation *bool                `json:"remediation,omitempty" jsonschema:"true for only remediation deployments, false for only planned ones; omit for both"`
	Sort        dora.DeploymentOrder `json:"sort,omitempty" jsonschema:"the order to list them in; longest_lead_time lists those that shipped no commits last"`
}

// DeploymentsResult is the output of list_deployments.
type DeploymentsResult struct {
	Deployments []dora.DeploymentSummary `json:"deployments"`
	Truncated   bool                     `json:"truncated" jsonschema:"true when more records matched than the limit allowed"`
}

func (h handlers) listDeployments(ctx context.Context, _ *mcp.CallToolRequest, in DeploymentsInput) (*mcp.CallToolResult, DeploymentsResult, error) {
	f, limit, err := h.listFilter(ctx, in.ListInput)
	if err != nil {
		return nil, DeploymentsResult{}, err
	}
	r, err := h.metricsRecords(ctx, f)
	if err != nil {
		return nil, DeploymentsResult{}, err
	}
	summaries := dora.SelectDeployments(dora.SummariseDeployments(r.Deployments, r.Changes, r.Incidents),
		dora.DeploymentFilter{Failed: in.Failed, Remediation: in.Remediation})
	kept, truncated := truncate(dora.SortDeployments(summaries, in.Sort), limit)
	return nil, DeploymentsResult{Deployments: kept, Truncated: truncated}, nil
}

// IncidentWindow is which time of an incident the period of a listing
// applies to.
type IncidentWindow string

// The times a listing of incidents can be selected by.
const (
	ByIncidentStart     IncidentWindow = "incident_start"
	ByCausingDeployment IncidentWindow = "causing_deployment"
)

// IncidentsInput selects the records list_incidents returns.
type IncidentsInput struct {
	ListInput
	WindowAppliesTo IncidentWindow     `json:"window_applies_to,omitempty" jsonschema:"incident_start (the default) for incidents that started in the period, or causing_deployment for incidents caused by deployments made in it, whenever they started"`
	Severity        dora.Severity      `json:"severity,omitempty" jsonschema:"only incidents of this severity; omit for every severity"`
	Status          dora.Status        `json:"status,omitempty" jsonschema:"only open or only resolved incidents; omit for both"`
	CausedBy        dora.Cause         `json:"caused_by,omitempty" jsonschema:"only incidents a deployment caused, or only those none did; omit for both"`
	Sort            dora.IncidentOrder `json:"sort,omitempty" jsonschema:"the order to list them in; longest_recovery lists resolved incidents slowest first, then open ones"`
}

// IncidentsResult is the output of list_incidents.
type IncidentsResult struct {
	Incidents []IncidentRow `json:"incidents"`
	Truncated bool          `json:"truncated" jsonschema:"true when more records matched than the limit allowed"`
}

// IncidentRow is an incident as list_incidents and get_deployment give it.
type IncidentRow struct {
	dora.Incident
	RecoveryHours *float64 `json:"recovery_hours" jsonschema:"hours from the start of the incident to its resolution; null while it is open"`
}

func (h handlers) listIncidents(ctx context.Context, _ *mcp.CallToolRequest, in IncidentsInput) (*mcp.CallToolResult, IncidentsResult, error) {
	if in.WindowAppliesTo == ByCausingDeployment && in.CausedBy == dora.CausedByNothing {
		return nil, IncidentsResult{}, fmt.Errorf("incidents no deployment caused have no causing deployment to select by; " +
			"use window_applies_to=incident_start")
	}
	f, limit, err := h.listFilter(ctx, in.ListInput)
	if err != nil {
		return nil, IncidentsResult{}, err
	}
	fetch := h.store.Incidents
	if in.WindowAppliesTo == ByCausingDeployment {
		fetch = h.store.IncidentsCausedByDeployments
	}
	incidents, err := fetch(ctx, f)
	if err != nil {
		return nil, IncidentsResult{}, err
	}
	incidents = dora.SelectIncidents(incidents, dora.IncidentFilter{Severity: in.Severity, Status: in.Status, CausedBy: in.CausedBy})
	kept, truncated := truncate(dora.SortIncidents(incidents, in.Sort), limit)
	rows := []IncidentRow{}
	for _, i := range kept {
		rows = append(rows, IncidentRow{Incident: i, RecoveryHours: i.RecoveryHours()})
	}
	return nil, IncidentsResult{Incidents: rows, Truncated: truncated}, nil
}

// listFilter checks a listing's input and turns it into a store filter and
// the number of records to keep.
func (h handlers) listFilter(ctx context.Context, in ListInput) (store.Filter, int, error) {
	limit := in.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 1 || limit > maxLimit {
		return store.Filter{}, 0, fmt.Errorf("limit must be between 1 and %d, got %d", maxLimit, in.Limit)
	}
	f, err := h.filter(ctx, in.Period)
	return f, limit, err
}

func truncate[T any](items []T, limit int) ([]T, bool) {
	if len(items) > limit {
		return items[:limit], true
	}
	return items, false
}
