package metricsql_test

import (
	"bufio"
	"context"
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/txtar"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// Corpus files for the parser differential test, one Go-quoted query per line.
// Regenerate with (needs network access for the MetricsQL documentation):
//
//	UPDATE_METRICSQL_CORPUS=1 go test ./internal/parser/metricsql -run TestGenerateCorpus
const corpusDir = "testdata/corpus"

// metricsqlDocsCommit pins the VictoriaMetrics documentation the docs corpus is taken from.
const metricsqlDocsCommit = "69633486310e"

func TestGenerateCorpus(t *testing.T) {
	if os.Getenv("UPDATE_METRICSQL_CORPUS") == "" {
		t.Skip("set UPDATE_METRICSQL_CORPUS=1 to regenerate the corpus")
	}
	mqlDir := moduleDir(t, "github.com/VictoriaMetrics/metricsql")
	promDir := moduleDir(t, "github.com/prometheus/prometheus")

	writeCorpus(t, "upstream_parser_success.txt", "VictoriaMetrics/metricsql parser_test.go TestParseSuccess",
		goStringArgs(t, filepath.Join(mqlDir, "parser_test.go"), "TestParseSuccess", "same", "another"))
	writeCorpus(t, "upstream_parser_error.txt", "VictoriaMetrics/metricsql parser_test.go TestParseError",
		goStringArgs(t, filepath.Join(mqlDir, "parser_test.go"), "TestParseError", "f"))
	writeCorpus(t, "prometheus_parser.txt", "prometheus/prometheus promql/parser/parse_test.go inputs",
		prometheusInputs(t, filepath.Join(promDir, "promql", "parser", "parse_test.go")))
	writeCorpus(t, "pint_testscripts.txt", "pint cmd/pint/tests/*.txt rule expressions",
		testscriptExprs(t, "../../../cmd/pint/tests"))
	writeCorpus(t, "metricsql_docs.txt", "VictoriaMetrics docs/victoriametrics/MetricsQL.md at "+metricsqlDocsCommit,
		docsExamples(t))

	all := make([]string, 0, 2048)
	for _, name := range corpusFiles {
		all = append(all, readCorpus(t, name)...)
	}
	writeCorpus(t, "mutations.txt", "deterministic malformed mutations of all other corpus files", mutations(all))
	all = append(all, readCorpus(t, "mutations.txt")...)
	writeVerdicts(t, all)
}

// corpusFiles are the corpus files written by hand or generated from upstream sources.
var corpusFiles = []string{
	"upstream_parser_success.txt",
	"upstream_parser_error.txt",
	"prometheus_parser.txt",
	"pint_testscripts.txt",
	"metricsql_docs.txt",
	"metricsql_only.txt",
}

// mutations returns malformed variants of queries: truncated in the middle, with the
// last closing bracket removed and with a dangling binary operator.
func mutations(queries []string) []string {
	var out []string
	for _, q := range queries {
		if len(q) > 3 {
			out = append(out, q[:len(q)/2])
		}
		for _, br := range []string{")", "}", "]"} {
			if i := strings.LastIndex(q, br); i >= 0 {
				out = append(out, q[:i]+q[i+1:])
				break
			}
		}
		out = append(out, q+" +")
	}
	return out
}

// upstreamVerdictProgram prints A or R for every quoted query read from stdin, parsed
// by the unmodified upstream metricsql module.
const upstreamVerdictProgram = `package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"

	"github.com/VictoriaMetrics/metricsql"
)

func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		q, err := strconv.Unquote(sc.Text())
		if err != nil {
			panic(err)
		}
		if _, err := metricsql.Parse(q); err != nil {
			fmt.Println("R")
		} else {
			fmt.Println("A")
		}
	}
}
`

// writeVerdicts records whether the upstream metricsql parser, at the version our fork is
// based on, accepts each query. It runs in a separate module without our replace.
func writeVerdicts(t *testing.T, queries []string) {
	t.Helper()
	version := upstreamVersion(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(upstreamVerdictProgram), 0o644))
	run := func(args ...string) {
		cmd := exec.CommandContext(t.Context(), "go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "go %s: %s", strings.Join(args, " "), out)
	}
	run("mod", "init", "verdict")
	run("get", "github.com/VictoriaMetrics/metricsql@"+version)

	var in strings.Builder
	seen := map[string]bool{}
	var unique []string
	for _, q := range queries {
		if !seen[q] {
			seen[q] = true
			unique = append(unique, q)
			in.WriteString(strconv.Quote(q))
			in.WriteByte('\n')
		}
	}
	cmd := exec.CommandContext(t.Context(), "go", "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	cmd.Stdin = strings.NewReader(in.String())
	out, err := cmd.Output()
	require.NoError(t, err)
	verdicts := strings.Fields(string(out))
	require.Len(t, verdicts, len(unique))

	var b strings.Builder
	fmt.Fprintf(&b, "# source: github.com/VictoriaMetrics/metricsql %s (upstream, unmodified) verdicts: A accepted, R rejected\n", version)
	for i, q := range unique {
		b.WriteString(verdicts[i])
		b.WriteByte(' ')
		b.WriteString(strconv.Quote(q))
		b.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(filepath.Join(corpusDir, "upstream_verdicts.txt"), []byte(b.String()), 0o644))
	t.Logf("upstream_verdicts.txt: %d queries", len(unique))
}

// upstreamVersion returns the upstream metricsql version pint requires, our fork is
// based on it.
func upstreamVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../../go.mod")
	require.NoError(t, err)
	m := regexp.MustCompile(`(?m)^\s*github\.com/VictoriaMetrics/metricsql (v\S+)`).FindSubmatch(data)
	require.NotNil(t, m, "no metricsql require in go.mod")
	return string(m[1])
}

func moduleDir(t *testing.T, mod string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", mod).Output()
	require.NoError(t, err, "go list %s", mod)
	return strings.TrimSpace(string(out))
}

func goStringArgs(t *testing.T, path, testName string, fns ...string) []string {
	t.Helper()
	f, err := goparser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	var out []string
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != testName {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok || len(ce.Args) == 0 {
				return true
			}
			if id, ok := ce.Fun.(*ast.Ident); ok && slices.Contains(fns, id.Name) {
				if s, ok := stringLit(ce.Args[0]); ok {
					out = append(out, s)
				}
			}
			return true
		})
	}
	require.NotEmpty(t, out, "no queries in %s %s", path, testName)
	return out
}

func stringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	return s, err == nil
}

func prometheusInputs(t *testing.T, path string) []string {
	t.Helper()
	f, err := goparser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "input" {
			if s, ok := stringLit(kv.Value); ok {
				out = append(out, s)
			}
		}
		return true
	})
	require.NotEmpty(t, out, "no inputs in %s", path)
	return out
}

func testscriptExprs(t *testing.T, dir string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	require.NoError(t, err)
	var out []string
	for _, p := range paths {
		ar, err := txtar.ParseFile(p)
		require.NoError(t, err)
		for _, f := range ar.Files {
			if !strings.HasSuffix(f.Name, ".yml") && !strings.HasSuffix(f.Name, ".yaml") {
				continue
			}
			var doc any
			if yaml.Unmarshal(f.Data, &doc) != nil {
				continue
			}
			collectExprs(doc, &out)
		}
	}
	require.NotEmpty(t, out)
	return out
}

func collectExprs(v any, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if s, ok := val.(string); ok && k == "expr" {
				*out = append(*out, s)
				continue
			}
			collectExprs(val, out)
		}
	case []any:
		for _, val := range x {
			collectExprs(val, out)
		}
	}
}

var (
	docsFenceRe  = regexp.MustCompile("(?s)```(?:metricsql|promql)?\n(.*?)```")
	docsInlineRe = regexp.MustCompile("`([^`\n]+)`")
)

// docsExamples returns code examples from the MetricsQL documentation that look like
// queries: fenced blocks and inline code with a call, selector or range.
func docsExamples(t *testing.T) []string {
	t.Helper()
	uri := "https://raw.githubusercontent.com/VictoriaMetrics/VictoriaMetrics/" + metricsqlDocsCommit + "/docs/victoriametrics/MetricsQL.md"
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, uri)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var out []string
	for _, m := range docsFenceRe.FindAllStringSubmatch(string(body), -1) {
		if q := strings.TrimSpace(m[1]); q != "" && !strings.Contains(q, "\n\n") {
			out = append(out, q)
		}
	}
	for _, m := range docsInlineRe.FindAllStringSubmatch(string(body), -1) {
		q := strings.TrimSpace(m[1])
		if strings.ContainsAny(q, "({[") && !strings.HasPrefix(q, "-") && !strings.Contains(q, "://") {
			out = append(out, q)
		}
	}
	require.NotEmpty(t, out)
	return out
}

func writeCorpus(t *testing.T, name, source string, queries []string) {
	t.Helper()
	seen := map[string]bool{}
	var b strings.Builder
	fmt.Fprintf(&b, "# source: %s\n", source)
	for _, q := range queries {
		if seen[q] {
			continue
		}
		seen[q] = true
		b.WriteString(strconv.Quote(q))
		b.WriteByte('\n')
	}
	require.NoError(t, os.MkdirAll(corpusDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(corpusDir, name), []byte(b.String()), 0o644))
	t.Logf("%s: %d queries", name, len(seen))
}

func readCorpus(t *testing.T, name string) []string {
	t.Helper()
	return readCorpusTB(t, name)
}

func readCorpusTB(t testing.TB, name string) []string {
	t.Helper()
	f, err := os.Open(filepath.Join(corpusDir, name))
	require.NoError(t, err)
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		q, err := strconv.Unquote(line)
		require.NoError(t, err, "%s: %s", name, line)
		out = append(out, q)
	}
	require.NoError(t, sc.Err())
	return out
}
