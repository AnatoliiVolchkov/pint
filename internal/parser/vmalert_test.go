package parser_test

import (
	"strings"
	"testing"

	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/parser"
)

// firstParseError returns the first file, group or rule error and the number of parsed rules.
func firstParseError(f parser.File) (string, int) {
	var rules int
	if f.Error.Err != nil {
		return f.Error.Err.Error(), rules
	}
	for _, g := range f.Groups {
		if g.Error.Err != nil {
			return g.Error.Err.Error(), rules
		}
		for _, r := range g.Rules {
			if r.Error.Err != nil {
				return r.Error.Err.Error(), rules
			}
			rules++
		}
	}
	return "", rules
}

const vmalertGroupHeader = `
groups:
  - name: vm
    interval: 1m
`

const vmalertRule = `
    rules:
      - alert: Foo
        expr: up == 0
`

func TestVMAlertRuleFileSchema(t *testing.T) {
	type testCaseT struct {
		description string
		content     string
		err         string
		language    string
		relaxed     bool
	}

	testCases := []testCaseT{
		{
			description: "all vmalert group and rule fields",
			content: vmalertGroupHeader + `
    type: prometheus
    eval_offset: 10s
    eval_alignment: false
    concurrency: 2
    limit: 100
    debug: true
    params:
      nocache: ["1"]
      extra_label: ["env=prod", "team=sre"]
    headers:
      - "X-Team: sre"
      - ""
    notifier_headers:
      - "X-Env: prod"
    labels:
      env: prod
    rules:
      - alert: Foo
        expr: up == 0
        for: 5m
        keep_firing_for: 10m
        debug: true
        update_entries_limit: 10
        labels:
          severity: warning
      - record: foo:sum
        expr: sum(up)
        debug: false
`,
		},
		{
			description: "negative eval_offset",
			content:     vmalertGroupHeader + "    eval_offset: -30s\n" + vmalertRule,
		},
		{
			description: "eval_delay",
			content:     vmalertGroupHeader + "    eval_delay: 30s\n" + vmalertRule,
		},
		{
			description: "relaxed mode accepts vmalert rule fields",
			content:     vmalertGroupHeader + vmalertRule + "        debug: true\n        update_entries_limit: 5\n",
			relaxed:     true,
		},
		{
			description: "promql: vmalert group field is rejected",
			content:     vmalertGroupHeader + "    concurrency: 2\n" + vmalertRule,
			language:    "promql",
			err:         "invalid group key concurrency",
		},
		{
			description: "promql: vmalert rule field is rejected",
			content:     vmalertGroupHeader + vmalertRule + "        debug: true\n",
			language:    "promql",
			err:         "invalid rule key debug",
		},
		{
			description: "promql relaxed: vmalert rule field is rejected",
			content:     vmalertGroupHeader + vmalertRule + "        debug: true\n",
			language:    "promql",
			relaxed:     true,
			err:         "invalid key(s) found: debug",
		},
		{
			description: "query_offset is not supported by vmalert",
			content:     vmalertGroupHeader + "    query_offset: 1m\n" + vmalertRule,
			err:         "query_offset is not supported by vmalert, use eval_offset instead",
		},
		{
			description: "unknown group field",
			content:     vmalertGroupHeader + "    foo: bar\n" + vmalertRule,
			err:         "invalid group key foo",
		},
		{
			description: "unknown rule field",
			content:     vmalertGroupHeader + vmalertRule + "        foo: bar\n",
			err:         "invalid rule key foo",
		},
		{
			description: "graphite type",
			content:     vmalertGroupHeader + "    type: graphite\n" + vmalertRule,
			err:         "group type graphite is not supported, pint only checks MetricsQL rules that use type prometheus",
		},
		{
			description: "invalid type",
			content:     vmalertGroupHeader + "    type: influx\n" + vmalertRule,
			err:         `invalid group type "influx", valid values are prometheus, graphite and vlogs`,
		},
		{
			description: "eval_offset with eval_delay",
			content:     vmalertGroupHeader + "    eval_offset: 10s\n    eval_delay: 10s\n" + vmalertRule,
			err:         "eval_offset cannot be used with eval_delay",
		},
		{
			description: "eval_offset bigger than interval",
			content:     vmalertGroupHeader + "    eval_offset: -2m\n" + vmalertRule,
			err:         "the abs value of eval_offset should be smaller than interval; now eval_offset: 2m, interval: 1m",
		},
		{
			description: "invalid eval_offset",
			content:     vmalertGroupHeader + "    eval_offset: 10x\n" + vmalertRule,
			err:         `invalid eval_offset value: unknown unit "x" in duration "10x"`,
		},
		{
			description: "negative eval_delay",
			content:     vmalertGroupHeader + "    eval_delay: -10s\n" + vmalertRule,
			err:         `invalid eval_delay value: not a valid duration string: "-10s"`,
		},
		{
			description: "negative concurrency",
			content:     vmalertGroupHeader + "    concurrency: -1\n" + vmalertRule,
			err:         "invalid concurrency -1, shouldn't be less than 0",
		},
		{
			description: "concurrency is not an integer",
			content:     vmalertGroupHeader + "    concurrency: two\n" + vmalertRule,
			err:         "group concurrency must be a integer, got string",
		},
		{
			description: "eval_alignment is not a boolean",
			content:     vmalertGroupHeader + "    eval_alignment: maybe\n" + vmalertRule,
			err:         "group eval_alignment must be a boolean, got string",
		},
		{
			description: "params value is not a list",
			content:     vmalertGroupHeader + "    params:\n      nocache: 1\n" + vmalertRule,
			err:         "group params value for nocache must be a list of strings",
		},
		{
			description: "header without colon",
			content:     vmalertGroupHeader + "    headers:\n      - X-Team sre\n" + vmalertRule,
			err:         `missing ':' in headers entry "X-Team sre", expecting "key: value" format`,
		},
		{
			description: "notifier_headers is not a list",
			content:     vmalertGroupHeader + "    notifier_headers: \"X-Env: prod\"\n" + vmalertRule,
			err:         "group notifier_headers must be a list of strings",
		},
		{
			description: "rule debug is not a boolean",
			content:     vmalertGroupHeader + vmalertRule + "        debug: yes please\n",
			err:         "debug must be a boolean, got string",
		},
		{
			description: "negative update_entries_limit",
			content:     vmalertGroupHeader + vmalertRule + "        update_entries_limit: -5\n",
			err:         "invalid update_entries_limit -5, shouldn't be less than 0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			name := tc.language
			if name == "" {
				name = "metricsql"
			}
			lang, err := parser.NewLanguage(name)
			require.NoError(t, err)
			p := parser.NewParser(parser.Options{
				Language: lang,
				Names:    model.UTF8Validation,
				Schema:   parser.PrometheusSchema,
				IsStrict: !tc.relaxed,
			})
			f := p.Parse(strings.NewReader(tc.content))
			got, rules := firstParseError(f)
			require.Equal(t, tc.err, got)
			if tc.err == "" {
				require.Positive(t, rules, "no rules parsed")
			}
		})
	}
}
