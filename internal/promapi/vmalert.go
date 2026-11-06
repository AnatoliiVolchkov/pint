package promapi

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
)

// vmalert serves the flags it was started with at /flags, only flags set explicitly, as
// `-name="value"` lines, with secrets as "secret" and array flags joined with commas, items
// with commas quoted (v1.153.0 in Docker, 2026-10-09):
//
//	-datasource.queryStep="10m0s"
//	-external.label="cluster=demo,dc=dc-1"
//	-external.label="\"team=\\\"a,b\\\"\",env=prod"
//
// and the loaded groups at /api/v1/rules, each with the file it was read from:
//
//	{"status":"success","data":{"groups":[{"name":"g1","file":"/etc/rules/a.yaml",...}]}}

const (
	vmalertPathFlags         = "/flags"
	vmalertPathRules         = "/api/v1/rules"
	vmalertFlagQueryStep     = "datasource.queryStep"
	vmalertFlagExternalLabel = "external.label"
	vmalertFlagResultsLimit  = "rule.resultsLimit"
)

// VMAlertResult holds settings of vmalert evaluating rules against a VictoriaMetrics server.
type VMAlertResult struct {
	ExternalLabels map[string]string
	URI            string
	RuleFiles      []string
	// QueryStep is -datasource.queryStep, 0 if vmalert uses the default.
	QueryStep time.Duration
	// ResultsLimit is -rule.resultsLimit, 0 if vmalert doesn't limit rule results.
	ResultsLimit int
}

type vmalertQuery struct {
	prom     *Prometheus
	ctx      context.Context
	cacheTTL time.Duration
}

func (q vmalertQuery) Run() queryResult {
	slog.LogAttrs(q.ctx, slog.LevelDebug, "Getting vmalert flags and rules", slog.String("uri", q.prom.vm.safeVMAlertURI))

	ctx, cancel := q.prom.requestContext(q.ctx)
	defer cancel()

	var qr queryResult
	result := VMAlertResult{
		ExternalLabels: nil,
		URI:            q.prom.vm.safeVMAlertURI,
		RuleFiles:      nil,
		QueryStep:      0,
		ResultsLimit:   0,
	}

	flags, err := q.flags(ctx)
	if err != nil {
		qr.err = fmt.Errorf("failed to query vmalert flags: %w", err)
		return qr
	}
	if v, ok := flags[vmalertFlagQueryStep]; ok {
		d, perr := time.ParseDuration(v)
		if perr != nil || d <= 0 {
			qr.err = APIError{
				Status:    "error",
				ErrorType: v1.ErrBadResponse,
				Err:       fmt.Errorf("invalid vmalert -%s flag value %q", vmalertFlagQueryStep, v),
			}
			return qr
		}
		result.QueryStep = d
	}
	if v, ok := flags[vmalertFlagResultsLimit]; ok {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 {
			qr.err = APIError{
				Status:    "error",
				ErrorType: v1.ErrBadResponse,
				Err:       fmt.Errorf("invalid vmalert -%s flag value %q", vmalertFlagResultsLimit, v),
			}
			return qr
		}
		result.ResultsLimit = n
	}
	if v, ok := flags[vmalertFlagExternalLabel]; ok {
		if result.ExternalLabels, err = parseVMAlertExternalLabels(v); err != nil {
			qr.err = APIError{Status: "error", ErrorType: v1.ErrBadResponse, Err: err}
			return qr
		}
	}

	if result.RuleFiles, err = q.ruleFiles(ctx); err != nil {
		qr.err = fmt.Errorf("failed to query vmalert rules: %w", err)
		return qr
	}

	qr.value = result
	return qr
}

func (q vmalertQuery) flags(ctx context.Context) (v1.FlagsResult, error) {
	resp, err := q.prom.doRequestURI(ctx, http.MethodGet, q.prom.vm.vmalertURI, vmalertPathFlags, url.Values{})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, tryDecodingAPIError(resp)
	}
	return readVMFlags(resp.Body)
}

func (q vmalertQuery) ruleFiles(ctx context.Context) ([]string, error) {
	resp, err := q.prom.doRequestURI(ctx, http.MethodGet, q.prom.vm.vmalertURI, vmalertPathRules, url.Values{})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, tryDecodingAPIError(resp)
	}
	defer dummyReadAll(resp.Body)

	var data struct {
		Status    string `json:"status"`
		Error     string `json:"error"`
		ErrorType string `json:"errorType"`
		Data      struct {
			Groups []struct {
				File string `json:"file"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err = json.UnmarshalRead(resp.Body, &data); err != nil {
		return nil, APIError{Status: data.Status, ErrorType: v1.ErrBadResponse, Err: fmt.Errorf("JSON parse error: %w", err)}
	}
	if data.Status != "success" {
		if data.Error == "" {
			data.Error = "empty response object"
		}
		return nil, APIError{Status: data.Status, ErrorType: decodeErrorType(data.ErrorType), Err: errors.New(data.Error)}
	}
	files := make([]string, 0, len(data.Data.Groups))
	for _, g := range data.Data.Groups {
		if g.File != "" && !slices.Contains(files, g.File) {
			files = append(files, g.File)
		}
	}
	slices.Sort(files)
	return files, nil
}

func (q vmalertQuery) Endpoint() string {
	return vmalertPathFlags
}

func (q vmalertQuery) String() string {
	return "vmalert"
}

func (q vmalertQuery) CacheKey() uint64 {
	return hash(q.prom.vm.vmalertURI, "vmalert")
}

func (q vmalertQuery) CacheTTL() time.Duration {
	return q.cacheTTL
}

// parseVMAlertExternalLabels parses the -external.label flag value printed by vmalert /flags:
// `name=value` items joined with commas, items with commas quoted as Go strings.
func parseVMAlertExternalLabels(s string) (map[string]string, error) {
	labels := map[string]string{}
	for item := range splitVMArrayFlag(s) {
		if strings.HasPrefix(item, `"`) {
			uq, err := strconv.Unquote(item)
			if err != nil {
				return nil, fmt.Errorf("invalid vmalert -%s flag item %q: %w", vmalertFlagExternalLabel, item, err)
			}
			item = uq
		}
		name, value, ok := strings.Cut(item, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("invalid vmalert -%s flag item %q, expected name=value", vmalertFlagExternalLabel, item)
		}
		labels[name] = value
	}
	return labels, nil
}

// splitVMArrayFlag splits an array flag value on commas outside quoted items.
func splitVMArrayFlag(s string) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		var inQuote, escaped bool
		start := 0
		for i, c := range s {
			switch {
			case escaped:
				escaped = false
			case c == '\\' && inQuote:
				escaped = true
			case c == '"':
				inQuote = !inQuote
			case c == ',' && !inQuote:
				if !yield(s[start:i]) {
					return
				}
				start = i + 1
			}
		}
		if s != "" {
			yield(s[start:])
		}
	}
}

// VMAlert returns settings of vmalert evaluating rules against this server and keeps them,
// so checks use its query step and external labels. It returns nil without vmalertURI.
func (prom *Prometheus) VMAlert(ctx context.Context, cacheTTL time.Duration) (*VMAlertResult, error) {
	if prom.vm == nil || prom.vm.vmalertURI == "" {
		return nil, nil
	}
	slog.LogAttrs(ctx, slog.LevelDebug, "Scheduling vmalert query", slog.String("uri", prom.vm.safeVMAlertURI))

	key := "vmalert\n" + prom.vm.vmalertURI
	prom.locker.lock(key)
	defer prom.locker.unlock(key)

	if cacheTTL == 0 {
		cacheTTL = time.Minute * 10
	}
	result, err := prom.runQuery(ctx, vmalertQuery{prom: prom, ctx: ctx, cacheTTL: cacheTTL})
	if err != nil {
		return nil, QueryError{err: err, msg: "vmalert at " + prom.vm.safeVMAlertURI + ": " + decodeError(err)}
	}
	r := result.value.(VMAlertResult)
	prom.vm.vmalert.Store(&r)
	return &r, nil
}

// VMAlertURI returns the vmalert URI configured for VictoriaMetrics servers in this group.
func (fg *FailoverGroup) VMAlertURI() string {
	if len(fg.servers) == 0 || fg.servers[0].vm == nil {
		return ""
	}
	return fg.servers[0].vm.safeVMAlertURI
}

// LoadVMAlert reads settings of vmalert evaluating rules against servers in this group, if
// vmalertURI is set. All servers in the group share it.
func (fg *FailoverGroup) LoadVMAlert(ctx context.Context) error {
	if fg.VMAlertURI() == "" {
		return nil
	}
	va, err := fg.servers[0].VMAlert(ctx, 0)
	if err != nil {
		return &FailoverGroupError{err: err, uri: fg.servers[0].vm.safeVMAlertURI, isStrict: fg.strictErrors}
	}
	for _, prom := range fg.servers[1:] {
		prom.vm.vmalert.Store(va)
	}
	slog.LogAttrs(ctx, slog.LevelInfo, "Loaded vmalert settings",
		slog.String("name", fg.name),
		slog.String("uri", fg.servers[0].vm.safeVMAlertURI),
		slog.String("queryStep", va.QueryStep.String()),
		slog.Any("externalLabels", slices.Sorted(maps.Keys(va.ExternalLabels))),
		slog.Int("ruleFiles", len(va.RuleFiles)),
	)
	return nil
}
