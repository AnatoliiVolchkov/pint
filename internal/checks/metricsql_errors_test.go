package checks_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/promapi"
)

// Backend failures must be reported as "unable to run checks", never as a problem with
// the rule itself, and invalid MetricsQL must be reported by promql/syntax only.

type vmErrorScenario struct {
	handler func(w http.ResponseWriter, r *http.Request)
	name    string
	// message is a substring of the reported error.
	message string
	// closed makes the server refuse connections.
	closed bool
}

func vmErrorResponse(code int, contentType, body string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}
}

// vmJSONError returns an error the way VictoriaMetrics does, with the HTTP status code
// as errorType.
func vmJSONError(code int, msg string) func(w http.ResponseWriter, r *http.Request) {
	return vmErrorResponse(code, "application/json",
		`{"status":"error","errorType":"`+strconv.Itoa(code)+`","error":"`+msg+`"}`)
}

// vmFlagsErrorScenarios cover /flags, a plain text endpoint listing non-default flags.
var vmFlagsErrorScenarios = []vmErrorScenario{
	{name: "400 bad request", handler: vmErrorResponse(400, "text/plain", "bad request"), message: "400 Bad Request"},
	{name: "401 unauthorized", handler: vmErrorResponse(401, "text/plain", "Unauthorized"), message: "401 Unauthorized"},
	{name: "403 forbidden", handler: vmErrorResponse(403, "text/plain", "Forbidden"), message: "403 Forbidden"},
	{name: "429 too many requests", handler: vmErrorResponse(429, "text/plain", "too many requests"), message: "429 Too Many Requests"},
	{name: "500 server error", handler: vmErrorResponse(500, "text/plain", "internal error"), message: "500 Internal Server Error"},
	{name: "garbage", handler: vmErrorResponse(200, "text/plain", "not a flag\n"), message: "invalid flag line"},
	{name: "invalid retention", handler: vmErrorResponse(200, "text/plain", `-retentionPeriod="abc"`), message: "retentionPeriod"},
	{name: "timeout", handler: func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(200)
	}, message: "connection timeout"},
	{name: "connection refused", closed: true, message: "connection refused"},
}

var vmErrorScenarios = []vmErrorScenario{
	{name: "400 bad data", handler: vmJSONError(400, "cannot parse query"), message: "bad_data: cannot parse query"},
	{name: "401 unauthorized", handler: vmErrorResponse(401, "text/plain", "Unauthorized"), message: "401 Unauthorized"},
	{name: "403 forbidden", handler: vmErrorResponse(403, "text/plain", "Forbidden"), message: "403 Forbidden"},
	{name: "404 not found", handler: vmErrorResponse(404, "text/plain", "not found"), message: "404 Not Found"},
	{name: "422 execution error", handler: vmJSONError(422, "cannot execute query"), message: "execution: cannot execute query"},
	{name: "429 too many requests", handler: vmErrorResponse(429, "text/plain", "too many requests"), message: "429 Too Many Requests"},
	{name: "500 server error", handler: vmJSONError(500, "internal error"), message: "server_error: internal error"},
	{name: "503 unavailable", handler: vmErrorResponse(503, "text/plain", "service unavailable"), message: "503 Service Unavailable"},
	{name: "malformed JSON", handler: vmErrorResponse(200, "application/json", `{"status":"success","data":`), message: "JSON parse error"},
	{name: "empty response", handler: vmErrorResponse(200, "application/json", ``), message: "JSON parse error"},
	{name: "empty object", handler: vmErrorResponse(200, "application/json", `{}`), message: "empty response object"},
	{name: "partial response", handler: vmErrorResponse(200, "application/json",
		`{"status":"success","isPartial":true,"data":{"resultType":"vector","result":[]}}`), message: "partial response"},
	{name: "timeout", handler: func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(200)
	}, message: "connection timeout"},
	{name: "connection refused", closed: true, message: "connection refused"},
}

func vmErrorProm(uri string) *promapi.FailoverGroup {
	return promapi.NewFailoverGroup("vm", uri, []*promapi.Prometheus{
		promapi.NewVictoriaMetrics("vm", uri, "", nil, time.Second, 4, 1000, nil, promapi.VictoriaMetricsOptions{
			ExternalLabels: nil,
			ScrapeInterval: time.Minute,
		}),
	}, true, "up", nil, nil, nil)
}

func runVMErrorCheck(t *testing.T, uri string, newChecker func(*promapi.FailoverGroup) checks.RuleChecker, content string) []checks.Problem {
	t.Helper()
	prom := vmErrorProm(uri)
	reg := prometheus.NewRegistry()
	prom.StartWorkers(reg)
	defer prom.Close(reg)
	checker := newChecker(prom)
	opts := parser.DefaultOptions
	opts.Language = parser.MetricsQL
	entries, err := parseContent(content, opts, nil)
	require.NoError(t, err)
	problems := make([]checks.Problem, 0, len(entries))
	for _, e := range entries {
		e.State = discovery.Noop
		problems = append(problems, checker.Check(t.Context(), e, entries)...)
	}
	return problems
}

func TestMetricsQLBackendErrors(t *testing.T) {
	type checkCase struct {
		checker   func(*promapi.FailoverGroup) checks.RuleChecker
		name      string
		scenarios []vmErrorScenario
	}
	checkCases := []checkCase{
		{name: checks.SeriesCheckName, scenarios: vmErrorScenarios, checker: func(p *promapi.FailoverGroup) checks.RuleChecker { return checks.NewSeriesCheck(p) }},
		{name: checks.VectorMatchingCheckName, scenarios: vmErrorScenarios, checker: func(p *promapi.FailoverGroup) checks.RuleChecker {
			return checks.NewVectorMatchingCheck(p)
		}},
		{name: checks.CostCheckName, scenarios: vmErrorScenarios, checker: func(p *promapi.FailoverGroup) checks.RuleChecker {
			return checks.NewCostCheck(p, 1, 0, 0, 0, "", checks.Bug)
		}},
		{name: checks.RangeQueryCheckName, scenarios: vmFlagsErrorScenarios, checker: func(p *promapi.FailoverGroup) checks.RuleChecker {
			return checks.NewRangeQueryCheck(p, 0, "", checks.Warning)
		}},
		{name: checks.OffsetCheckName, scenarios: vmFlagsErrorScenarios, checker: func(p *promapi.FailoverGroup) checks.RuleChecker {
			return checks.NewOffsetCheck(p)
		}},
	}
	const content = "- record: x\n  expr: 'sum(rate(foo{job=\"a\" or job=\"b\"}[5m] offset 1h)) by (job) / on(job) bar'\n"

	for _, cc := range checkCases {
		for _, sc := range cc.scenarios {
			t.Run(cc.name+"/"+sc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case strings.HasPrefix(r.URL.Path, "/api/v1/query"), r.URL.Path == "/flags":
						sc.handler(w, r)
					default:
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte("unsupported path requested: " + r.URL.Path))
					}
				}))
				uri := srv.URL
				if sc.closed {
					srv.Close()
				} else {
					t.Cleanup(srv.Close)
				}

				problems := runVMErrorCheck(t, uri, cc.checker, content)
				require.NotEmpty(t, problems, "%s must report that it couldn't run", cc.name)
				for _, p := range problems {
					require.Equal(t, cc.name, p.Reporter)
					require.Equal(t, "unable to run checks", p.Summary, "backend failure reported as a rule problem: %v", p)
					require.Len(t, p.Diagnostics, 1)
					require.Contains(t, p.Diagnostics[0].Message, sc.message)
				}
			})
		}
	}
}

// VictoriaMetrics /flags only lists flags that differ from the defaults, an empty
// response means default retention and nothing to report.
func TestMetricsQLEmptyFlagsMeansDefaults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	newChecker := func(p *promapi.FailoverGroup) checks.RuleChecker { return checks.NewOffsetCheck(p) }
	require.Empty(t, runVMErrorCheck(t, srv.URL, newChecker, "- record: x\n  expr: 'foo offset 30d'\n"))
	problems := runVMErrorCheck(t, srv.URL, newChecker, "- record: x\n  expr: 'foo offset 40d'\n")
	require.Len(t, problems, 1)
	require.Contains(t, problems[0].Diagnostics[0].Message, "only keep 31d of metrics history")
}

func TestMetricsQLInvalidQueryIsNotSentToBackend(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	const content = "- record: x\n  expr: 'sum(rate(foo[5m])) by ('\n"
	for name, newChecker := range map[string]func(*promapi.FailoverGroup) checks.RuleChecker{
		checks.SeriesCheckName: func(p *promapi.FailoverGroup) checks.RuleChecker { return checks.NewSeriesCheck(p) },
		checks.RangeQueryCheckName: func(p *promapi.FailoverGroup) checks.RuleChecker {
			return checks.NewRangeQueryCheck(p, 0, "", checks.Warning)
		},
		checks.VectorMatchingCheckName: func(p *promapi.FailoverGroup) checks.RuleChecker { return checks.NewVectorMatchingCheck(p) },
		checks.CostCheckName: func(p *promapi.FailoverGroup) checks.RuleChecker {
			return checks.NewCostCheck(p, 1, 0, 0, 0, "", checks.Bug)
		},
	} {
		require.Empty(t, runVMErrorCheck(t, srv.URL, newChecker, content), "%s reported a problem for an invalid query", name)
	}
	require.Zero(t, requests.Load(), "invalid queries must not be sent to VictoriaMetrics")

	problems := runVMErrorCheck(t, srv.URL, func(*promapi.FailoverGroup) checks.RuleChecker { return checks.NewSyntaxCheck() }, content)
	require.Len(t, problems, 1)
	require.Equal(t, "MetricsQL syntax error", problems[0].Summary)
	require.Equal(t, checks.Fatal, problems[0].Severity)
}
