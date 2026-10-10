package checks_test

import (
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/common/model"

	"github.com/cloudflare/pint/internal/promapi"
)

// vmIntervalsResponse answers max(scrape_interval(selector[window])) queries of promql/rate on
// VictoriaMetrics with the interval between samples, in seconds, set for the selector, and an
// empty result for other selectors.
type vmIntervalsResponse struct {
	intervals map[string]float64
}

func (vr vmIntervalsResponse) respond(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimPrefix(r.Form.Get("query"), "max(scrape_interval(")
	selector, _, _ := strings.Cut(query, "[")
	samples := model.Vector{}
	if v, ok := vr.intervals[selector]; ok {
		samples = append(samples, &model.Sample{Metric: model.Metric{}, Value: model.SampleValue(v), Timestamp: model.TimeFromUnix(time.Now().Unix())})
	}
	vectorResponse{samples: samples, stats: promapi.QueryStats{}}.respond(w, r)
}

type queryPrefixCond struct {
	prefix string
}

func (qc queryPrefixCond) isMatch(r *http.Request) bool {
	_ = r.ParseForm()
	return strings.HasPrefix(r.Form.Get("query"), qc.prefix)
}

// vmIntervals mocks the measurement of intervals between samples done by promql/rate.
func vmIntervals(intervals map[string]float64) *prometheusMock {
	return &prometheusMock{
		conds: []requestCondition{requireQueryPath, queryPrefixCond{prefix: "max(scrape_interval("}},
		resp:  vmIntervalsResponse{intervals: intervals},
	}
}
