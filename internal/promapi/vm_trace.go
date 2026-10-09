package promapi

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

// VictoriaMetrics query stats only have seriesFetched and executionTimeMsec, but its query
// trace (trace=1) reports samplesScanned for every rollup it evaluates: over raw samples
// ("samplesScanned=N"), over raw samples with incremental aggregation ("series after
// aggregation with sum(): 1; samplesScanned=N") and over subquery results ("rollup
// max_over_time() over 3 series returned by subquery: series=3, samplesScanned=N"), see
// app/vmselect/promql/eval.go in v1.153.0. These are the same numbers VictoriaMetrics adds to
// its vm_rows_scanned_per_query metric. Their sum is the number of samples the query scanned.
// vmstorage nodes of a cluster don't report it, so nothing is counted twice: single-node
// and cluster v1.153.0 returned the same sums. nocache=1 makes sure cached results don't
// hide samples.

var vmSamplesScanned = regexp.MustCompile(`samplesScanned=(\d+)`)

type vmTraceNode struct {
	Message  string        `json:"message"`
	Children []vmTraceNode `json:"children"`
}

func (n vmTraceNode) samplesScanned() (total int) {
	for _, m := range vmSamplesScanned.FindAllStringSubmatch(n.Message, -1) {
		if v, err := strconv.Atoi(m[1]); err == nil {
			total += v
		}
	}
	for _, c := range n.Children {
		total += c.samplesScanned()
	}
	return total
}

// tracedQuery is an instant query sent to VictoriaMetrics with trace=1, its stats include
// the number of samples the query scanned.
type tracedQuery struct {
	query instantQuery
}

func (q tracedQuery) Endpoint() string        { return q.query.Endpoint() }
func (q tracedQuery) String() string          { return q.query.String() }
func (q tracedQuery) CacheTTL() time.Duration { return q.query.CacheTTL() }

func (q tracedQuery) Run() queryResult {
	slog.LogAttrs(
		q.query.ctx, slog.LevelDebug,
		"Running prometheus query with trace",
		slog.String("uri", q.query.prom.safeURI),
		slog.String("query", q.query.expr),
	)

	ctx, cancel := q.query.prom.requestContext(q.query.ctx)
	defer cancel()

	var qr queryResult

	args := url.Values{}
	args.Set("query", q.query.expr)
	args.Set("timeout", q.query.prom.timeout.String())
	args.Set("stats", "1")
	args.Set("trace", "1")
	args.Set("nocache", "1")
	resp, err := q.query.prom.doRequest(ctx, http.MethodPost, q.Endpoint(), args)
	if err != nil {
		qr.err = err
		return qr
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		qr.err = tryDecodingAPIError(resp)
		return qr
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		qr.err = err
		return qr
	}
	qr.value, qr.stats, qr.err = parseVectorSamples(bytes.NewReader(body))
	if qr.err != nil {
		return qr
	}
	var traced struct {
		Trace *vmTraceNode `json:"trace"`
	}
	if err = json.Unmarshal(body, &traced); err == nil && traced.Trace != nil {
		qr.stats.Samples.TotalQueryableSamples = traced.Trace.samplesScanned()
	}
	return qr
}

func (q tracedQuery) CacheKey() uint64 {
	return hash(q.query.prom.unsafeURI, q.Endpoint(), q.query.expr, "trace")
}

// QueryWithTrace runs an instant query on VictoriaMetrics with tracing enabled, so
// Stats.Samples.TotalQueryableSamples is the number of samples the query scanned.
func (prom *Prometheus) QueryWithTrace(ctx context.Context, expr string) (*QueryResult, error) {
	slog.LogAttrs(ctx, slog.LevelDebug, "Scheduling prometheus query with trace", slog.String("uri", prom.safeURI), slog.String("query", expr))

	key := APIPathQuery + "trace" + expr
	prom.locker.lock(key)
	defer prom.locker.unlock(key)

	result, err := prom.runQuery(ctx, tracedQuery{
		query: instantQuery{
			prom:      prom,
			ctx:       ctx,
			expr:      expr,
			timestamp: time.Now(),
		},
	})
	if err != nil {
		return nil, QueryError{err: err, msg: decodeError(err)}
	}

	return &QueryResult{
		URI:    prom.publicURI,
		Series: result.value.([]Sample),
		Stats:  result.stats,
	}, nil
}

// QueryWithTrace is Query with the number of samples the query scanned, for
// VictoriaMetrics servers.
func (fg *FailoverGroup) QueryWithTrace(ctx context.Context, expr string) *Request[*QueryResult] {
	return newRequest(func() (*QueryResult, error) {
		var qr *QueryResult
		var uri string
		var err error
		for try, prom := range fg.servers {
			if try > 0 {
				slog.LogAttrs(ctx, slog.LevelDebug, "Using failover URI",
					slog.String("name", fg.name), slog.Int("retry", try), slog.String("uri", prom.safeURI))
			}
			uri = prom.safeURI
			qr, err = prom.QueryWithTrace(ctx, expr)
			if err == nil {
				return qr, nil
			}
			if !IsUnavailableError(err) {
				return qr, &FailoverGroupError{err: err, uri: uri, isStrict: fg.strictErrors}
			}
		}
		return nil, &FailoverGroupError{err: err, uri: uri, isStrict: fg.strictErrors}
	})
}
