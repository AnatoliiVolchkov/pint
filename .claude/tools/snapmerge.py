# Keep the committed order of a go-snaps file after UPDATE_SNAPS=true reordered it.
#
# Usage, from the repository root, after regenerating snapshots of the whole package:
#   python3 .claude/tools/snapmerge.py internal/checks/metricsql_test.snap
#
# Takes the content of every entry from the regenerated file and the order from HEAD.
# New entries go right after their predecessor in the regenerated order (same test
# function), or before their successor if they are first. Fails if an entry from HEAD
# is missing, unless --drop-missing is passed for renamed or removed tests.
# Review `git diff` afterwards: it should only show real changes.
import re, subprocess, sys
path = sys.argv[1]
base = subprocess.run(['git', 'show', f'HEAD:{path}'], capture_output=True, text=True, check=True).stdout
new = open(path).read()
def parse(s):
    parts = re.split(r'(?m)^(?=\[[^\n]+ - \d+\]$)', s)
    head, entries = parts[0], parts[1:]
    out = []
    for e in entries:
        key = e.split('\n', 1)[0]
        body = e.rstrip('\n') + '\n\n'
        out.append((key, body))
    return head, out
bh, be = parse(base)
nh, ne = parse(new)
newmap = dict(ne)
basekeys = [k for k, _ in be]
missing = [k for k in basekeys if k not in newmap]
if missing and '--drop-missing' not in sys.argv:
    sys.exit(f'entries missing from regenerated file (renamed or removed tests? pass --drop-missing): {missing[:5]}')
for k in missing:
    print(f'dropped {k}')
result = [(k, newmap[k]) for k in basekeys if k in newmap]
order = [k for k, _ in ne]
func = lambda k: k[1:].split('/', 1)[0]
for i, (k, body) in enumerate(ne):
    if k in basekeys:
        continue
    keys = [x for x, _ in result]
    prev = next((order[j] for j in range(i - 1, -1, -1) if func(order[j]) == func(k) and order[j] in keys), None)
    if prev is not None:
        result.insert(keys.index(prev) + 1, (k, body))
        continue
    nxt = next((order[j] for j in range(i + 1, len(order)) if func(order[j]) == func(k) and order[j] in keys), None)
    if nxt is not None:
        result.insert(keys.index(nxt), (k, body))
    else:
        result.append((k, body))
out = bh + ''.join(b for _, b in result)
open(path, 'w').write(out.rstrip('\n') + '\n')
print(f'{len(result)} entries, {len(result) - len(be) + len(missing)} new, {len(missing)} dropped')
