package promapi

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

const (
	BackendPrometheus      = "prometheus"
	BackendVictoriaMetrics = "victoriametrics"

	// VictoriaMetrics serves flags as plain text at the root of the server.
	vmPathFlags = "/flags"
	// Flag with the retention period on VictoriaMetrics single-node and vmstorage.
	vmFlagRetentionPeriod = "retentionPeriod"
	// Prometheus flag used by pint checks.
	promFlagRetention = "storage.tsdb.retention.time"
	// Default -retentionPeriod value, one month.
	vmDefaultRetention = vmMonth
	// VictoriaMetrics uses 31 days for a month.
	vmMonth = time.Hour * 24 * 31
	vmYear  = time.Hour * 24 * 365
)

// Prometheus compatible API prefixes used by VictoriaMetrics:
// - /select/<tenant>/prometheus on vmselect (cluster).
// - /prometheus on single-node.
var vmAPIPrefix = regexp.MustCompile(`(/select/[^/]+)?/prometheus/?$`)

// VictoriaMetricsOptions are settings that VictoriaMetrics can't expose via API.
type VictoriaMetricsOptions struct {
	ExternalLabels map[string]string
	ScrapeInterval time.Duration
}

type victoriaMetrics struct {
	externalLabels map[string]string
	// rootURI is the URI of the VictoriaMetrics HTTP server, without any API prefix.
	rootURI        string
	scrapeInterval time.Duration
	cluster        bool
}

func NewVictoriaMetrics(name, uri, publicURI string, headers map[string]string, timeout time.Duration, concurrency, rl int, tlsConf *tls.Config, opts VictoriaMetricsOptions) *Prometheus {
	// VictoriaMetrics doesn't report how many samples a query read, sampleRateLimit can't work.
	prom := NewPrometheus(name, uri, publicURI, headers, timeout, concurrency, rl, 0, tlsConf)
	prom.backend = BackendVictoriaMetrics
	prom.vm = newVictoriaMetrics(strings.TrimSuffix(uri, "/"), opts)
	return prom
}

func newVictoriaMetrics(uri string, opts VictoriaMetricsOptions) *victoriaMetrics {
	vm := victoriaMetrics{
		externalLabels: opts.ExternalLabels,
		rootURI:        uri,
		scrapeInterval: opts.ScrapeInterval,
		cluster:        false,
	}
	if u, err := url.Parse(uri); err == nil {
		if m := vmAPIPrefix.FindStringSubmatch(u.Path); m != nil {
			vm.cluster = m[1] != ""
			u.Path = strings.TrimSuffix(u.Path, m[0])
			vm.rootURI = u.String()
		}
	}
	return &vm
}

// config returns Prometheus configuration for VictoriaMetrics,
// it doesn't expose one so we use values from pint config and Prometheus defaults.
func (vm *victoriaMetrics) config() PrometheusConfig {
	cfg := PrometheusConfig{
		RuleFiles: nil,
		Global: ConfigSectionGlobal{
			ExternalLabels:     vm.externalLabels,
			ScrapeInterval:     vm.scrapeInterval,
			ScrapeTimeout:      time.Second * 10,
			EvaluationInterval: time.Minute,
		},
	}
	if cfg.Global.ScrapeInterval == 0 {
		cfg.Global.ScrapeInterval = time.Minute
	}
	return cfg
}

// parseVictoriaMetricsFlags parses /flags response which uses `-name="value"` lines.
// It also sets Prometheus retention flag used by pint checks.
func parseVictoriaMetricsFlags(r io.Reader) (v1.FlagsResult, error) {
	defer dummyReadAll(r)

	flags := v1.FlagsResult{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(strings.TrimPrefix(line, "-"), "=")
		if !ok || !strings.HasPrefix(line, "-") {
			return nil, APIError{
				Status:    "error",
				ErrorType: v1.ErrBadResponse,
				Err:       fmt.Errorf("invalid flag line: %q", line),
			}
		}
		if uv, err := strconv.Unquote(value); err == nil {
			value = uv
		}
		flags[name] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, APIError{
			Status:    "error",
			ErrorType: v1.ErrBadResponse,
			Err:       fmt.Errorf("failed to read flags: %w", err),
		}
	}

	// vmselect doesn't know the retention of vmstorage nodes, assume the default.
	retention := vmDefaultRetention
	if v, ok := flags[vmFlagRetentionPeriod]; ok {
		r, err := parseVictoriaMetricsRetention(v)
		if err != nil {
			return nil, APIError{
				Status:    "error",
				ErrorType: v1.ErrBadResponse,
				Err:       fmt.Errorf("invalid -%s flag value %q: %w", vmFlagRetentionPeriod, v, err),
			}
		}
		retention = r
	}
	flags[promFlagRetention] = model.Duration(retention).String()

	return flags, nil
}

// parseVictoriaMetricsRetention parses -retentionPeriod value.
// Values without a suffix are in months, supported suffixes are s, m, h, d, w and y.
func parseVictoriaMetricsRetention(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty value")
	}

	unit := vmMonth
	num := s
	switch s[len(s)-1] {
	case 's':
		unit, num = time.Second, s[:len(s)-1]
	case 'm':
		unit, num = time.Minute, s[:len(s)-1]
	case 'h':
		unit, num = time.Hour, s[:len(s)-1]
	case 'd':
		unit, num = time.Hour*24, s[:len(s)-1]
	case 'w':
		unit, num = time.Hour*24*7, s[:len(s)-1]
	case 'y':
		unit, num = vmYear, s[:len(s)-1]
	case 'M':
		unit, num = vmMonth, s[:len(s)-1]
	}

	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, err
	}
	if v <= 0 {
		return 0, errors.New("retention must be positive")
	}
	return time.Duration(v * float64(unit)), nil
}

// vmQueryStats are query stats returned by VictoriaMetrics at the top level of the response.
type vmQueryStats struct {
	SeriesFetched     string  `json:"seriesFetched"`
	ExecutionTimeMsec float64 `json:"executionTimeMsec"`
}

func (s *vmQueryStats) apply(stats *QueryStats) {
	if s == nil {
		return
	}
	if n, err := strconv.Atoi(s.SeriesFetched); err == nil {
		stats.SeriesFetched = n
	}
	if stats.Timings.EvalTotalTime == 0 {
		stats.Timings.EvalTotalTime = s.ExecutionTimeMsec / 1000
	}
}
