#!/bin/bash
# Compare the findings of two pint binaries, offline, on every local rule set, to prove
# that a change only adds or removes the intended problems.
#
# Usage, from anywhere:
#   .claude/tools/compare-binaries.sh <old pint> <new pint> <output dir>
#
# Rule sets:
#   - .github/metricsql-test (in this repository; expected Fatal=5 Bug=34 Warning=4)
#   - the MetricsQL parser corpus (internal/parser/metricsql/testdata/corpus), as recording rules
#   - ../test in the parent workspace, skipped when missing
#   - a private rule set outside the repository, skipped unless PINT_PRIVATE_RULES names its
#     directory (k8s/ and canary/k8s/ inside); PINT_PRIVATE_LABELS holds its --external-label value
#
# For every set it prints "identical" or the diff of the two outputs.
set -u
OLD=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
NEW=$(cd "$(dirname "$2")" && pwd)/$(basename "$2")
OUT=$3
REPO=$(cd "$(dirname "$0")/../.." && pwd)
WORKSPACE=$(dirname "$REPO")
mkdir -p "$OUT"
export VM_AUTH=${VM_AUTH:-x}   # configs need it set, offline runs don't use it

run() { # name dir args...
  local name=$1 dir=$2
  shift 2
  if [ ! -d "$dir" ]; then
    echo "$name: skipped, $dir is missing"
    return
  fi
  for b in OLD NEW; do
    (cd "$dir" && "${!b}" --no-color --offline --show-duplicates "$@" 2>&1) |
      grep -v '^level=INFO msg="\(Loading\|Finding\|Checking\|Offline\|Configured new\)' >"$OUT/$name.$b.txt"
  done
  if cmp -s "$OUT/$name.OLD.txt" "$OUT/$name.NEW.txt"; then
    echo "$name: identical ($(grep -o 'Problems found.*' "$OUT/$name.NEW.txt"))"
  else
    echo "$name: DIFFERENT"
    diff "$OUT/$name.OLD.txt" "$OUT/$name.NEW.txt" | head -60
  fi
}

# The parser corpus as recording rules. ensure_ascii=False keeps non-ASCII characters
# as they are: double-quoted YAML with \u escapes triggers an upstream reporter panic.
mkdir -p "$OUT/corpus/rules"
printf 'parser {\n  query_language = "metricsql"\n}\n' >"$OUT/corpus/.pint.hcl"
python3 - "$REPO/internal/parser/metricsql/testdata/corpus" "$OUT/corpus/rules/corpus.yml" <<'EOF'
import glob, json, sys
seen, out = set(), ['groups:', '- name: corpus', '  rules:']
for path in sorted(glob.glob(sys.argv[1] + '/*.txt')):
    for line in open(path, encoding='utf-8'):
        line = line.rstrip('\n')
        if not line or line.startswith('#'):
            continue
        if line[:2] in ('A ', 'R '):
            if line[0] == 'R':
                continue
            line = line[2:]
        try:
            q = json.loads(line)
        except ValueError:
            continue
        if q in seen:
            continue
        seen.add(q)
        out += [f'  - record: r{len(seen)}', '    expr: ' + json.dumps(q, ensure_ascii=False)]
open(sys.argv[2], 'w', encoding='utf-8').write('\n'.join(out) + '\n')
EOF

run metricsql-test "$REPO/.github/metricsql-test" lint -n info rules k8s
run corpus "$OUT/corpus" lint -n info rules
run test "$WORKSPACE/test" -c offline.hcl lint -n info rules invalid node-exporter.yml
run fields "$WORKSPACE/test" -c fields.hcl lint -n info rules invalid node-exporter.yml
if [ -n "${PINT_PRIVATE_RULES:-}" ]; then
  run private "$PINT_PRIVATE_RULES" --external-label "${PINT_PRIVATE_LABELS:-}" lint -n info k8s
  run canary "$PINT_PRIVATE_RULES" --external-label "${PINT_PRIVATE_LABELS:-}" lint -n info canary/k8s
fi
