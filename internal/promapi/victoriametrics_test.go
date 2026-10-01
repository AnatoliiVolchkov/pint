package promapi_test

import (
	"net/http"
	"testing"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.nhat.io/httpmock"

	"github.com/cloudflare/pint/internal/promapi"
)

func newVictoriaMetricsGroup(t *testing.T, uri string, opts promapi.VictoriaMetricsOptions) *promapi.FailoverGroup {
	fg := promapi.NewFailoverGroup("vm", uri, []*promapi.Prometheus{
		promapi.NewVictoriaMetrics("vm", uri, "", nil, time.Second, 1, 100, nil, opts),
	}, true, "up", nil, nil, nil)
	reg := prometheus.NewRegistry()
	fg.StartWorkers(reg)
	t.Cleanup(func() { fg.Close(reg) })
	return fg
}

func TestVictoriaMetricsBackend(t *testing.T) {
	fg := newVictoriaMetricsGroup(t, "http://localhost", promapi.VictoriaMetricsOptions{})
	require.Equal(t, promapi.BackendVictoriaMetrics, fg.Backend())

	prom := promapi.NewPrometheus("prom", "http://localhost", "", nil, time.Second, 1, 100, nil)
	require.Equal(t, promapi.BackendPrometheus, prom.Backend())
}

func TestVictoriaMetricsFlags(t *testing.T) {
	type testCaseT struct {
		mock      httpmock.Mocker
		assertErr func(t *testing.T, err error)
		flags     v1.FlagsResult
		name      string
		prefix    string
	}

	testCases := []testCaseT{
		{
			name:   "single-node, retention in months",
			prefix: "",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").
					Return("-retentionPeriod=\"3\"\n-search.maxQueryDuration=\"30s\"\n").
					Once()
			}),
			flags: v1.FlagsResult{
				"retentionPeriod":             "3",
				"search.maxQueryDuration":     "30s",
				"storage.tsdb.retention.time": "93d",
			},
		},
		{
			name:   "single-node with /prometheus prefix, retention in days",
			prefix: "/prometheus",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").
					Return("-retentionPeriod=\"30d\"\n").
					Once()
			}),
			flags: v1.FlagsResult{
				"retentionPeriod":             "30d",
				"storage.tsdb.retention.time": "30d",
			},
		},
		{
			name:   "single-node, retention in years",
			prefix: "",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").
					Return("-retentionPeriod=\"1y\"\n").
					Once()
			}),
			flags: v1.FlagsResult{
				"retentionPeriod":             "1y",
				"storage.tsdb.retention.time": "1y",
			},
		},
		{
			name:   "cluster vmselect, default retention",
			prefix: "/select/0/prometheus",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").
					Return("-storageNode=\"vmstorage:8401\"\n").
					Once()
			}),
			flags: v1.FlagsResult{
				"storageNode":                 "vmstorage:8401",
				"storage.tsdb.retention.time": "31d",
			},
		},
		{
			name:   "cluster vmselect with project ID",
			prefix: "/select/1:2/prometheus/",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").
					Return("").
					Once()
			}),
			flags: v1.FlagsResult{
				"storage.tsdb.retention.time": "31d",
			},
		},
		{
			name:   "invalid retention",
			prefix: "",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").
					Return("-retentionPeriod=\"abc\"\n").
					Once()
			}),
			assertErr: func(t *testing.T, err error) {
				require.ErrorContains(t, err, `invalid -retentionPeriod flag value "abc"`)
			},
		},
		{
			name:   "invalid flags response",
			prefix: "",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").
					Return("<html></html>\n").
					Once()
			}),
			assertErr: func(t *testing.T, err error) {
				require.EqualError(t, err, `bad_response: invalid flag line: "<html></html>"`)
			},
		},
		{
			name:   "flags not found",
			prefix: "",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectGet("/flags").
					ReturnCode(http.StatusNotFound).
					Return("404 page not found\n").
					Once()
			}),
			assertErr: func(t *testing.T, err error) {
				require.ErrorIs(t, err, promapi.ErrUnsupported)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := tc.mock(t)
			fg := newVictoriaMetricsGroup(t, srv.URL()+tc.prefix, promapi.VictoriaMetricsOptions{})

			flags, err := fg.Flags(t.Context()).Wait()
			if tc.assertErr != nil {
				tc.assertErr(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.flags, flags.Flags)
		})
	}
}

func TestVictoriaMetricsConfig(t *testing.T) {
	// No API calls are expected, the config is synthesized from pint settings.
	srv := httpmock.New(func(_ *httpmock.Server) {})(t)

	t.Run("defaults", func(t *testing.T) {
		fg := newVictoriaMetricsGroup(t, srv.URL(), promapi.VictoriaMetricsOptions{})
		cfg, err := fg.Config(t.Context(), 0).Wait()
		require.NoError(t, err)
		require.Equal(t, srv.URL(), cfg.URI)
		require.Equal(t, promapi.PrometheusConfig{
			RuleFiles: nil,
			Global: promapi.ConfigSectionGlobal{
				ExternalLabels:     nil,
				ScrapeInterval:     time.Minute,
				ScrapeTimeout:      time.Second * 10,
				EvaluationInterval: time.Minute,
			},
		}, cfg.Config)
	})

	t.Run("overrides", func(t *testing.T) {
		fg := newVictoriaMetricsGroup(t, srv.URL()+"/select/0/prometheus", promapi.VictoriaMetricsOptions{
			ExternalLabels: map[string]string{"cluster": "a"},
			ScrapeInterval: time.Second * 15,
		})
		cfg, err := fg.Config(t.Context(), 0).Wait()
		require.NoError(t, err)
		require.Equal(t, map[string]string{"cluster": "a"}, cfg.Config.Global.ExternalLabels)
		require.Equal(t, time.Second*15, cfg.Config.Global.ScrapeInterval)
	})
}

func TestVictoriaMetricsQuery(t *testing.T) {
	type testCaseT struct {
		mock      httpmock.Mocker
		assertErr func(t *testing.T, err error)
		name      string
		prefix    string
		series    int
		stats     promapi.QueryStats
	}

	testCases := []testCaseT{
		{
			name:   "single-node stats",
			prefix: "",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectPost(promapi.APIPathQuery).
					ReturnHeader("Content-Type", "application/json").
					Return(`{"status":"success","isPartial":false,"data":{"resultType":"vector","result":[{"metric":{"instance":"a"},"value":[1700000000,"1"]}]},"stats":{"seriesFetched":"12","executionTimeMsec":250}}`).
					Once()
			}),
			series: 1,
			stats: promapi.QueryStats{
				Timings:       promapi.QueryTimings{EvalTotalTime: 0.25},
				SeriesFetched: 12,
			},
		},
		{
			name:   "cluster query path",
			prefix: "/select/0/prometheus",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectPost("/select/0/prometheus"+promapi.APIPathQuery).
					ReturnHeader("Content-Type", "application/json").
					Return(`{"status":"success","data":{"resultType":"vector","result":[]},"stats":{"seriesFetched":"0","executionTimeMsec":0}}`).
					Once()
			}),
		},
		{
			name:   "numeric errorType for bad query",
			prefix: "",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectPost(promapi.APIPathQuery).
					ReturnCode(http.StatusBadRequest).
					ReturnHeader("Content-Type", "application/json").
					Return(`{"status":"error","errorType":"400","error":"cannot parse query"}`).
					Once()
			}),
			assertErr: func(t *testing.T, err error) {
				require.EqualError(t, err, "bad_data: cannot parse query")
			},
		},
		{
			name:   "numeric errorType for query limit",
			prefix: "",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectPost(promapi.APIPathQuery).
					ReturnCode(http.StatusUnprocessableEntity).
					ReturnHeader("Content-Type", "application/json").
					Return(`{"status":"error","errorType":"422","error":"cannot select more than -search.maxSamplesPerQuery=100 samples"}`).
					Once()
			}),
			assertErr: func(t *testing.T, err error) {
				require.EqualError(t, err, "execution: cannot select more than -search.maxSamplesPerQuery=100 samples")
				require.True(t, promapi.IsQueryTooExpensive(err))
			},
		},
		{
			name:   "numeric errorType for server error",
			prefix: "",
			mock: httpmock.New(func(s *httpmock.Server) {
				s.ExpectPost(promapi.APIPathQuery).
					ReturnCode(http.StatusServiceUnavailable).
					ReturnHeader("Content-Type", "application/json").
					Return(`{"status":"error","errorType":"503","error":"all the vmstorage nodes are unavailable"}`).
					Once()
			}),
			assertErr: func(t *testing.T, err error) {
				require.EqualError(t, err, "server_error: all the vmstorage nodes are unavailable")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := tc.mock(t)
			fg := newVictoriaMetricsGroup(t, srv.URL()+tc.prefix, promapi.VictoriaMetricsOptions{})

			qr, err := fg.Query(t.Context(), "up").Wait()
			if tc.assertErr != nil {
				tc.assertErr(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, qr.Series, tc.series)
			require.Equal(t, tc.stats, qr.Stats)
		})
	}
}

func TestVictoriaMetricsMetadata(t *testing.T) {
	t.Run("placeholder response", func(t *testing.T) {
		srv := httpmock.New(func(s *httpmock.Server) {
			s.ExpectGet(promapi.APIPathMetadata+"?metric=foo").
				ReturnHeader("Content-Type", "application/json").
				Return(`{"status":"success","data":{}}`).
				Once()
		})(t)
		fg := newVictoriaMetricsGroup(t, srv.URL(), promapi.VictoriaMetricsOptions{})

		md, err := fg.Metadata(t.Context(), "foo").Wait()
		require.NoError(t, err)
		require.Empty(t, md.Metadata)
	})

	t.Run("unsupported path", func(t *testing.T) {
		srv := httpmock.New(func(s *httpmock.Server) {
			s.ExpectGet("/select/0/prometheus" + promapi.APIPathMetadata + "?metric=foo").
				ReturnCode(http.StatusBadRequest).
				Return("unsupported path requested: \"/select/0/prometheus/api/v1/metadata\"\n").
				Once()
		})(t)
		fg := newVictoriaMetricsGroup(t, srv.URL()+"/select/0/prometheus", promapi.VictoriaMetricsOptions{})

		_, err := fg.Metadata(t.Context(), "foo").Wait()
		require.ErrorIs(t, err, promapi.ErrUnsupported)

		// The API is disabled after the first unsupported response.
		_, err = fg.Metadata(t.Context(), "bar").Wait()
		require.ErrorIs(t, err, promapi.ErrUnsupported)
	})
}
