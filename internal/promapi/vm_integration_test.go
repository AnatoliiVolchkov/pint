package promapi_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/promapi"
)

// Integration tests against real VictoriaMetrics servers.
// Set PINT_VM_URL to the single-node URL (http://127.0.0.1:8428),
// PINT_VM_CLUSTER_URL to the vmselect URL (http://127.0.0.1:8481/select/0/prometheus)
// and PINT_VM_CLUSTER_INSERT_URL to the vminsert URL (http://127.0.0.1:8480/insert/0/prometheus).
// Tests are skipped if these are not set, unless PINT_VM_REQUIRED is set.

func vmIntegrationEnv(t *testing.T, names ...string) []string {
	t.Helper()
	values := make([]string, 0, len(names))
	for _, name := range names {
		v := os.Getenv(name)
		if v == "" {
			if os.Getenv("PINT_VM_REQUIRED") != "" {
				t.Fatalf("%s must be set when PINT_VM_REQUIRED is set", name)
			}
			t.Skipf("%s is not set, skipping VictoriaMetrics integration test", name)
		}
		values = append(values, strings.TrimSuffix(v, "/"))
	}
	return values
}

// vmImportSamples writes samples for the last 10 minutes and waits until they can be queried.
func vmImportSamples(t *testing.T, importURI, queryURI, metric string) {
	t.Helper()

	var body strings.Builder
	now := time.Now()
	for ts := now.Add(-10 * time.Minute); !ts.After(now); ts = ts.Add(15 * time.Second) {
		for i, job := range []string{"a", "b"} {
			fmt.Fprintf(&body, "%s{job=%q} %d %d\n", metric, job, int(ts.Sub(now).Seconds())*(i+1)+10000, ts.UnixMilli())
		}
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, importURI+"/api/v1/import/prometheus", strings.NewReader(body.String()))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	query := url.Values{"query": {fmt.Sprintf("count(%s) == 2", metric)}}.Encode()
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, queryURI+"/api/v1/query?"+query, nil)
		if err != nil {
			return false
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		return err == nil && resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"value":[`)
	}, time.Minute, time.Second, "imported samples are not queryable")
}

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

func TestVictoriaMetricsIntegrationSingleNode(t *testing.T) {
	env := vmIntegrationEnv(t, "PINT_VM_URL")
	metric := fmt.Sprintf("pint_vm_single_%d", time.Now().UnixNano())
	vmImportSamples(t, env[0], env[0], metric)
	testVMIntegration(t, newVMIntegrationGroup(t, env[0]), metric)
}

func TestVictoriaMetricsIntegrationCluster(t *testing.T) {
	env := vmIntegrationEnv(t, "PINT_VM_CLUSTER_URL", "PINT_VM_CLUSTER_INSERT_URL")
	metric := fmt.Sprintf("pint_vm_cluster_%d", time.Now().UnixNano())
	vmImportSamples(t, env[1], env[0], metric)
	testVMIntegration(t, newVMIntegrationGroup(t, env[0]), metric)
}
