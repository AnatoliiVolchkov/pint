package checks

import (
	"fmt"

	"github.com/cloudflare/pint/internal/promapi"
)

// VictoriaMetrics has no configuration API. External labels of VictoriaMetrics servers come
// from pint config (externalLabels and --external-label) and must match vmalert -external.label
// flags, so problems point there instead of global:external_labels and the /config page.

const ExternalLabelsVMAlertDetails = "vmalert adds labels set with `-external.label` flags to all alerts and recording rule results, " +
	"see [vmalert docs](https://docs.victoriametrics.com/victoriametrics/vmalert/). " +
	"pint reads them from `externalLabels` of this server and from the `--external-label` flag, keep them the same as on vmalert."

func isVictoriaMetrics(prom *promapi.FailoverGroup) bool {
	return prom != nil && prom.Backend() == promapi.BackendVictoriaMetrics
}

// externalLabelsDetails returns problem details telling where external labels of the server are set.
func externalLabelsDetails(prom *promapi.FailoverGroup, uri string) string {
	if isVictoriaMetrics(prom) {
		return ExternalLabelsVMAlertDetails
	}
	return fmt.Sprintf("[Click here](%s/config) to see `%s` Prometheus runtime configuration.", uri, prom.Name())
}

func vmServerText(prom *promapi.FailoverGroup, uri string) string {
	return fmt.Sprintf("`%s` VictoriaMetrics server at %s", prom.Name(), uri)
}

// missingExternalLabelText describes an external label used in a template that isn't set on the server.
func missingExternalLabelText(prom *promapi.FailoverGroup, uri, name string) string {
	if isVictoriaMetrics(prom) && prom.VMAlertURI() != "" {
		return fmt.Sprintf("The template is using `%s` external label but it isn't set for %s, so vmalert renders it as an empty string. "+
			"vmalert at %s doesn't set it with `-external.label` and it isn't in `externalLabels` of this server.",
			name, vmServerText(prom, uri), prom.VMAlertURI())
	}
	if isVictoriaMetrics(prom) {
		return fmt.Sprintf("The template is using `%s` external label but it isn't set for %s, so vmalert renders it as an empty string. "+
			"If vmalert sets it with `-external.label`, add it to `externalLabels` of this server or pass it with `--external-label`.",
			name, vmServerText(prom, uri))
	}
	return fmt.Sprintf("The template is using `%s` external label but %s doesn't have this label configured in global:external_labels.", name, promText(prom.Name(), uri))
}

// vmalertLabelConflictText describes a rule label that has the same name as an external label of a
// VictoriaMetrics server. vmalert merges external labels into the labels of every alerting and
// recording rule, rule labels take priority (NewGroup in app/vmalert/rule/group.go).
func vmalertLabelConflictText(prom *promapi.FailoverGroup, uri, name, ruleValue, externalValue string) string {
	if ruleValue == externalValue {
		return fmt.Sprintf("This label is redundant. `%s=%q` is an external label of %s and vmalert adds it to all alerts and recording rule results, there's no need to set it manually.",
			name, externalValue, vmServerText(prom, uri))
	}
	return fmt.Sprintf("`%s=%q` is an external label of %s and vmalert adds it to all alerts and recording rule results, this rule overrides it with %q. "+
		"Please choose a different name for this label to avoid any conflicts.",
		name, externalValue, vmServerText(prom, uri), ruleValue)
}

// StaticOnVictoriaMetrics returns true for online checks that don't send any queries to
// VictoriaMetrics servers, they only use external labels from pint config, so they also
// work with --offline. Online they also use external labels read from vmalertURI.
func StaticOnVictoriaMetrics(name string) bool {
	return name == AlertsExternalLabelsCheckName || name == LabelsConflictCheckName
}
