package promapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewVictoriaMetricsLayout(t *testing.T) {
	type testCaseT struct {
		uri     string
		root    string
		cluster bool
	}

	testCases := []testCaseT{
		{uri: "http://localhost:8428", root: "http://localhost:8428"},
		{uri: "http://localhost:8428/prometheus", root: "http://localhost:8428"},
		{uri: "http://localhost:8428/prometheus/", root: "http://localhost:8428"},
		{uri: "http://user:pass@localhost/vm/prometheus", root: "http://user:pass@localhost/vm"},
		{uri: "http://vmselect:8481/select/0/prometheus", root: "http://vmselect:8481", cluster: true},
		{uri: "http://vmselect:8481/select/1:2/prometheus", root: "http://vmselect:8481", cluster: true},
		{uri: "http://vmselect:8481/select/multitenant/prometheus", root: "http://vmselect:8481", cluster: true},
		{uri: "https://proxy/vmselect/select/0/prometheus", root: "https://proxy/vmselect", cluster: true},
		{uri: "http://localhost:8428/api", root: "http://localhost:8428/api"},
	}

	for _, tc := range testCases {
		t.Run(tc.uri, func(t *testing.T) {
			vm := newVictoriaMetrics(tc.uri, VictoriaMetricsOptions{})
			require.Equal(t, tc.root, vm.rootURI)
			require.Equal(t, tc.cluster, vm.cluster)
		})
	}
}

func TestParseVictoriaMetricsRetention(t *testing.T) {
	type testCaseT struct {
		input    string
		err      string
		expected time.Duration
	}

	day := time.Hour * 24
	testCases := []testCaseT{
		{input: "1", expected: day * 31},
		{input: "12", expected: day * 31 * 12},
		{input: "0.5", expected: day * 31 / 2},
		{input: "2M", expected: day * 62},
		{input: "100y", expected: day * 365 * 100},
		{input: "2w", expected: day * 14},
		{input: "30d", expected: day * 30},
		{input: "48h", expected: day * 2},
		{input: "90m", expected: time.Minute * 90},
		{input: "3600s", expected: time.Hour},
		{input: "", err: "empty value"},
		{input: "0", err: "retention must be positive"},
		{input: "-1d", err: "retention must be positive"},
		{input: "1x", err: `strconv.ParseFloat: parsing "1x": invalid syntax`},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			d, err := parseVictoriaMetricsRetention(tc.input)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expected, d)
		})
	}
}
