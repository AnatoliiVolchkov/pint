package main

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMetricsqlVersion(t *testing.T) {
	type testCaseT struct {
		info     *debug.BuildInfo
		expected string
		ok       bool
	}

	testCases := []testCaseT{
		{
			info:     nil,
			ok:       false,
			expected: "unknown",
		},
		{
			info:     &debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/foo/bar", Version: "v1.0.0"}}},
			ok:       true,
			expected: "unknown",
		},
		{
			info:     &debug.BuildInfo{Deps: []*debug.Module{{Path: metricsqlModule, Version: "v0.87.5"}}},
			ok:       true,
			expected: "v0.87.5",
		},
		{
			info: &debug.BuildInfo{Deps: []*debug.Module{{
				Path:    metricsqlModule,
				Version: "v0.87.5",
				Replace: &debug.Module{Path: "github.com/AnatoliiVolchkov/metricsql", Version: "v0.87.5-pint.3"},
			}}},
			ok:       true,
			expected: "v0.87.5-pint.3 (replaces VictoriaMetrics/metricsql v0.87.5)",
		},
		{
			info: &debug.BuildInfo{Deps: []*debug.Module{{
				Path:    metricsqlModule,
				Version: "v0.87.5",
				Replace: &debug.Module{Path: "../metricsql"},
			}}},
			ok:       true,
			expected: "../metricsql (replaces VictoriaMetrics/metricsql v0.87.5)",
		},
		{
			// go.work with a local metricsql checkout.
			info:     &debug.BuildInfo{Deps: []*debug.Module{{Path: metricsqlModule, Version: "(devel)"}}},
			ok:       true,
			expected: "(devel)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			require.Equal(t, tc.expected, metricsqlVersion(tc.info, tc.ok))
		})
	}
}
