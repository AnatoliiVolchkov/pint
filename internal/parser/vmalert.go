package parser

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/common/model"
	"go.yaml.in/yaml/v3"

	"github.com/cloudflare/pint/internal/parser/metricsql"
)

// vmalert rule file fields that are not part of the Prometheus rule file schema, see
// https://docs.victoriametrics.com/victoriametrics/vmalert/#groups and Group/Rule types in
// https://github.com/VictoriaMetrics/VictoriaMetrics/blob/master/app/vmalert/config/config.go
const (
	vmalertTypeKey            = "type"
	vmalertEvalOffsetKey      = "eval_offset"
	vmalertEvalDelayKey       = "eval_delay"
	vmalertEvalAlignmentKey   = "eval_alignment"
	vmalertConcurrencyKey     = "concurrency"
	vmalertParamsKey          = "params"
	vmalertHeadersKey         = "headers"
	vmalertNotifierHeadersKey = "notifier_headers"
	vmalertDebugKey           = "debug"
	vmalertUpdateEntriesKey   = "update_entries_limit"

	// vmalert group datasource type for Prometheus compatible datasources, the only one using MetricsQL.
	vmalertPrometheusType = "prometheus"

	boolTag = "!!bool"
)

// isVMAlert returns true if rules are evaluated by vmalert, which is the case
// for all rules using MetricsQL.
func (p Parser) isVMAlert() bool {
	return p.opts.Language != nil && p.opts.Language.Name() == metricsql.Name
}

// isVMAlertRuleKey returns true for vmalert-only rule fields.
func (p Parser) isVMAlertRuleKey(key string) bool {
	if !p.isVMAlert() {
		return false
	}
	switch key {
	case vmalertDebugKey, vmalertUpdateEntriesKey:
		return true
	default:
		return false
	}
}

// vmalertGroup holds vmalert-only group fields needed to validate the group as a whole.
type vmalertGroup struct {
	evalOffset *time.Duration
	evalDelay  *time.Duration
	offsetLine int
}

// parseVMAlertGroupKey validates a vmalert-only group field. It returns false if key isn't one.
func (p Parser) parseVMAlertGroupKey(entry yamlMap, vg *vmalertGroup) (bool, ParseError) {
	if !p.isVMAlert() {
		return false, ParseError{}
	}

	key := entry.key.Value
	val := resolveNode(entry.val)
	line := entry.key.Line
	fail := func(format string, args ...any) (bool, ParseError) {
		return true, ParseError{Line: line, Err: fmt.Errorf(format, args...)}
	}

	switch key {
	case vmalertTypeKey:
		if val.Kind != yaml.ScalarNode || val.ShortTag() != strTag {
			return fail("group %s must be a %s, got %s", key, describeTag(strTag), describeTag(val.ShortTag()))
		}
		switch t := val.Value; t {
		case vmalertPrometheusType:
		case "graphite", "vlogs":
			return fail("group type %s is not supported, pint only checks MetricsQL rules that use type %s", t, vmalertPrometheusType)
		default:
			return fail("invalid group type %q, valid values are prometheus, graphite and vlogs", t)
		}
	case vmalertEvalOffsetKey, vmalertEvalDelayKey:
		if val.Kind != yaml.ScalarNode || val.ShortTag() != strTag {
			return fail("group %s must be a %s, got %s", key, describeTag(strTag), describeTag(val.ShortTag()))
		}
		d, err := parseSignedDuration(val.Value, key == vmalertEvalOffsetKey)
		if err != nil {
			return fail("invalid %s value: %w", key, err)
		}
		if key == vmalertEvalOffsetKey {
			vg.evalOffset = &d
			vg.offsetLine = line
		} else {
			vg.evalDelay = &d
		}
	case vmalertEvalAlignmentKey, vmalertDebugKey:
		if val.Kind != yaml.ScalarNode || val.ShortTag() != boolTag {
			return fail("group %s must be a boolean, got %s", key, describeTag(val.ShortTag()))
		}
	case vmalertConcurrencyKey:
		if val.Kind != yaml.ScalarNode || val.ShortTag() != intTag {
			return fail("group %s must be a %s, got %s", key, describeTag(intTag), describeTag(val.ShortTag()))
		}
		if n, err := strconv.Atoi(val.Value); err != nil || n < 0 {
			return fail("invalid %s %s, shouldn't be less than 0", key, val.Value)
		}
	case vmalertParamsKey:
		if val.ShortTag() != mapTag {
			return fail("group %s must be a %s, got %s", key, describeTag(mapTag), describeTag(val.ShortTag()))
		}
		for _, param := range mappingNodes(val) {
			if !isStringList(param.val) {
				return true, ParseError{
					Line: param.key.Line,
					Err:  fmt.Errorf("group %s value for %s must be a list of strings", key, param.key.Value),
				}
			}
		}
	case vmalertHeadersKey, vmalertNotifierHeadersKey:
		if !isStringList(val) {
			return fail("group %s must be a list of strings", key)
		}
		for _, h := range val.Content {
			// Same rules as vmalert: empty entries are ignored, others need a colon.
			if v := resolveNode(h).Value; v != "" && !strings.Contains(v, ":") {
				return true, ParseError{
					Line: h.Line,
					Err:  fmt.Errorf("missing ':' in %s entry %q, expecting \"key: value\" format", key, v),
				}
			}
		}
	default:
		return false, ParseError{}
	}
	return true, ParseError{}
}

// validateVMAlertGroup checks rules vmalert applies to the group as a whole.
func validateVMAlertGroup(group Group, vg vmalertGroup) ParseError {
	if vg.evalOffset != nil && vg.evalDelay != nil {
		return ParseError{Line: vg.offsetLine, Err: errors.New("eval_offset cannot be used with eval_delay")}
	}
	if vg.evalOffset != nil && group.Interval != nil && vg.evalOffset.Abs() > group.Interval.Value {
		return ParseError{
			Line: vg.offsetLine,
			Err: fmt.Errorf("the abs value of eval_offset should be smaller than interval; now eval_offset: %s, interval: %s",
				model.Duration(vg.evalOffset.Abs()), model.Duration(group.Interval.Value)),
		}
	}
	return ParseError{}
}

// validateVMAlertRuleKey checks the value of a vmalert-only rule field in strict mode.
func validateVMAlertRuleKey(key, val *yaml.Node) ParseError {
	v := resolveNode(val)
	switch key.Value {
	case vmalertDebugKey:
		if v.Kind != yaml.ScalarNode || v.ShortTag() != boolTag {
			return ParseError{Line: key.Line, Err: fmt.Errorf("%s must be a boolean, got %s", key.Value, describeTag(v.ShortTag()))}
		}
	case vmalertUpdateEntriesKey:
		if v.Kind != yaml.ScalarNode || v.ShortTag() != intTag {
			return ParseError{Line: key.Line, Err: fmt.Errorf("%s must be a %s, got %s", key.Value, describeTag(intTag), describeTag(v.ShortTag()))}
		}
		if n, err := strconv.Atoi(v.Value); err != nil || n < 0 {
			return ParseError{Line: key.Line, Err: fmt.Errorf("invalid %s %s, shouldn't be less than 0", key.Value, v.Value)}
		}
	}
	return ParseError{}
}

func isStringList(node *yaml.Node) bool {
	n := resolveNode(node)
	if n.Kind != yaml.SequenceNode {
		return false
	}
	for _, item := range n.Content {
		item = resolveNode(item)
		if item.Kind != yaml.ScalarNode {
			return false
		}
	}
	return true
}

func parseSignedDuration(s string, allowNegative bool) (time.Duration, error) {
	neg := false
	if allowNegative && strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	d, err := model.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if neg {
		return -time.Duration(d), nil
	}
	return time.Duration(d), nil
}

// VMAlertGroup holds vmalert group fields that change how vmalert queries data and
// timestamps rule results, see Group.Start and adjustReqTimestamp in
// app/vmalert/rule/group.go.
type VMAlertGroup struct {
	// Params are extra query arguments vmalert sends with every query of the group.
	// They replace arguments vmalert sets itself, like step.
	Params map[string][]string
	// EvalOffset is set when the group is evaluated at a fixed offset within its interval.
	EvalOffset *time.Duration
	// EvalAlignment is nil when eval_alignment isn't set, it's enabled by default.
	EvalAlignment *bool
	// Debug is the `debug` value when it's set to true.
	Debug *YamlNode
}

// VMAlertRule holds vmalert-only rule fields used by checks.
type VMAlertRule struct {
	// Debug is the `debug` value when it's set to true.
	Debug *YamlNode
}

// vmalertDebug returns the value node of a `debug: true` field, nil for other values.
func vmalertDebug(val *yaml.Node, offsetLine, offsetColumn int, contentLines []string) *YamlNode {
	if b, err := strconv.ParseBool(val.Value); err != nil || !b || val.ShortTag() != boolTag {
		return nil
	}
	return newYamlNode(val, offsetLine, offsetColumn, contentLines, val.Column)
}

// Param returns the first value of a group query argument, vmalert sends all of them
// and VictoriaMetrics uses the first one.
func (vg *VMAlertGroup) Param(name string) (string, bool) {
	if vg == nil || len(vg.Params[name]) == 0 {
		return "", false
	}
	return vg.Params[name][0], true
}

// vmalertGroupSettings returns vmalert group fields used by checks, or nil if there are
// none. Invalid values are skipped, strict mode reports them.
func vmalertGroupSettings(node *yaml.Node, offsetLine, offsetColumn int, contentLines []string) *VMAlertGroup {
	var vg VMAlertGroup
	var found bool
	for _, entry := range mappingNodes(node) {
		val := resolveNode(entry.val)
		switch nodeValue(entry.key) {
		case vmalertParamsKey:
			if val.Kind != yaml.MappingNode {
				continue
			}
			for _, param := range mappingNodes(val) {
				if !isStringList(param.val) {
					continue
				}
				if vg.Params == nil {
					vg.Params = map[string][]string{}
				}
				for _, v := range resolveNode(param.val).Content {
					vg.Params[nodeValue(param.key)] = append(vg.Params[nodeValue(param.key)], resolveNode(v).Value)
				}
				found = true
			}
		case vmalertEvalOffsetKey:
			if d, err := parseSignedDuration(val.Value, true); err == nil {
				vg.EvalOffset = &d
				found = true
			}
		case vmalertEvalAlignmentKey:
			if b, err := strconv.ParseBool(val.Value); err == nil && val.ShortTag() == boolTag {
				vg.EvalAlignment = &b
				found = true
			}
		case vmalertDebugKey:
			if vg.Debug = vmalertDebug(val, offsetLine, offsetColumn, contentLines); vg.Debug != nil {
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	return &vg
}
