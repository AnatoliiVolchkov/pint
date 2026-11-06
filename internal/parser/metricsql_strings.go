package parser

import (
	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/metricsql"
	"github.com/cloudflare/pint/internal/parser/source"
)

// VictoriaMetrics returns no series for a string literal: an instant query of `"down"`
// returned an empty vector on v1.155.0, and vmalert v1.155.0 loaded a rule with
// `expr: '"down"'` (also with -dryRun), the alert never fired and the recording rule wrote
// nothing (Docker, 2026-11-06). Source analysis treats a string as always returning a result,
// right for PromQL, where Prometheus refuses to load such a rule, so alerts/comparison called
// it an always firing alert. For MetricsQL it's dead code instead.

const metricsQLStringReason = "VictoriaMetrics returns no series for a string, " +
	"so this query never returns anything: an alerting rule never fires and a recording rule writes nothing."

// markMetricsQLStrings marks string sources of MetricsQL queries as never returning anything.
func markMetricsQLStrings(lang ast.Language, src []*source.Source) []*source.Source {
	if lang == nil || lang.Name() != metricsql.Name {
		return src
	}
	for _, s := range src {
		if s.Type == source.StringSource && s.DeadInfo == nil {
			s.ReturnInfo.AlwaysReturns = false
			s.DeadInfo = &source.DeadInfo{Reason: metricsQLStringReason, Fragment: s.Position}
		}
	}
	return src
}
