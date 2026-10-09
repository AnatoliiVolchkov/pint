package checks

import (
	"fmt"
	"strings"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
)

// VictoriaMetrics returns one metadata entry per metric name and the type can come from any
// target exposing that name, so it's less reliable than Prometheus metadata. prom.example.com
// returned `type: gauge` for `process_cpu_seconds_total` on 2026-10-05 and
// 2026-10-08, which made pint report rate() on it as a Bug. For VictoriaMetrics servers
// "non-counter" is a Warning, and an Information when the name ends with `_total`, which is
// reserved for counters, since the metadata itself is then the likely mistake.

const nonCounterVMDetails = "VictoriaMetrics returns one metadata entry per metric name, " +
	"and the type can come from any target exposing a metric with that name. " +
	"A wrong `# TYPE` line on one target is enough to make a counter look like a gauge."

// vmNonCounter adjusts a "counter based function called on a non-counter" problem for
// VictoriaMetrics servers.
func (c RateCheck) vmNonCounter(problem Problem, fn, metric string, mtype v1.MetricType, uri string) Problem {
	if !isVictoriaMetrics(c.prom) {
		return problem
	}
	problem.Severity = Warning
	problem.Details += "\n\n" + nonCounterVMDetails
	problem.Diagnostics[0].Message = fmt.Sprintf(
		"`%s()` should only be used with counters but `%s` is a %s according to metrics metadata from %s.",
		fn, metric, mtype, vmServerText(c.prom, uri),
	)
	if mtype == v1.MetricTypeGauge && strings.HasSuffix(metric, "_total") {
		problem.Summary = "metric metadata contradicts the metric name"
		problem.Severity = Information
		problem.Diagnostics[0].Message = fmt.Sprintf(
			"`%s()` is used on `%s`, which is a %s according to metrics metadata from %s, "+
				"but its name ends with `_total`, which is reserved for counters. "+
				"The metadata is likely wrong, check the `# TYPE` line of targets exposing `%s`.",
			fn, metric, mtype, vmServerText(c.prom, uri), metric,
		)
	}
	return problem
}
