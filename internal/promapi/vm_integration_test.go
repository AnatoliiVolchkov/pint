package promapi_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cloudflare/pint/internal/promapi"
	"github.com/cloudflare/pint/internal/promapi/vmtest"
)

// Integration tests against real single-node and cluster VictoriaMetrics, see package vmtest.

func newVMIntegrationGroup(t *testing.T, uri string) *promapi.FailoverGroup {
	t.Helper()
	fg := promapi.NewFailoverGroup("vm", uri, []*promapi.Prometheus{
		promapi.NewVictoriaMetrics("vm", uri, "", nil, time.Second*30, 4, 100, nil, promapi.VictoriaMetricsOptions{
			ExternalLabels: map[string]string{"cluster": "test"},
			ScrapeInterval: time.Second * 15,
		}),
	}, true, "up", nil, nil, nil)
	reg := prometheus.NewRegistry()
	fg.StartWorkers(reg)
	t.Cleanup(func() { fg.Close(reg) })
	return fg
}

func TestVictoriaMetricsIntegration(t *testing.T) {
	env := vmtest.Start(t)
	for _, tg := range env.Targets() {
		t.Run(tg.Name, func(t *testing.T) {
			defer goleak.VerifyNone(t, append(vmtest.LeakOptions(), goleak.IgnoreCurrent())...)
			metric := fmt.Sprintf("pint_vm_%s_%s", tg.Name, env.RunID)
			tg.Import(t, 15*time.Second,
				vmtest.Series{Metric: metric + `{job="a"}`, Values: vmtest.Counter(40, 10000, 15)},
				vmtest.Series{Metric: metric + `{job="b"}`, Values: vmtest.Counter(40, 10000, 30)},
			)
			t.Run("api", func(t *testing.T) {
				testVMIntegration(t, newVMIntegrationGroup(t, tg.QueryURL), metric)
			})
		})
	}
}

func testVMIntegration(t *testing.T, fg *promapi.FailoverGroup, metric string) {
	t.Run("config", func(t *testing.T) {
		cfg, err := fg.Config(t.Context(), 0).Wait()
		require.NoError(t, err)
		require.Equal(t, time.Second*15, cfg.Config.Global.ScrapeInterval)
		require.Equal(t, map[string]string{"cluster": "test"}, cfg.Config.Global.ExternalLabels)
		require.Empty(t, cfg.Config.RuleFiles)
	})

	t.Run("flags", func(t *testing.T) {
		flags, err := fg.Flags(t.Context()).Wait()
		require.NoError(t, err)
		v, ok := flags.Flags["storage.tsdb.retention.time"]
		require.True(t, ok, "retention flag missing: %v", flags.Flags)
		d, err := model.ParseDuration(v)
		require.NoError(t, err)
		require.Positive(t, d)
	})

	t.Run("buildinfo", func(t *testing.T) {
		_, err := fg.BuildInfo(t.Context()).Wait()
		if err != nil {
			require.ErrorIs(t, err, promapi.ErrUnsupported)
		}
	})

	t.Run("metadata", func(t *testing.T) {
		_, err := fg.Metadata(t.Context(), metric).Wait()
		if err != nil {
			require.ErrorIs(t, err, promapi.ErrUnsupported)
		}
	})

	t.Run("MetricsQL query", func(t *testing.T) {
		qr, err := fg.Query(t.Context(), fmt.Sprintf(`WITH (r = rate(%s{job="a" or job="b"})) sum(r) by (job)`, metric)).Wait()
		require.NoError(t, err)
		require.Len(t, qr.Series, 2)
		require.Positive(t, qr.Stats.SeriesFetched)
		for _, s := range qr.Series {
			switch s.Labels.Get("job") {
			case "a":
				require.InDelta(t, 1, s.Value, 0.01)
			case "b":
				require.InDelta(t, 2, s.Value, 0.01)
			default:
				t.Fatalf("unexpected series: %s", s.Labels)
			}
		}
	})

	t.Run("query error", func(t *testing.T) {
		_, err := fg.Query(t.Context(), "sum(").Wait()
		require.Error(t, err)
		require.Regexp(t, "^(bad_data|execution): ", err.Error())
	})

	t.Run("range query", func(t *testing.T) {
		qr, err := fg.RangeQuery(t.Context(), metric, promapi.NewRelativeRange(time.Minute*5, time.Minute)).Wait()
		require.NoError(t, err)
		require.Len(t, qr.Series.Ranges, 2)
	})
}
