package promapi

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
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
	// Flag only vmselect has, it lists vmstorage nodes.
	vmFlagStorageNode = "storageNode"
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
	// VMAlertURI is the URI of vmalert evaluating rules against this server, pint reads its
	// flags and loaded rule files.
	VMAlertURI     string
	ScrapeInterval time.Duration
	// Retention overrides -retentionPeriod, vmselect doesn't know it.
	Retention time.Duration
	// QueryStep is -datasource.queryStep of vmalert evaluating rules against this server.
	QueryStep time.Duration
}

type victoriaMetrics struct {
	externalLabels map[string]string
	// vmalert holds settings read from vmalertURI, nil until they are loaded.
	vmalert atomic.Pointer[VMAlertResult]
	// rootURI is the URI of the VictoriaMetrics HTTP server, without any API prefix.
	rootURI        string
	vmalertURI     string
	safeVMAlertURI string
	scrapeInterval time.Duration
	retention      time.Duration
	queryStep      time.Duration
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
		vmalert:        atomic.Pointer[VMAlertResult]{},
		rootURI:        uri,
		vmalertURI:     strings.TrimSuffix(opts.VMAlertURI, "/"),
		safeVMAlertURI: sanitizeURI(strings.TrimSuffix(opts.VMAlertURI, "/")),
		scrapeInterval: opts.ScrapeInterval,
		retention:      opts.Retention,
		queryStep:      opts.QueryStep,
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

// allExternalLabels returns external labels read from vmalert, if loaded, with labels set in
// pint config taking precedence.
func (vm *victoriaMetrics) allExternalLabels() map[string]string {
	va := vm.vmalert.Load()
	if va == nil || len(va.ExternalLabels) == 0 {
		return maps.Clone(vm.externalLabels)
	}
	labels := maps.Clone(va.ExternalLabels)
	maps.Copy(labels, vm.externalLabels)
	return labels
}

// config returns Prometheus configuration for VictoriaMetrics,
// it doesn't expose one so we use values from pint config, vmalert and Prometheus defaults.
func (vm *victoriaMetrics) config() PrometheusConfig {
	var ruleFiles []string
	if va := vm.vmalert.Load(); va != nil {
		ruleFiles = va.RuleFiles
	}
	cfg := PrometheusConfig{
		RuleFiles: ruleFiles,
		Global: ConfigSectionGlobal{
			ExternalLabels:     vm.allExternalLabels(),
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

// parseFlags parses /flags response which uses `-name="value"` lines.
// It also sets the Prometheus retention flag used by pint checks, unless the retention
// is unknown: vmselect doesn't know the retention of vmstorage nodes.
func (vm *victoriaMetrics) parseFlags(r io.Reader) (v1.FlagsResult, error) {
	flags, err := readVMFlags(r)
	if err != nil {
		return nil, err
	}
	return vm.withRetention(flags)
}

// readVMFlags reads `-name="value"` lines of the /flags endpoint of VictoriaMetrics components.
func readVMFlags(r io.Reader) (v1.FlagsResult, error) {
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
	return flags, nil
}

func (vm *victoriaMetrics) withRetention(flags v1.FlagsResult) (v1.FlagsResult, error) {
	retention := vm.retention
	if v, ok := flags[vmFlagRetentionPeriod]; ok && retention == 0 {
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
	if retention == 0 {
		if _, isSelect := flags[vmFlagStorageNode]; vm.cluster || isSelect {
			// vmselect doesn't know the retention of vmstorage nodes.
			return flags, nil
		}
		// Single-node VictoriaMetrics doesn't list flags left at their default.
		retention = vmDefaultRetention
	}
	flags[promFlagRetention] = model.Duration(retention).String()

	return flags, nil
}

// RetentionUnknown returns true for VictoriaMetrics servers that don't report
// their retention and don't have it configured, vmselect doesn't know it.
func RetentionUnknown(prom *FailoverGroup, flags map[string]string) bool {
	if prom.Backend() != BackendVictoriaMetrics {
		return false
	}
	_, ok := flags[promFlagRetention]
	return !ok
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

// VMAlertDefaultQueryStep is the default vmalert -datasource.queryStep, vmalert sends it as
// the step of every instant query and VictoriaMetrics uses it as the lookback window.
const VMAlertDefaultQueryStep = 5 * time.Minute

// VMAlertQueryStep returns the -datasource.queryStep of vmalert evaluating rules against
// VictoriaMetrics servers in this group. ok is false for Prometheus servers.
// VMAlertResultsLimit returns -rule.resultsLimit of vmalert read with vmalertURI, and the
// vmalert URI it comes from. It returns 0 when vmalert doesn't limit rule results or its
// settings weren't read.
func (fg *FailoverGroup) VMAlertResultsLimit() (int, string) {
	if len(fg.servers) == 0 || fg.servers[0].vm == nil {
		return 0, ""
	}
	if va := fg.servers[0].vm.vmalert.Load(); va != nil {
		return va.ResultsLimit, va.URI
	}
	return 0, ""
}

func (fg *FailoverGroup) VMAlertQueryStep() (step time.Duration, ok bool) {
	if len(fg.servers) == 0 || fg.servers[0].vm == nil {
		return 0, false
	}
	if step = fg.servers[0].vm.queryStep; step > 0 {
		return step, true
	}
	if va := fg.servers[0].vm.vmalert.Load(); va != nil && va.QueryStep > 0 {
		return va.QueryStep, true
	}
	return VMAlertDefaultQueryStep, true
}

// VMAlertQueryStepFrom returns the vmalert URI if VMAlertQueryStep was read from its flags.
func (fg *FailoverGroup) VMAlertQueryStepFrom() string {
	if len(fg.servers) == 0 || fg.servers[0].vm == nil || fg.servers[0].vm.queryStep > 0 {
		return ""
	}
	if va := fg.servers[0].vm.vmalert.Load(); va != nil && va.QueryStep > 0 {
		return va.URI
	}
	return ""
}
