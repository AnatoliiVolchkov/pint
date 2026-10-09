package checks

import (
	"context"

	"github.com/cloudflare/pint/internal/promapi"
)

// queryForCost runs the query used to calculate the cost of a rule. VictoriaMetrics query
// stats don't include samples, so with maxTotalSamples set the query is traced and the
// number of samples comes from samplesScanned in the trace, see promapi.QueryWithTrace.
// It's the same single query, sent with trace=1 and nocache=1.
func (c CostCheck) queryForCost(ctx context.Context, expr string) *promapi.Request[*promapi.QueryResult] {
	if c.maxTotalSamples > 0 && isVictoriaMetrics(c.prom) {
		return c.prom.QueryWithTrace(ctx, expr)
	}
	return c.prom.Query(ctx, expr)
}
