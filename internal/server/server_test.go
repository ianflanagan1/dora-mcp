package server_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ianflanagan1/dora-mcp/internal/dora"
	"github.com/ianflanagan1/dora-mcp/internal/server"
	"github.com/ianflanagan1/dora-mcp/internal/store"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr[T any](v T) *T { return &v }

var fixture = dora.Records{
	Services: []dora.Service{{Name: "api", Team: "Core"}, {Name: "web", Team: "Front"}, {Name: "worker", Team: "Core"}},
	Deployments: []dora.Deployment{
		{ID: 1, Service: "api", DeployedAt: at("2026-03-02T10:00:00Z")},
		{ID: 2, Service: "api", DeployedAt: at("2026-03-03T10:00:00Z"), Remediation: true},
		{ID: 3, Service: "web", DeployedAt: at("2026-03-04T10:00:00Z")},
		{ID: 4, Service: "api", DeployedAt: at("2026-03-08T23:00:00Z")},
		{ID: 5, Service: "worker", DeployedAt: at("2026-03-05T10:00:00Z")},
		{ID: 6, Service: "web", DeployedAt: at("2026-03-08T22:00:00Z")},
	},
	Changes: []dora.Change{
		{ID: 1, DeploymentID: 1, SHA: "aaaaaaaa11111111", CommittedAt: at("2026-03-02T08:00:00Z")},
		{ID: 2, DeploymentID: 2, SHA: "bbbbbbbb22222222", CommittedAt: at("2026-03-03T06:00:00Z")},
	},
	Incidents: []dora.Incident{
		{ID: 1, Service: "api", CausedByDeploymentID: ptr[int64](1), Severity: "sev2", Title: "Errors",
			StartedAt: at("2026-03-02T11:00:00Z"), ResolvedAt: ptr(at("2026-03-02T14:00:00Z"))},
		{ID: 2, Service: "web", Severity: "sev3", Title: "Network blip", StartedAt: at("2026-03-05T09:00:00Z")},
		{ID: 3, Service: "worker", CausedByDeploymentID: ptr[int64](5), Severity: "sev1", Title: "Jobs stuck",
			StartedAt: at("2026-03-05T11:00:00Z"), ResolvedAt: ptr(at("2026-03-05T12:00:00Z"))},
		{ID: 4, Service: "web", CausedByDeploymentID: ptr[int64](6), Severity: "sev2", Title: "Slow pages",
			StartedAt: at("2026-03-09T01:00:00Z"), ResolvedAt: ptr(at("2026-03-09T09:00:00Z"))},
	},
}

func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dora.sqlite")
	if err := store.Create(t.Context(), path, fixture); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.New(st, "test").Connect(t.Context(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// call calls a tool that is expected to succeed and decodes its structured
// output into out.
func call(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any, out any) {
	t.Helper()
	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("%s failed: %s", tool, text(res))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}

// callError calls a tool that is expected to fail and returns the message the
// model would see.
func callError(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) string {
	t.Helper()
	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("%s succeeded, want a tool error", tool)
	}
	return text(res)
}

func text(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestEveryToolIsAdvertisedAsReadOnly(t *testing.T) {
	res, err := connect(t).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tool.Name)
		}
	}
	slices.Sort(names)
	want := []string{"compare_dora_metrics", "find_commit", "get_deployment", "get_dora_metrics", "get_dora_trend",
		"list_deployments", "list_incidents", "list_services", "summarise_incidents"}
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
}

// property returns the input schema a tool advertises for one argument.
func property(t *testing.T, s *mcp.ClientSession, tool, name string) map[string]any {
	t.Helper()
	res, err := s.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		if tl.Name != tool {
			continue
		}
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct{ Properties map[string]map[string]any }
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if p, ok := schema.Properties[name]; ok {
			return p
		}
		t.Fatalf("%s has no argument %q", tool, name)
	}
	t.Fatalf("there is no tool %q", tool)
	return nil
}

func TestChoicesAreAdvertisedSoTheModelKnowsTheAllowedValues(t *testing.T) {
	s := connect(t)
	for _, tc := range []struct{ tool, argument, want string }{
		{"compare_dora_metrics", "group_by", "[service team]"},
		{"get_dora_trend", "interval", "[week month quarter]"},
		{"list_deployments", "sort", "[oldest_first newest_first longest_lead_time]"},
		{"list_incidents", "window_applies_to", "[incident_start causing_deployment]"},
		{"list_incidents", "severity", "[sev1 sev2 sev3]"},
		{"list_incidents", "status", "[open resolved]"},
		{"list_incidents", "caused_by", "[deployment none]"},
		{"list_incidents", "sort", "[oldest_first newest_first longest_recovery]"},
		{"summarise_incidents", "group_by", "[service team severity]"},
	} {
		t.Run(tc.tool+" "+tc.argument, func(t *testing.T) {
			p := property(t, s, tc.tool, tc.argument)
			if got := fmt.Sprint(p["enum"]); got != tc.want {
				t.Errorf("enum = %s, want %s", got, tc.want)
			}
			if p["description"] == nil {
				t.Error("no description")
			}
		})
	}
}

func TestListServicesGivesTheServicesAndTheDatesTheDataCovers(t *testing.T) {
	var out struct {
		Services []struct{ Name, Team string }
		DataFrom string `json:"data_from"`
		DataTo   string `json:"data_to"`
	}
	call(t, connect(t), "list_services", map[string]any{}, &out)

	if len(out.Services) != 3 || out.Services[0].Name != "api" || out.Services[0].Team != "Core" {
		t.Errorf("services = %+v", out.Services)
	}
	if out.DataFrom != "2026-03-02" || out.DataTo != "2026-03-08" {
		t.Errorf("data range = %s to %s", out.DataFrom, out.DataTo)
	}
}

func TestMetricsIncludeTheWholeOfTheLastDay(t *testing.T) {
	var out struct {
		Deployments       int      `json:"deployments"`
		ChangeFailureRate *float64 `json:"change_failure_rate"`
		LeadTimeMedian    *float64 `json:"lead_time_median_hours"`
		ReworkRate        *float64 `json:"rework_rate"`
		RecoveryMedian    *float64 `json:"recovery_median_hours"`
	}
	call(t, connect(t), "get_dora_metrics",
		map[string]any{"from": "2026-03-02", "to": "2026-03-08", "service": "api"}, &out)

	if out.Deployments != 3 {
		t.Errorf("deployments = %d, want 3", out.Deployments)
	}
	if out.ChangeFailureRate == nil || *out.ChangeFailureRate != 1.0/3 {
		t.Errorf("change failure rate = %v, want 1/3", out.ChangeFailureRate)
	}
	if out.LeadTimeMedian == nil || *out.LeadTimeMedian != 3 {
		t.Errorf("lead time median = %v, want 3", out.LeadTimeMedian)
	}
	if out.RecoveryMedian == nil || *out.RecoveryMedian != 3 {
		t.Errorf("recovery median = %v, want 3", out.RecoveryMedian)
	}
}

func TestTeamMetricsPoolTheDeploymentsOfTheTeamsServices(t *testing.T) {
	var out struct {
		Deployments       int      `json:"deployments"`
		FailedDeployments int      `json:"failed_deployments"`
		ChangeFailureRate *float64 `json:"change_failure_rate"`
	}
	call(t, connect(t), "get_dora_metrics",
		map[string]any{"from": "2026-03-02", "to": "2026-03-08", "team": "Core"}, &out)

	// api has 3 deployments, 1 failed; worker has 1, failed. Pooled that is
	// 2 of 4, where the mean of the two services' rates would be 2/3.
	if out.Deployments != 4 || out.FailedDeployments != 2 {
		t.Errorf("deployments = %d, failed = %d, want 4 and 2", out.Deployments, out.FailedDeployments)
	}
	if out.ChangeFailureRate == nil || *out.ChangeFailureRate != 0.5 {
		t.Errorf("change failure rate = %v, want 0.5", out.ChangeFailureRate)
	}
}

type comparison struct {
	Groups []struct {
		Name              string
		Deployments       int
		ChangeFailureRate *float64 `json:"change_failure_rate"`
	}
}

func TestComparingByTeamGivesEachTeamsPooledMetrics(t *testing.T) {
	var out comparison
	call(t, connect(t), "compare_dora_metrics",
		map[string]any{"from": "2026-03-02", "to": "2026-03-08", "group_by": "team"}, &out)

	if len(out.Groups) != 2 || out.Groups[0].Name != "Core" || out.Groups[1].Name != "Front" {
		t.Fatalf("groups = %+v", out.Groups)
	}
	if core := out.Groups[0]; core.Deployments != 4 || core.ChangeFailureRate == nil || *core.ChangeFailureRate != 0.5 {
		t.Errorf("Core = %+v, want 4 deployments at 0.5", core)
	}
}

func TestComparingServicesWithinATeamListsOnlyThatTeamsServices(t *testing.T) {
	var out comparison
	call(t, connect(t), "compare_dora_metrics",
		map[string]any{"from": "2026-03-02", "to": "2026-03-08", "group_by": "service", "team": "Core"}, &out)

	if len(out.Groups) != 2 || out.Groups[0].Name != "api" || out.Groups[1].Name != "worker" {
		t.Errorf("groups = %+v, want api and worker", out.Groups)
	}
}

func TestAComparisonNeedsAGroupingItKnows(t *testing.T) {
	got := callError(t, connect(t), "compare_dora_metrics",
		map[string]any{"from": "2026-03-02", "to": "2026-03-08", "group_by": "colour"})

	if !strings.Contains(got, "group_by") {
		t.Errorf("error %q does not mention group_by", got)
	}
}

func TestATrendGivesTheMetricsOfEachBucketBetweenInclusiveDates(t *testing.T) {
	var out struct {
		Points []struct {
			From        string
			To          string
			Partial     bool
			Deployments int
		}
	}
	call(t, connect(t), "get_dora_trend",
		map[string]any{"from": "2026-03-04", "to": "2026-03-15", "interval": "week"}, &out)

	if len(out.Points) != 2 {
		t.Fatalf("points = %+v, want 2", out.Points)
	}
	first, second := out.Points[0], out.Points[1]
	if first.From != "2026-03-04" || first.To != "2026-03-08" || !first.Partial || first.Deployments != 4 {
		t.Errorf("first point = %+v, want 2026-03-04 to 2026-03-08, partial, 4 deployments", first)
	}
	if second.From != "2026-03-09" || second.To != "2026-03-15" || second.Partial || second.Deployments != 0 {
		t.Errorf("second point = %+v, want 2026-03-09 to 2026-03-15, whole, 0 deployments", second)
	}
}

func TestATrendWithTooManyBucketsIsRefusedWithAWayForward(t *testing.T) {
	got := callError(t, connect(t), "get_dora_trend",
		map[string]any{"from": "2024-01-01", "to": "2026-12-31", "interval": "week"})

	if !strings.Contains(got, "longer interval") {
		t.Errorf("error %q does not suggest a longer interval", got)
	}
}

func TestMetricsWithNoDeploymentsReportRatesAsNull(t *testing.T) {
	var out map[string]any
	call(t, connect(t), "get_dora_metrics", map[string]any{"from": "2026-01-01", "to": "2026-01-31"}, &out)

	if out["deployments"] != 0.0 || out["change_failure_rate"] != nil {
		t.Errorf("deployments = %v, change_failure_rate = %v, want 0 and null",
			out["deployments"], out["change_failure_rate"])
	}
}

func TestBadArgumentsAreExplainedSoTheModelCanCorrectThem(t *testing.T) {
	s := connect(t)
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"from": "March", "to": "2026-03-08"}, "YYYY-MM-DD"},
		{map[string]any{"from": "2026-03-08", "to": "2026-03-01"}, "before"},
		{map[string]any{"from": "2026-03-01", "to": "2026-03-08", "service": "nope"}, "list_services"},
		{map[string]any{"from": "2026-03-01", "to": "2026-03-08", "team": "Nope"}, "list_services"},
		{map[string]any{"from": "2026-03-01", "to": "2026-03-08", "service": "api", "team": "Core"}, "not both"},
	} {
		if got := callError(t, s, "get_dora_metrics", tc.args); !strings.Contains(got, tc.want) {
			t.Errorf("args %v: error %q does not mention %q", tc.args, got, tc.want)
		}
	}
}

func TestListingsSayWhenTheLimitLeftRecordsOut(t *testing.T) {
	var out struct {
		Deployments []struct{ ID int64 }
		Truncated   bool
	}
	call(t, connect(t), "list_deployments", map[string]any{"from": "2026-03-01", "to": "2026-03-31", "limit": 2}, &out)

	if len(out.Deployments) != 2 || out.Deployments[0].ID != 1 || !out.Truncated {
		t.Errorf("deployments = %+v, truncated = %v", out.Deployments, out.Truncated)
	}
}

func TestAListingThatFitsTheLimitIsNotTruncated(t *testing.T) {
	var out struct {
		Incidents []struct {
			Title                string
			CausedByDeploymentID *int64 `json:"caused_by_deployment_id"`
		}
		Truncated bool
	}
	call(t, connect(t), "list_incidents", map[string]any{"from": "2026-03-01", "to": "2026-03-31"}, &out)

	if len(out.Incidents) != 4 || out.Truncated {
		t.Errorf("incidents = %+v, truncated = %v", out.Incidents, out.Truncated)
	}
	if out.Incidents[1].CausedByDeploymentID != nil {
		t.Errorf("incident 2 caused by %v, want null", *out.Incidents[1].CausedByDeploymentID)
	}
}

type incidentListing struct {
	Incidents []struct {
		ID            int64
		RecoveryHours *float64 `json:"recovery_hours"`
	}
	Truncated bool
}

func (l incidentListing) ids() []int64 {
	ids := []int64{}
	for _, i := range l.Incidents {
		ids = append(ids, i.ID)
	}
	return ids
}

func listIncidents(t *testing.T, s *mcp.ClientSession, args map[string]any) incidentListing {
	t.Helper()
	args["from"], args["to"] = "2026-03-02", "2026-03-08"
	var out incidentListing
	call(t, s, "list_incidents", args, &out)
	return out
}

func TestTheIncidentsBehindAPeriodsFailuresIncludeOnesThatStartedAfterIt(t *testing.T) {
	s := connect(t)

	if got := listIncidents(t, s, map[string]any{}).ids(); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Errorf("incidents started in the period = %v, want [1 2 3]", got)
	}
	got := listIncidents(t, s, map[string]any{"window_applies_to": "causing_deployment"}).ids()
	if !slices.Equal(got, []int64{1, 3, 4}) {
		t.Errorf("incidents caused by deployments in the period = %v, want [1 3 4]", got)
	}
}

func TestIncidentsAreFilteredByTeamSeverityStatusAndCause(t *testing.T) {
	s := connect(t)
	for _, tc := range []struct {
		name string
		args map[string]any
		want []int64
	}{
		{"by team", map[string]any{"team": "Front"}, []int64{2}},
		{"by severity", map[string]any{"severity": "sev1"}, []int64{3}},
		{"by status", map[string]any{"status": "open"}, []int64{2}},
		{"by cause", map[string]any{"caused_by": "none"}, []int64{2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := listIncidents(t, s, tc.args).ids(); !slices.Equal(got, tc.want) {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIncidentsCanBeListedSlowestRecoveryFirstWithTheirRecoveryTimes(t *testing.T) {
	out := listIncidents(t, connect(t), map[string]any{"window_applies_to": "causing_deployment", "sort": "longest_recovery"})

	if got := out.ids(); !slices.Equal(got, []int64{4, 1, 3}) {
		t.Fatalf("order = %v, want [4 1 3]", got)
	}
	if r := out.Incidents[0].RecoveryHours; r == nil || *r != 8 {
		t.Errorf("recovery hours = %v, want 8", r)
	}
}

func TestIncidentsNoDeploymentCausedCannotBeSelectedByTheirCausingDeployment(t *testing.T) {
	got := callError(t, connect(t), "list_incidents", map[string]any{"from": "2026-03-02", "to": "2026-03-08",
		"window_applies_to": "causing_deployment", "caused_by": "none"})

	if !strings.Contains(got, "incident_start") {
		t.Errorf("error %q does not say to select by incident_start", got)
	}
}

func TestAListingLimitAppliesAfterTheFilters(t *testing.T) {
	out := listIncidents(t, connect(t), map[string]any{"severity": "sev1", "limit": 1})

	if got := out.ids(); !slices.Equal(got, []int64{3}) || out.Truncated {
		t.Errorf("ids = %v, truncated = %v, want [3] and false", got, out.Truncated)
	}
}

type deploymentListing struct {
	Deployments []struct {
		ID                   int64
		Changes              int
		LongestLeadTimeHours *float64 `json:"longest_lead_time_hours"`
		Failed               bool
		CausedIncidentIDs    []int64 `json:"caused_incident_ids"`
	}
}

func (l deploymentListing) ids() []int64 {
	ids := []int64{}
	for _, d := range l.Deployments {
		ids = append(ids, d.ID)
	}
	return ids
}

func listDeployments(t *testing.T, s *mcp.ClientSession, args map[string]any) deploymentListing {
	t.Helper()
	args["from"], args["to"] = "2026-03-02", "2026-03-08"
	var out deploymentListing
	call(t, s, "list_deployments", args, &out)
	return out
}

func TestDeploymentsAreListedWithWhatTheyShippedAndTheIncidentsTheyCaused(t *testing.T) {
	out := listDeployments(t, connect(t), map[string]any{})

	first := out.Deployments[0]
	if first.Changes != 1 || first.LongestLeadTimeHours == nil || *first.LongestLeadTimeHours != 2 {
		t.Errorf("deployment 1 = %+v, want 1 change with a 2 hour lead time", first)
	}
	if !first.Failed || !slices.Equal(first.CausedIncidentIDs, []int64{1}) {
		t.Errorf("deployment 1 = %+v, want failed, causing incident 1", first)
	}
}

func TestDeploymentsAreFilteredByFailureAndRemediation(t *testing.T) {
	s := connect(t)
	for _, tc := range []struct {
		name string
		args map[string]any
		want []int64
	}{
		{"failed", map[string]any{"failed": true}, []int64{1, 5, 6}},
		{"remediation", map[string]any{"remediation": true}, []int64{2}},
		{"a team's deployments that did not fail", map[string]any{"team": "Core", "failed": false}, []int64{2, 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := listDeployments(t, s, tc.args).ids(); !slices.Equal(got, tc.want) {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeploymentsCanBeListedByLongestLeadTime(t *testing.T) {
	got := listDeployments(t, connect(t), map[string]any{"sort": "longest_lead_time"}).ids()

	// Deployments 3, 5, 6 and 4 shipped no commits, so they follow, oldest first.
	if want := []int64{2, 1, 3, 5, 6, 4}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestADeploymentIsShownWithTheCommitsItShippedAndTheIncidentsItCaused(t *testing.T) {
	var out struct {
		Deployment struct {
			ID     int64
			Failed bool
		}
		Commits []struct {
			SHA           string
			LeadTimeHours float64 `json:"lead_time_hours"`
		}
		Incidents []struct{ Title string }
	}
	call(t, connect(t), "get_deployment", map[string]any{"id": 1}, &out)

	if out.Deployment.ID != 1 || !out.Deployment.Failed {
		t.Errorf("deployment = %+v, want 1, failed", out.Deployment)
	}
	if len(out.Commits) != 1 || out.Commits[0].SHA != "aaaaaaaa11111111" || out.Commits[0].LeadTimeHours != 2 {
		t.Errorf("commits = %+v, want aaaaaaaa11111111 with a 2 hour lead time", out.Commits)
	}
	if len(out.Incidents) != 1 || out.Incidents[0].Title != "Errors" {
		t.Errorf("incidents = %+v, want Errors", out.Incidents)
	}
}

func TestAnUnknownDeploymentIsReportedWithWhereToFindTheirIDs(t *testing.T) {
	got := callError(t, connect(t), "get_deployment", map[string]any{"id": 99})

	if !strings.Contains(got, "list_deployments") {
		t.Errorf("error %q does not mention list_deployments", got)
	}
}

func TestACommitIsTracedToTheDeploymentThatShippedItByAPrefixOfItsSHA(t *testing.T) {
	var out struct {
		Commits []struct {
			SHA           string
			DeploymentID  int64 `json:"deployment_id"`
			Service       string
			DeployedAt    string  `json:"deployed_at"`
			LeadTimeHours float64 `json:"lead_time_hours"`
		}
	}
	call(t, connect(t), "find_commit", map[string]any{"sha": "BBBBBBBB2"}, &out)

	if len(out.Commits) != 1 {
		t.Fatalf("commits = %+v, want 1", out.Commits)
	}
	c := out.Commits[0]
	if c.DeploymentID != 2 || c.Service != "api" || c.DeployedAt != "2026-03-03T10:00:00Z" || c.LeadTimeHours != 4 {
		t.Errorf("commit = %+v, want deployment 2 of api at 2026-03-03T10:00:00Z, 4 hours after commit", c)
	}
}

func TestACommitNotDeployedIsAnEmptyResultNotAnError(t *testing.T) {
	var out struct{ Commits []any }
	call(t, connect(t), "find_commit", map[string]any{"sha": "1234567"}, &out)

	if out.Commits == nil || len(out.Commits) != 0 {
		t.Errorf("commits = %v, want an empty list", out.Commits)
	}
}

func TestACommitSearchNeedsAtLeastSevenHexDigits(t *testing.T) {
	s := connect(t)
	for _, sha := range []string{"aaaaaa", "aaaaaaa%", "not-a-sha"} {
		if got := callError(t, s, "find_commit", map[string]any{"sha": sha}); !strings.Contains(got, "7") {
			t.Errorf("sha %q: error %q does not give the minimum length", sha, got)
		}
	}
}

func TestAnIncidentSummaryCountsEveryIncidentThatStartedInThePeriodIncludingThoseNoDeploymentCaused(t *testing.T) {
	var out struct {
		Groups []struct {
			Name               string
			Incidents          int
			Open               int
			CausedByDeployment int `json:"caused_by_deployment"`
		}
	}
	call(t, connect(t), "summarise_incidents",
		map[string]any{"from": "2026-03-02", "to": "2026-03-08", "group_by": "team"}, &out)

	if len(out.Groups) != 2 {
		t.Fatalf("groups = %+v, want Core and Front", out.Groups)
	}
	if core := out.Groups[0]; core.Name != "Core" || core.Incidents != 2 || core.CausedByDeployment != 2 {
		t.Errorf("Core = %+v, want 2 incidents, both caused by a deployment", core)
	}
	if front := out.Groups[1]; front.Name != "Front" || front.Incidents != 1 || front.Open != 1 || front.CausedByDeployment != 0 {
		t.Errorf("Front = %+v, want 1 open incident no deployment caused", front)
	}
}

func TestAListingLimitOutsideTheAllowedRangeIsRejected(t *testing.T) {
	got := callError(t, connect(t), "list_incidents",
		map[string]any{"from": "2026-03-01", "to": "2026-03-31", "limit": 1000})

	if !strings.Contains(got, "between 1 and 500") {
		t.Errorf("error = %q", got)
	}
}
