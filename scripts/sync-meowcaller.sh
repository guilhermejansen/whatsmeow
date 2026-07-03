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
# and re-apply the fork's divergences. Everything else stays byte-faithful so the
# diff against upstream is minimal and re-syncs are mechanical.
#
# The `rm -rf calls/` regeneration would otherwise WIPE the fork's local additions
# on every re-sync. Two layers keep them:
#   1. Fork-owned overlay files (FORK_FILES, e.g. calls/video_fork.go) are pure
#      additions (new methods on meowcaller types). They are saved before the reset
#      and restored after, untouched.
#   2. In-file modifications to upstream functions are re-applied as idempotent,
#      LOUD-on-failure Python patches (the <ack>/<call> hook, and the accept-video
#      <video> node in sendAccept).
# A final `go build ./calls/...` gate makes a broken re-sync fail instead of landing
# silently.
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

# Fork-owned overlay files: NOT from meowcaller. They live in calls/ but are pure
# additions this fork maintains on top of the vendored engine, so they are preserved
# verbatim across the rm -rf regeneration below. Anything listed here survives a re-sync.
FORK_FILES=(video_fork.go)

cleanup() {
	[ -n "${TMP_CLONE:-}" ] && rm -rf "$TMP_CLONE"
	[ -n "${KEEP:-}" ] && rm -rf "$KEEP"
}
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

# 0. Preserve fork-owned overlay files across the reset. They are not regenerated
#    from upstream; they are pure additions this fork maintains on top of meowcaller.
KEEP="$(mktemp -d)"
for f in "${FORK_FILES[@]}"; do
	if [ -f "$DEST/$f" ]; then
		cp "$DEST/$f" "$KEEP/$f"
		echo ">> preserved fork overlay: $f"
	else
		echo ">> (note) fork overlay not present yet, will not restore: $f"
	fi
done

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

# 4b. Re-apply the accept-video patch: a video call MUST accept with a <video> node in
#     sendAccept, or the server rejects the accept. This is the fork's one in-file
#     divergence from upstream sendAccept. Idempotent + LOUD on anchor miss (a future
#     meowcaller sendAccept change must be reviewed, not silently dropped). The
#     AcceptParams.Video field and its <video> emission already exist upstream in
#     signaling/stanza.go — this only flips it on for inbound video offers.
python3 - "$DEST/engine.go" <<'PYEOF'
import sys
path = sys.argv[1]
src = open(path).read()

if 'isVideo := m.isVideo' in src:
    print(">> accept-video patch already present; skipping")
    sys.exit(0)

lines = src.split('\n')
out = []
patched_decl = False
patched_field = False
for ln in lines:
    stripped = ln.strip()
    # 1. Declare isVideo right before the BuildAccept call in sendAccept.
    if stripped == 'accept := signaling.BuildAccept(&signaling.AcceptParams{' and not patched_decl:
        indent = ln[:len(ln) - len(ln.lstrip('\t'))]
        out.append(indent + "// Mirror the offer's media: a video call MUST accept with a <video> node, or the")
        out.append(indent + '// server rejects the accept (error 500) / never negotiates video. Audio-only calls')
        out.append(indent + '// keep isVideo=false, so their accept is unchanged.')
        out.append(indent + 'isVideo := m.isVideo')
        patched_decl = True
    out.append(ln)
    # 2. Add the Video field right after the Metadata field in the same literal.
    if stripped.startswith('Metadata:') and 'peer_abtest_bucket_id_list' in stripped and not patched_field:
        indent = ln[:len(ln) - len(ln.lstrip('\t'))]
        out.append(indent + 'Video:      isVideo,')
        patched_field = True

if not patched_decl:
    sys.exit("!! sendAccept accept-video patch failed: BuildAccept anchor not found")
if not patched_field:
    sys.exit("!! sendAccept accept-video patch failed: Metadata anchor not found")

open(path, 'w').write('\n'.join(out))
print(">> patched engine.go sendAccept (accept-video)")
PYEOF

# 4c. Restore fork-owned overlay files preserved in step 0.
for f in "${FORK_FILES[@]}"; do
	if [ -f "$KEEP/$f" ]; then
		cp "$KEEP/$f" "$DEST/$f"
		echo ">> restored fork overlay: $f"
	fi
done

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
   drop the \`reflect\`/\`unsafe\` imports.

## Fork divergences preserved across re-sync (this is NOT upstream meowcaller)
- **Overlay file \`video_fork.go\`** (${FORK_FILES[*]}) — the fork's OUTBOUND-video
  additions (\`CallVideo\`/\`placeCallVideo\`, \`SetVideoState\`/\`sendVideoState\`).
  Pure additions on meowcaller types; saved before \`rm -rf\` and restored after, so a
  re-sync never wipes them. **NOT VALIDATED** end-to-end (meowcaller video is initial
  support): RTP egress is real, but the WhatsApp peer-side bridge is unproven.
- **accept-video patch in \`engine.go\` \`sendAccept\`** — flips \`AcceptParams.Video\`
  on for inbound video offers (\`isVideo := m.isVideo\`), so a video call answers with a
  \`<video>\` node. Re-applied as an idempotent, loud-on-failure Python patch (step 4b).
  The \`AcceptParams.Video\` field + \`<video>\` emission already exist upstream in
  \`signaling/stanza.go\`.

The base-library offer builder (\`Client.MakeCallOffer\` in call.go) duplicates the
load-bearing offer child order from \`calls/signaling/stanza.go\` BuildOffer. If you
pull an upstream change to that stanza, mirror it there too.
EOF

# 6. Build gate: a re-sync that does not compile must FAIL, not land silently.
echo ">> verifying calls/ builds"
if ! (cd "$REPO_ROOT" && go build ./calls/...); then
	echo "!! go build ./calls/... FAILED after sync — the regenerated tree does not compile" >&2
	exit 1
fi

echo ">> done. calls/ regenerated (rev $UPSTREAM_REV), fork overlay restored, patches applied, build OK."
echo ">> review with: git -C \"$REPO_ROOT\" status calls/ && git -C \"$REPO_ROOT\" diff calls/"
