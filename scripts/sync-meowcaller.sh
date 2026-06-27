#!/usr/bin/env bash
# sync-meowcaller.sh — (re)generate the calls/ subpackage from an upstream
# github.com/purpshell/meowcaller checkout, as a faithful copy with the imports
# rewritten into this module and the unsafe call-node hook replaced by the
# supported whatsmeow API (Client.RegisterCallNodeHandler).
#
# whatsmeow-private vendors the meowcaller MIT-licensed call engine under
# go.mau.fi/whatsmeow/calls so the whole VoIP stack ships inside one module.
# Re-run this whenever you want to pull upstream fixes/updates from meowcaller.
#
# Usage:
#   scripts/sync-meowcaller.sh [path-to-meowcaller-checkout]
#
# With no argument it clones the upstream repo into a temp dir. The transform is
# deterministic: copy a fixed file set, rewrite the package name + import prefix,
# and re-apply the single hook patch. Everything else stays byte-faithful so the
# diff against upstream is minimal and re-syncs are mechanical.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="$REPO_ROOT/calls"
SRC="${1:-}"
MEOWCALLER_GIT="https://github.com/purpshell/meowcaller.git"
OLD_MODULE="github.com/purpshell/meowcaller"
NEW_MODULE="go.mau.fi/whatsmeow/calls"

# Directories ported verbatim (with their *_test.go and testdata).
SUBPKGS=(diag stun util mlow rtp relay signaling srtp)
# Directories never ported: audio/ (CGO miniaudio, CLI mic/speaker only),
# examples/ (have their own go.mod), scripts/ (upstream tooling).

cleanup() { [ -n "${TMP_CLONE:-}" ] && rm -rf "$TMP_CLONE"; }
trap cleanup EXIT

if [ -z "$SRC" ]; then
	TMP_CLONE="$(mktemp -d)"
	echo ">> cloning $MEOWCALLER_GIT"
	git clone --depth 1 "$MEOWCALLER_GIT" "$TMP_CLONE"
	SRC="$TMP_CLONE"
fi

if [ ! -f "$SRC/engine.go" ] || [ ! -d "$SRC/mlow" ]; then
	echo "!! $SRC does not look like a meowcaller checkout (missing engine.go / mlow/)" >&2
	exit 1
fi

UPSTREAM_REV="$(git -C "$SRC" rev-parse --short HEAD 2>/dev/null || echo unknown)"
echo ">> upstream meowcaller revision: $UPSTREAM_REV"

echo ">> resetting $DEST"
rm -rf "$DEST"
mkdir -p "$DEST"

# 1. Root-level *.go (the package `calls`), excluding the CGO audio helper.
for f in "$SRC"/*.go; do
	base="$(basename "$f")"
	cp "$f" "$DEST/$base"
done

# 2. Sub-packages, verbatim (incl. testdata + internal).
for pkg in "${SUBPKGS[@]}"; do
	[ -d "$SRC/$pkg" ] && cp -R "$SRC/$pkg" "$DEST/$pkg"
done

# 3. Rewrite the root package name and the module import prefix everywhere.
#    Subpackage files keep their own package names; only the import prefix moves.
find "$DEST" -name '*.go' -print0 | while IFS= read -r -d '' f; do
	# package meowcaller -> package calls (root files only declare it)
	perl -0pi -e 's/^package meowcaller$/package calls/m' "$f"
	# import prefix github.com/purpshell/meowcaller[...] -> go.mau.fi/whatsmeow/calls[...]
	perl -0pi -e "s{\Q$OLD_MODULE\E}{$NEW_MODULE}g" "$f"
done

# 4. Re-apply the hook patch: replace engine.go installCallAckHook (reflection +
#    unsafe) with the supported Client.RegisterCallNodeHandler, and drop the now
#    unused reflect/unsafe imports. Done in Go-aware Python for reliability.
python3 - "$DEST/engine.go" <<'PYEOF'
import re, sys
path = sys.argv[1]
src = open(path).read()

# Drop the reflect/unsafe imports (only the hook used them).
src = re.sub(r'^\t"reflect"\n', '', src, flags=re.M)
src = re.sub(r'^\t"unsafe"\n', '', src, flags=re.M)

new_func = '''// installCallAckHook registers the engine's low-level interceptor for raw <call>
// and <ack class="call"> nodes via the supported whatsmeow API. whatsmeow has no
// <ack> handler of its own — it drops <ack> nodes — but an outbound call's relay
// allocation arrives only inside <ack class="call" type="offer">, so without
// intercepting it the caller never learns the relay endpoint and media never starts.
// The <call> interceptor also lets the engine see the raw stanza id (which the
// CallOffer event drops) and send the typed type="video" ack a video upgrade needs.
func (e *engine) installCallAckHook() {
\te.c.wa.RegisterCallNodeHandler(func(_ context.Context, node *waBinary.Node) bool {
\t\tswitch node.Tag {
\t\tcase "ack":
\t\t\t// whatsmeow forwards only <ack class="call"> here.
\t\t\te.onCallAck(node)
\t\t\treturn true
\t\tcase "call":
\t\t\t// onCallRaw returns true when it fully handled the node (incl. its own
\t\t\t// typed ack), so whatsmeow skips its generic typeless ack — the <video>
\t\t\t// upgrade needs a type="video" ack that a bare ack does not satisfy.
\t\t\treturn e.onCallRaw(node)
\t\t}
\t\treturn false
\t})
}
'''

# Replace from the installCallAckHook doc comment through the end of the function.
# The function is the last thing before the "// ---- whatsmeow glue" banner.
pattern = re.compile(
    r'// installCallAckHook injects.*?\nfunc \(e \*engine\) installCallAckHook\(\) \{.*?\n\}\n',
    re.S)
if not pattern.search(src):
    sys.exit("!! could not locate installCallAckHook to patch")
src = pattern.sub(new_func, src, count=1)

open(path, 'w').write(src)
print(">> patched engine.go installCallAckHook (removed reflect+unsafe)")
PYEOF

# 5. Record provenance.
cat > "$DEST/UPSTREAM.md" <<EOF
# calls/ — vendored from purpshell/meowcaller

This package is a faithful copy of [purpshell/meowcaller]($MEOWCALLER_GIT)
(MIT, © Rajeh Taher), re-homed under \`$NEW_MODULE\` so the whatsmeow module
ships the full WhatsApp 1:1 VoIP stack natively.

- Upstream revision: \`$UPSTREAM_REV\`
- Regenerate with: \`scripts/sync-meowcaller.sh [path-to-meowcaller]\`

## Transform applied by the sync script
1. Copy root \`*.go\` + sub-packages: ${SUBPKGS[*]} (verbatim, incl. tests/testdata).
2. Skip \`audio/\` (CGO miniaudio, CLI only), \`examples/\`, \`scripts/\`, nested go.mod.
3. \`package meowcaller\` → \`package calls\`; import prefix
   \`$OLD_MODULE\` → \`$NEW_MODULE\`.
4. Replace \`engine.go\` \`installCallAckHook\` (reflection + unsafe poke into the
   client's private nodeHandlers map) with \`Client.RegisterCallNodeHandler\` and
   drop the \`reflect\`/\`unsafe\` imports. This is the ONLY behavioural divergence
   from upstream.

The base-library offer builder (\`Client.MakeCallOffer\` in call.go) duplicates the
load-bearing offer child order from \`calls/signaling/stanza.go\` BuildOffer. If you
pull an upstream change to that stanza, mirror it there too.
EOF

echo ">> done. Review with: git status calls/ && go build ./calls/..."
