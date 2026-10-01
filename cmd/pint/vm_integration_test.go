package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
)

// TestVictoriaMetricsScripts runs pint lint, ci and watch against real VictoriaMetrics servers.
// Set PINT_VM_URL to the single-node URL (http://127.0.0.1:8428) and optionally
// PINT_VM_CLUSTER_URL (http://127.0.0.1:8481/select/0/prometheus) together with
// PINT_VM_CLUSTER_INSERT_URL (http://127.0.0.1:8480/insert/0/prometheus) to enable [vmcluster] scripts.
// Tests are skipped if PINT_VM_URL is not set, unless PINT_VM_REQUIRED is set.
func TestVictoriaMetricsScripts(t *testing.T) {
	vmURL := strings.TrimSuffix(os.Getenv("PINT_VM_URL"), "/")
	clusterURL := strings.TrimSuffix(os.Getenv("PINT_VM_CLUSTER_URL"), "/")
	clusterInsertURL := strings.TrimSuffix(os.Getenv("PINT_VM_CLUSTER_INSERT_URL"), "/")
	if vmURL == "" {
		if os.Getenv("PINT_VM_REQUIRED") != "" {
			t.Fatal("PINT_VM_URL must be set when PINT_VM_REQUIRED is set")
		}
		t.Skip("PINT_VM_URL is not set, skipping VictoriaMetrics integration tests")
	}
	if (clusterURL == "") != (clusterInsertURL == "") {
		t.Fatal("PINT_VM_CLUSTER_URL and PINT_VM_CLUSTER_INSERT_URL must be set together")
	}
	if clusterURL == "" && os.Getenv("PINT_VM_REQUIRED") != "" {
		t.Fatal("PINT_VM_CLUSTER_URL must be set when PINT_VM_REQUIRED is set")
	}

	vmSeed(t, vmURL, vmURL)
	if clusterURL != "" {
		vmSeed(t, clusterInsertURL, clusterURL)
	}

	testscript.Run(t, testscript.Params{
		Dir: "tests_vm",
		Setup: func(env *testscript.Env) error {
			env.Setenv("PINT_VM_URL", vmURL)
			env.Setenv("PINT_VM_CLUSTER_URL", clusterURL)
			return nil
		},
		Condition: func(cond string) (bool, error) {
			if cond == "vmcluster" {
				return clusterURL != "", nil
			}
			return false, fmt.Errorf("unknown condition %q", cond)
		},
	})
}

// vmSeed imports the series used by tests_vm scripts and waits until they can be queried.
func vmSeed(t *testing.T, importURI, queryURI string) {
	t.Helper()

	var body strings.Builder
	now := time.Now()
	for ts := now.Add(-30 * time.Minute); !ts.After(now); ts = ts.Add(15 * time.Second) {
		offset := int(ts.Sub(now.Add(-30 * time.Minute)).Seconds())
		for _, instance := range []string{"a", "b"} {
			fmt.Fprintf(&body, "pint_vm_e2e_up{job=\"api\",instance=%q} 1 %d\n", instance, ts.UnixMilli())
			fmt.Fprintf(&body, "pint_vm_e2e_requests_total{job=\"api\",instance=%q} %d %d\n", instance, offset*10, ts.UnixMilli())
		}
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, importURI+"/api/v1/import/prometheus", strings.NewReader(body.String()))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("importing samples to %s returned %s", importURI, resp.Status)
	}

	query := url.Values{"query": {"count(pint_vm_e2e_up) == 2 and count(pint_vm_e2e_requests_total) == 2"}}.Encode()
	deadline := time.Now().Add(time.Minute)
	for {
		if vmHasResults(t, queryURI+"/api/v1/query?"+query) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("imported samples are not queryable on %s", queryURI)
		}
		time.Sleep(time.Second)
	}
}

func vmHasResults(t *testing.T, uri string) bool {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, uri, nil)
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
}
