package promapi_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.nhat.io/httpmock"

	"github.com/cloudflare/pint/internal/promapi"
)

// Responses of vmalert v1.153.0 started with -datasource.queryStep=10m
// -external.label=cluster=demo -external.label=dc=dc-1 (Docker, 2026-10-09).
const (
	vmalertFlags = `-datasource.queryStep="10m0s"
-datasource.url="secret"
-external.label="cluster=demo,dc=dc-1"
-remoteWrite.url="secret"
-rule="/etc/rules/*.yaml,/etc/rules/b.yml"
`
	vmalertRules = `{"status":"success","data":{"groups":[` +
		`{"name":"g1","file":"/etc/rules/a.yaml","interval":60,"rules":[]},` +
		`{"name":"g2","file":"/etc/rules/a.yaml","interval":30,"params":["step=10m"],"rules":[]},` +
		`{"name":"g3","file":"/etc/rules/b.yml","interval":30,"rules":[]}]}}`
)

func TestVMAlert(t *testing.T) {
	type testCaseT struct {
		mock      httpmock.Mocker
		labels    map[string]string
		name      string
		err       string
		files     []string
		opts      promapi.VictoriaMetricsOptions
		queryStep time.Duration
		stepFrom  bool
	}

	testCases := []testCaseT{
		{
			name: "flags and rule files",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").Return(vmalertFlags).Once()
				s.ExpectGet("/api/v1/rules").Return(vmalertRules).Once()
			}),
			labels:    map[string]string{"cluster": "demo", "dc": "dc-1"},
			files:     []string{"/etc/rules/a.yaml", "/etc/rules/b.yml"},
			queryStep: time.Minute * 10,
			stepFrom:  true,
		},
		{
			name: "pint config takes precedence",
			opts: promapi.VictoriaMetricsOptions{
				ExternalLabels: map[string]string{"cluster": "dev", "team": "a"},
				QueryStep:      time.Minute,
			},
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").Return(vmalertFlags).Once()
				s.ExpectGet("/api/v1/rules").Return(vmalertRules).Once()
			}),
			labels:    map[string]string{"cluster": "dev", "dc": "dc-1", "team": "a"},
			files:     []string{"/etc/rules/a.yaml", "/etc/rules/b.yml"},
			queryStep: time.Minute,
		},
		{
			name: "defaults and quoted labels",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").Return(`-external.label="\"team=\\\"a,b\\\"\",env=prod"` + "\n").Once()
				s.ExpectGet("/api/v1/rules").Return(`{"status":"success","data":{"groups":[]}}`).Once()
			}),
			labels:    map[string]string{"team": `"a,b"`, "env": "prod"},
			files:     []string{},
			queryStep: promapi.VMAlertDefaultQueryStep,
		},
		{
			name: "flags error",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").ReturnCode(500).Return("fatal error").Once()
			}),
			err:       "vmalert at $URI: server_error: 500 Internal Server Error",
			queryStep: promapi.VMAlertDefaultQueryStep,
		},
		{
			name: "invalid query step",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").Return(`-datasource.queryStep="abc"` + "\n").Once()
			}),
			err:       `vmalert at $URI: bad_response: invalid vmalert -datasource.queryStep flag value "abc"`,
			queryStep: promapi.VMAlertDefaultQueryStep,
		},
		{
			name: "invalid external label",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").Return(`-external.label="cluster"` + "\n").Once()
			}),
			err:       `vmalert at $URI: bad_response: invalid vmalert -external.label flag item "cluster", expected name=value`,
			queryStep: promapi.VMAlertDefaultQueryStep,
		},
		{
			name: "rules error",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").Return(vmalertFlags).Once()
				s.ExpectGet("/api/v1/rules").Return(`{"status":"error","errorType":"execution","error":"boom"}`).Once()
			}),
			err:       "vmalert at $URI: execution: boom",
			queryStep: promapi.VMAlertDefaultQueryStep,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := tc.mock(t)
			opts := tc.opts
			opts.VMAlertURI = srv.URL() + "/"
			fg := newVictoriaMetricsGroup(t, "http://127.0.0.1:1", opts)
			require.Equal(t, srv.URL(), fg.VMAlertURI())

			err := fg.LoadVMAlert(t.Context())
			if tc.err != "" {
				require.EqualError(t, err, strings.ReplaceAll(tc.err, "$URI", srv.URL()))
			} else {
				require.NoError(t, err)
				labels, ok := fg.StaticExternalLabels()
				require.True(t, ok)
				require.Equal(t, tc.labels, labels)

				cfg, err := fg.Config(t.Context(), 0).Wait()
				require.NoError(t, err)
				require.Equal(t, tc.labels, cfg.Config.Global.ExternalLabels)
				require.Equal(t, tc.files, cfg.Config.RuleFiles)
			}

			step, ok := fg.VMAlertQueryStep()
			require.True(t, ok)
			require.Equal(t, tc.queryStep, step)
			if tc.stepFrom {
				require.Equal(t, srv.URL(), fg.VMAlertQueryStepFrom())
			} else {
				require.Empty(t, fg.VMAlertQueryStepFrom())
			}
		})
	}
}

func TestVMAlertNotConfigured(t *testing.T) {
	fg := newVictoriaMetricsGroup(t, "http://127.0.0.1:1", promapi.VictoriaMetricsOptions{})
	require.Empty(t, fg.VMAlertURI())
	require.NoError(t, fg.LoadVMAlert(t.Context()))
	require.Empty(t, fg.VMAlertQueryStepFrom())

	prom := promapi.NewFailoverGroup("prom", "http://127.0.0.1:1", []*promapi.Prometheus{
		promapi.NewPrometheus("prom", "http://127.0.0.1:1", "", nil, time.Second, 1, 100, 0, nil),
	}, true, "up", nil, nil, nil)
	require.Empty(t, prom.VMAlertURI())
	require.NoError(t, prom.LoadVMAlert(t.Context()))
}

// -rule.resultsLimit limits the number of results of every rule, a group's limit overrides it.
func TestVMAlertResultsLimit(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		srv := httpmock.New(func(s *httpmock.Server) {
			s.ExpectGet("/flags").Return(`-rule.resultsLimit="5"` + "\n").Once()
			s.ExpectGet("/api/v1/rules").Return(`{"status":"success","data":{"groups":[]}}`).Once()
		})(t)
		fg := newVictoriaMetricsGroup(t, "http://127.0.0.1:1", promapi.VictoriaMetricsOptions{VMAlertURI: srv.URL() + "/"})
		limit, uri := fg.VMAlertResultsLimit()
		require.Zero(t, limit, "not read before LoadVMAlert")
		require.Empty(t, uri)
		require.NoError(t, fg.LoadVMAlert(t.Context()))
		limit, uri = fg.VMAlertResultsLimit()
		require.Equal(t, 5, limit)
		require.Equal(t, srv.URL(), uri)
	})
	t.Run("invalid", func(t *testing.T) {
		srv := httpmock.New(func(s *httpmock.Server) {
			s.ExpectGet("/flags").Return(`-rule.resultsLimit="x"` + "\n").Once()
		})(t)
		fg := newVictoriaMetricsGroup(t, "http://127.0.0.1:1", promapi.VictoriaMetricsOptions{VMAlertURI: srv.URL() + "/"})
		require.EqualError(t, fg.LoadVMAlert(t.Context()), "vmalert at "+srv.URL()+`: bad_response: invalid vmalert -rule.resultsLimit flag value "x"`)
	})
}
