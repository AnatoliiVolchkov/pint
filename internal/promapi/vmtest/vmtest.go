// Package vmtest provides real VictoriaMetrics deployments for integration tests.
//
// Start returns URLs of a single-node VictoriaMetrics and of a VictoriaMetrics cluster
// (vminsert, vmstorage and vmselect). They come from, in order:
//
//   - PINT_VM_URL, PINT_VM_CLUSTER_URL, PINT_VM_CLUSTER_INSERT_URL and optionally
//     PINT_VM_CLUSTER_STORAGE_URL (vmstorage HTTP root) if PINT_VM_URL is set;
//   - a Docker Compose project started from docker-compose.yml, shared by all tests of
//     the test binary and removed when the test binary exits.
//
// Tests are skipped when neither is available, unless PINT_VM_REQUIRED is set, in which
// case they fail.
package vmtest

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

//go:embed docker-compose.yml
var composeFile []byte

// Target is a VictoriaMetrics deployment.
type Target struct {
	// Name is "single" or "cluster".
	Name string
	// QueryURL is the URL pint is configured with.
	QueryURL string
	// InsertURL accepts /api/v1/import/prometheus requests.
	InsertURL string
	// FlushURL is the storage root serving /internal/force_flush, optional.
	FlushURL string
	// DeleteURL serves /api/v1/admin/tsdb/delete_series.
	DeleteURL string
}

// Env describes available VictoriaMetrics deployments.
type Env struct {
	Single  Target
	Cluster Target
	// RunID is unique for every test binary run, use it to keep test data apart.
	RunID string
}

// HasCluster returns true if a VictoriaMetrics cluster is available.
func (e *Env) HasCluster() bool {
	return e.Cluster.QueryURL != ""
}

// Targets returns all available deployments.
func (e *Env) Targets() []Target {
	if e.HasCluster() {
		return []Target{e.Single, e.Cluster}
	}
	return []Target{e.Single}
}

var (
	once     sync.Once
	env      *Env
	errStart error
	skipMsg  string
)

// Required returns true if integration tests must not be skipped.
func Required() bool {
	return os.Getenv("PINT_VM_REQUIRED") != ""
}

// Start returns available VictoriaMetrics deployments or skips the test.
func Start(t testing.TB) *Env {
	t.Helper()
	once.Do(start)
	if errStart != nil {
		t.Fatalf("cannot start VictoriaMetrics: %s", errStart)
	}
	if skipMsg != "" {
		if Required() {
			t.Fatalf("PINT_VM_REQUIRED is set but VictoriaMetrics is unavailable: %s", skipMsg)
		}
		t.Skip(skipMsg)
	}
	if Required() && !env.HasCluster() {
		t.Fatal("PINT_VM_REQUIRED is set but no VictoriaMetrics cluster is available")
	}
	return env
}

func start() {
	runID := fmt.Sprintf("r%d%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	if single := os.Getenv("PINT_VM_URL"); single != "" {
		clusterURL := trim(os.Getenv("PINT_VM_CLUSTER_URL"))
		env = &Env{
			Single: Target{Name: "single", QueryURL: trim(single), InsertURL: trim(single), FlushURL: trim(single), DeleteURL: trim(single)},
			Cluster: Target{
				Name:      "cluster",
				QueryURL:  clusterURL,
				InsertURL: trim(os.Getenv("PINT_VM_CLUSTER_INSERT_URL")),
				FlushURL:  trim(os.Getenv("PINT_VM_CLUSTER_STORAGE_URL")),
				DeleteURL: strings.Replace(clusterURL, "/select/", "/delete/", 1),
			},
			RunID: runID,
		}
		if (env.Cluster.QueryURL == "") != (env.Cluster.InsertURL == "") {
			errStart = errors.New("PINT_VM_CLUSTER_URL and PINT_VM_CLUSTER_INSERT_URL must be set together")
		}
		return
	}
	if err := dockerAvailable(); err != nil {
		skipMsg = "Docker is not available and PINT_VM_URL is not set: " + err.Error()
		return
	}
	env, errStart = startCompose(runID)
}

func trim(s string) string {
	return strings.TrimSuffix(s, "/")
}

func dockerAvailable() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		return fmt.Errorf("docker info: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

type compose struct {
	project string
	file    string
}

func (c compose) cmd(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "docker", append([]string{"compose", "-p", c.project, "-f", c.file}, args...)...)
}

func (c compose) run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := c.cmd(ctx, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker compose %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return string(bytes.TrimSpace(out)), nil
}

func (c compose) url(service, port, path string) (string, error) {
	addr, err := c.run("port", service, port)
	if err != nil {
		return "", err
	}
	return "http://" + addr + path, nil
}

func startCompose(runID string) (*Env, error) {
	dir, err := os.MkdirTemp("", "pint-vmtest-")
	if err != nil {
		return nil, err
	}
	c := compose{project: "pint-vmtest-" + runID, file: filepath.Join(dir, "docker-compose.yml")}
	if err = os.WriteFile(c.file, composeFile, 0o600); err != nil {
		return nil, err
	}
	if err = startReaper(c, dir); err != nil {
		return nil, err
	}
	if _, err = c.run("up", "-d", "--wait", "--wait-timeout", "180"); err != nil {
		logs, _ := c.run("logs", "--no-color", "--tail", "50")
		_, _ = c.run("down", "-v", "--remove-orphans")
		return nil, fmt.Errorf("%w\n%s", err, logs)
	}
	single, err := c.url("vmsingle", "8428", "")
	if err != nil {
		return nil, err
	}
	sel, err := c.url("vmselect", "8481", "/select/0/prometheus")
	if err != nil {
		return nil, err
	}
	ins, err := c.url("vminsert", "8480", "/insert/0/prometheus")
	if err != nil {
		return nil, err
	}
	storage, err := c.url("vmstorage", "8482", "")
	if err != nil {
		return nil, err
	}
	del, err := c.url("vmselect", "8481", "/delete/0/prometheus")
	if err != nil {
		return nil, err
	}
	return &Env{
		Single:  Target{Name: "single", QueryURL: single, InsertURL: single, FlushURL: single, DeleteURL: single},
		Cluster: Target{Name: "cluster", QueryURL: sel, InsertURL: ins, FlushURL: storage, DeleteURL: del},
		RunID:   runID,
	}, nil
}

// startReaper starts a detached process that removes the compose project once this
// test binary exits, including on panics and timeouts.
func startReaper(c compose, dir string) error {
	script := fmt.Sprintf(
		"while kill -0 %d 2>/dev/null; do sleep 1; done; docker compose -p %s -f %s down -v --remove-orphans >/dev/null 2>&1; rm -rf %s",
		os.Getpid(), c.project, c.file, dir,
	)
	// The reaper must outlive this process, so it's never cancelled.
	cmd := exec.CommandContext(context.Background(), "sh", "-c", script)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start compose reaper: %w", err)
	}
	return cmd.Process.Release()
}
