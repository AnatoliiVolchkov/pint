package diags

// replacementsFit returns true if all replacements lie within an expression of given length.
// Positions of queries inside double-quoted YAML strings, like a rule file stored in a
// Kubernetes ConfigMap value, don't map to source lines, so AST nodes can point past the
// end of the line they are reported on.
func replacementsFit(reps []intRange, length int) bool {
	var last int
	for _, r := range reps {
		if r.start < last || r.end < r.start || r.end > length {
			return false
		}
		last = r.end
	}
	return true
}
