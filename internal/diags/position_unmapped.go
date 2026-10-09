package diags

import "go.yaml.in/yaml/v3"

// unmappedValuePosition is the position of a value none of whose characters were found in
// the source. NewPositionRange looks for the bytes of the decoded value in the file, which
// never matches a double-quoted YAML string starting with an escape like `ტ`: the
// decoded UTF-8 bytes aren't in the file. An empty position made the console reporter
// panic with `index out of range [0] with length 0` (upstream v0.89.1 too), so the value
// is placed on the line it starts on, from its first column to the end of that line.
func unmappedValuePosition(lines []string, val *yaml.Node) PositionRanges {
	last := val.Column
	if val.Line >= 1 && val.Line <= len(lines) {
		last = max(last, len(lines[val.Line-1]))
	}
	return PositionRanges{{Line: val.Line, FirstColumn: val.Column, LastColumn: last}}
}
