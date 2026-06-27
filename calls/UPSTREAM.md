# calls/ — vendored from purpshell/meowcaller

This package is a faithful copy of [purpshell/meowcaller](https://github.com/purpshell/meowcaller.git)
(MIT, © Rajeh Taher), re-homed under `go.mau.fi/whatsmeow/calls` so the whatsmeow module
ships the full WhatsApp 1:1 VoIP stack natively.

- Upstream revision: `0f1265d`
- Regenerate with: `scripts/sync-meowcaller.sh [path-to-meowcaller]`

## Transform applied by the sync script
1. Copy root `*.go` + sub-packages: diag stun util mlow rtp relay signaling srtp (verbatim, incl. tests/testdata).
2. Skip `audio/` (CGO miniaudio, CLI only), `examples/`, `scripts/`, nested go.mod.
3. `package meowcaller` → `package calls`; import prefix
   `github.com/purpshell/meowcaller` → `go.mau.fi/whatsmeow/calls`.
4. Replace `engine.go` `installCallAckHook` (reflection + unsafe poke into the
   client's private nodeHandlers map) with `Client.RegisterCallNodeHandler` and
   drop the `reflect`/`unsafe` imports. This is the ONLY behavioural divergence
   from upstream.

The base-library offer builder (`Client.MakeCallOffer` in call.go) duplicates the
load-bearing offer child order from `calls/signaling/stanza.go` BuildOffer. If you
pull an upstream change to that stanza, mirror it there too.
