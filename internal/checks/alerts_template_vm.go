package checks

import "regexp"

// Template errors start with the byte offset of the error in the template pint parses, like
// `304: executing "summary" at <...>`. pint prepends a header defining template variables,
// longer for vmalert, so the number counts the header too and points nowhere in the user's
// text. Upstream pint shows it for PromQL; for MetricsQL rules it's dropped, the diagnostic
// already points at the error.
var templateErrorOffsetRe = regexp.MustCompile(`^\d+: `)

// vmalertTemplateErrorMessage drops the internal offset from a normalized template error.
func vmalertTemplateErrorMessage(msg string) string {
	return templateErrorOffsetRe.ReplaceAllString(msg, "")
}
