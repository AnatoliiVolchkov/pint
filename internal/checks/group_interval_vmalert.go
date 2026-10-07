package checks

import (
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/parser/metricsql"
)

const groupIntervalVMAlertMessage = "Using group interval > 5m will cause gaps in recording rule results, " +
	"VictoriaMetrics instant queries only look back as far as the query step, which is 5m by default."

func isVMAlertRule(entry *discovery.Entry) bool {
	expr := entry.Rule.Expr()
	return expr != nil && expr.Language().Name() == metricsql.Name
}

// skipGroupIntervalForVMAlert returns true for alerting rules evaluated by vmalert. vmalert sends
// firing alerts with EndsAt set to 4 x max(group interval, -rule.resendDelay) from now
// (getResolveDuration in app/vmalert/rule/group.go, v1.153.0) and resends them on every
// evaluation, so Alertmanager doesn't resolve them between evaluations and a long group
// interval doesn't cause flapping alerts. Recording rule results do get gaps.
func skipGroupIntervalForVMAlert(entry *discovery.Entry) bool {
	return entry.Rule.AlertingRule != nil && isVMAlertRule(entry)
}

func groupIntervalMessage(entry *discovery.Entry, upstream string) string {
	if isVMAlertRule(entry) {
		return groupIntervalVMAlertMessage
	}
	return upstream
}
