package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/cloudflare/pint/internal/promapi/vmtest"
)

// TestVictoriaMetricsScripts runs pint lint, ci and watch from tests_vm against real
// single-node and cluster VictoriaMetrics, see package vmtest.
//
// Scripts get PINT_VM_URL and PINT_VM_CLUSTER_URL, scripts that need a cluster start with
// `[!vmcluster] skip`.
func TestVictoriaMetricsScripts(t *testing.T) {
	env := vmtest.Start(t)
	for _, tg := range env.Targets() {
		vmSeed(t, tg)
	}

	testscript.Run(t, testscript.Params{
		Dir: "tests_vm",
		Setup: func(e *testscript.Env) error {
			e.Setenv("PINT_VM_URL", env.Single.QueryURL)
			e.Setenv("PINT_VM_CLUSTER_URL", env.Cluster.QueryURL)
			return nil
		},
		Condition: func(cond string) (bool, error) {
			if cond == "vmcluster" {
				return env.HasCluster(), nil
			}
			return false, fmt.Errorf("unknown condition %q", cond)
		},
	})
}

// vmSeed imports the series used by tests_vm scripts: 30 minutes of data, 15s apart.
func vmSeed(t *testing.T, tg vmtest.Target) {
	t.Helper()
	const points = 121
	series := make([]vmtest.Series, 0, 4)
	for _, instance := range []string{"a", "b"} {
		series = append(series,
			vmtest.Series{Metric: fmt.Sprintf(`pint_vm_e2e_up{job="api",instance=%q}`, instance), Values: vmtest.Constant(points, 1)},
			vmtest.Series{Metric: fmt.Sprintf(`pint_vm_e2e_requests_total{job="api",instance=%q}`, instance), Values: vmtest.Counter(points, 0, 150)},
		)
	}
	tg.Import(t, 15*time.Second, series...)
}
