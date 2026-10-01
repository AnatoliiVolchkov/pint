package vmtest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Series is a synthetic time series written to VictoriaMetrics.
type Series struct {
	// Metric is the series in Prometheus text format without a value, e.g. `foo{job="a"}`.
	Metric string
	// Values from oldest to newest, one per step, the last one shortly before the import.
	// NaN skips a sample, which creates a gap.
	Values []float64
}

// Constant returns n samples with value v.
func Constant(n int, v float64) []float64 {
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = v
	}
	return vals
}

// Counter returns n samples of a counter starting at start and growing by inc per sample.
func Counter(n int, start, inc float64) []float64 {
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = start + float64(i)*inc
	}
	return vals
}

// Gap returns vals with samples in [from, to) removed.
func Gap(vals []float64, from, to int) []float64 {
	out := append([]float64(nil), vals...)
	for i := from; i < to && i < len(out); i++ {
		out[i] = math.NaN()
	}
	return out
}

func metricName(metric string) string {
	name, _, _ := strings.Cut(metric, "{")
	return name
}

// newestSampleAge keeps the newest imported sample older than -search.latencyOffset,
// so instant queries always see it.
const newestSampleAge = 5 * time.Second

// Import replaces all data of the imported metrics with series whose samples are step
// apart, the newest 5s ago, and waits until every series can be queried. Replacing keeps
// repeated test runs (go test -count=N) against the same VictoriaMetrics independent.
func (tg Target) Import(t testing.TB, step time.Duration, series ...Series) {
	t.Helper()
	tg.deleteMetrics(t, series)
	now := time.Now().Add(-newestSampleAge).Truncate(time.Second)
	var body strings.Builder
	expected := map[string]int{}
	var maxLen int
	for _, s := range series {
		if metricName(s.Metric) == "" {
			t.Fatalf("series %q has no metric name", s.Metric)
		}
		expected[metricName(s.Metric)]++
		maxLen = max(maxLen, len(s.Values))
		for i, v := range s.Values {
			if math.IsNaN(v) {
				continue
			}
			ts := now.Add(-time.Duration(len(s.Values)-1-i) * step)
			fmt.Fprintf(&body, "%s %s %d\n", s.Metric, strconv.FormatFloat(v, 'g', -1, 64), ts.UnixMilli())
		}
	}

	resp, err := post(tg.InsertURL+"/api/v1/import/prometheus", "text/plain", body.String())
	if err != nil {
		t.Fatalf("cannot import samples into %s: %s", tg.Name, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("importing samples into %s returned %s", tg.Name, resp.Status)
	}
	tg.flush()

	window := time.Duration(maxLen+1) * step
	names := make([]string, 0, len(expected))
	for name := range expected {
		names = append(names, name)
	}
	sort.Strings(names)
	deadline := time.Now().Add(time.Minute)
	for _, name := range names {
		q := fmt.Sprintf(`count(last_over_time({__name__=%q}[%ds]))`, name, int(window.Seconds())+1)
		for {
			res, err := tg.Query(q)
			if err == nil && len(res) == 1 && int(res[0].Value) == expected[name] {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("imported %s series are not queryable on %s: %v %v", name, tg.Name, res, err)
			}
			time.Sleep(250 * time.Millisecond)
			tg.flush()
		}
	}
}

// deleteMetrics removes existing series of every metric name in series.
func (tg Target) deleteMetrics(t testing.TB, series []Series) {
	t.Helper()
	if tg.DeleteURL == "" {
		return
	}
	args := url.Values{}
	seen := map[string]bool{}
	for _, s := range series {
		if name := metricName(s.Metric); !seen[name] {
			seen[name] = true
			args.Add("match[]", fmt.Sprintf(`{__name__=%q}`, name))
		}
	}
	if len(args) == 0 {
		return
	}
	resp, err := post(tg.DeleteURL+"/api/v1/admin/tsdb/delete_series", "application/x-www-form-urlencoded", args.Encode())
	if err != nil {
		t.Fatalf("cannot delete series on %s: %s", tg.Name, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("deleting series on %s returned %s", tg.Name, resp.Status)
	}
}

// flush makes recently ingested samples searchable immediately, vminsert may still
// buffer them for a moment, which Import polls for.
func (tg Target) flush() {
	if tg.FlushURL == "" {
		return
	}
	if resp, err := post(tg.FlushURL+"/internal/force_flush", "text/plain", ""); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

// client doesn't keep idle connections, so fixtures leave no goroutines behind.
var client = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

func post(uri, contentType, body string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri, strings.NewReader(body))
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	defer c.cancel()
	return c.ReadCloser.Close()
}

// Sample is a single instant query result.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Query runs an instant query directly against VictoriaMetrics.
func (tg Target) Query(query string) ([]Sample, error) {
	resp, err := post(tg.QueryURL+"/api/v1/query", "application/x-www-form-urlencoded", url.Values{"query": {query}}.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err = json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("cannot decode %s response: %w: %s", resp.Status, err, body)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("query %q failed: %s", query, r.Error)
	}
	if r.Data.ResultType != "vector" {
		return nil, fmt.Errorf("query %q returned %s, expected vector", query, r.Data.ResultType)
	}
	samples := make([]Sample, 0, len(r.Data.Result))
	for _, res := range r.Data.Result {
		s, _ := res.Value[1].(string)
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid sample value %q: %w", s, err)
		}
		samples = append(samples, Sample{Labels: res.Metric, Value: v})
	}
	return samples, nil
}

// MustQuery runs an instant query and fails the test on error.
func (tg Target) MustQuery(t testing.TB, query string) []Sample {
	t.Helper()
	res, err := tg.Query(query)
	if err != nil {
		t.Fatalf("%s: %s", tg.Name, err)
	}
	return res
}
