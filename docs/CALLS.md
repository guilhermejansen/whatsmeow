# Calls (VoIP) in whatsmeow

This document explains how calling works across the two layers of this repo:

1. **Base library** (`package whatsmeow`) — call *signaling* primitives.
2. **`calls/` package** (`go.mau.fi/whatsmeow/calls`) — the full *media* engine
   (audio/video), vendored from [purpshell/meowcaller](https://github.com/purpshell/meowcaller),
   plus the `calls/voiceagent` Bot/IVR/AI layer.

If you just want to make/receive calls, read [`../calls/README.md`](../calls/README.md).
This document is for understanding and maintaining the internals.

---

## 1. Signaling in the base library

WhatsApp call signaling is a series of `<call>` stanzas. whatsmeow parses inbound ones
into `events.Call*` (`CallOffer`, `CallAccept`, `CallTerminate`, …) in
[`call.go`](../call.go) `handleCallEvent`, and can send:

| Method | Stanza | Notes |
|--------|--------|-------|
| `RejectCall`    | `<call><reject>`     | decline an inbound call |
| `PreAcceptCall` | `<call><preaccept>`  | ringing ack; must be followed by accept/reject |
| `AcceptCall`    | `<call><accept>`     | accept (needs media to actually connect) |
| `TerminateCall` | `<call><terminate>`  | hang up |
| `MakeCall` / `MakeCallOffer` | `<call><offer>` | **originate** a call (signaling only) |

### MakeCall / MakeCallOffer

`Client.MakeCall(ctx, to, video)` and `Client.MakeCallOffer(ctx, MakeCallParams)` send a
real call offer: they resolve the callee LID, discover its devices, generate a 32-byte
call key, encrypt the key to each device's Signal session (fetching a pre-key bundle and
attaching the signed device identity for a fresh `pkmsg` session), attach the privacy
token, and send the `<offer>`. The callee's phone rings.

They are **signaling only** — they negotiate no media, so audio never connects on its
own. `MakeCallOffer` returns the `CallID` and `CallKey` used, which a media engine needs
to derive SRTP keys. For real calls use [`calls.Client.Call`](#2-the-media-engine-calls),
which drives this offer plus the relay election, keying and media loop.

The offer's child order is **load-bearing** — the server rejects a malformed offer:

```
<call to=<peerLID> id=<msgid>>
  <offer call-id=<id> call-creator=<ownLID>>
    [<privacy>...]          (privacy token, if any)
    <audio enc=opus rate=8000/>
    <audio enc=opus rate=16000/>
    [<video .../>]          (video call only)
    <net medium=3/>
    <capability ver=1>...</capability>
    <enc v=2 type=pkmsg|msg count=0>...</enc>   (single device)
      — or —
    <destination><to jid=...><enc .../></to>...</destination>  (multi-device)
    <encopt keygen=2/>
    [<device-identity>...]  (pkmsg only)
  </offer>
</call>
```

> ⚠️ **Duplication note.** This builder is duplicated: once in base-lib
> `MakeCallOffer` (so the base library can originate calls without the media engine), and
> once in `calls/signaling/stanza.go` `BuildOffer` (used by the media engine, kept
> byte-faithful to upstream meowcaller). If WhatsApp changes the offer format, update
> **both**. The base-lib copy is the secondary path; the `calls/` copy tracks upstream.

---

## 2. The media engine (`calls/`)

`calls.NewClient(wa)` wraps a whatsmeow client and runs the full call lifecycle: it
listens for `events.Call*`, decrypts the call key, answers the relay-latency probes,
elects a relay, derives E2E-SRTP keys from the call key, and runs the per-frame media
loop (encode a `Player`'s frames out as MLOW/RTP/SRTP; decode the peer's frames into an
`AudioSink`). See [`../calls/doc.go`](../calls/doc.go) and the package source.

### The call-node hook (why it exists)

The media engine needs two things whatsmeow doesn't surface through events:

1. The **raw `<call>` node** — it carries the stanza `id` (dropped by the parsed events)
   and the in-call `<mute_v2>` / `<video>` nodes, and a video upgrade needs a typed
   `type="video"` ack (the generic ack does not satisfy the peer).
2. **`<ack class="call">`** — whatsmeow silently drops `<ack>` nodes, but an **outbound**
   call's relay allocation arrives only inside `<ack class="call" type="offer">`. Without
   it the caller never learns the relay endpoint and media never starts.

### How the hook is exposed: `RegisterCallNodeHandler`

The base library exposes a supported seam in [`call.go`](../call.go):

```go
type CallNodeHandler func(ctx context.Context, node *waBinary.Node) (handled bool)

func (cli *Client) RegisterCallNodeHandler(fn CallNodeHandler) (remove func())
```

- For a `<call>` node, the handler runs **before** whatsmeow's default ack + event
  dispatch. Returning `true` means "fully handled" → whatsmeow skips its default ack and
  dispatch (used for the typed video-upgrade ack). Returning `false` lets whatsmeow
  proceed normally (the handler may still have observed the node, e.g. for a deferred
  accept on the first `<mute_v2>`).
- For an `<ack class="call">` node, whatsmeow routes it to the handler via a new `"ack"`
  entry in its node-handler map (`handleAckNode`); every other ack is dropped exactly as
  before. The return value is ignored for acks.

Registration is a single atomic store, so it is **race-free and may be called any time**
(including after `Connect`). The `calls` engine registers it in
`engine.installCallAckHook`.

### Migration note: replacing the old `unsafe` hook

Upstream meowcaller installed this interception by reaching into whatsmeow's **private**
`nodeHandlers` map with `reflect` + `unsafe.Pointer` — which upstream itself marked "NOT
VALIDATED" because it breaks silently if whatsmeow changes internally. The vendored copy
replaces that with `RegisterCallNodeHandler`. If you maintain other code that used the
unsafe trick, switch to `RegisterCallNodeHandler`:

```go
// OLD (fragile): reflect + unsafe poke into cli.nodeHandlers["call"] / ["ack"]
// NEW (supported):
remove := wa.RegisterCallNodeHandler(func(_ context.Context, node *waBinary.Node) bool {
    switch node.Tag {
    case "ack":  // only <ack class="call"> reaches here
        handleCallAck(node); return true
    case "call":
        return handleRawCall(node) // true => skip whatsmeow's default ack+dispatch
    }
    return false
})
defer remove()
```

---

## 3. Vendoring & upstream sync

`calls/` is regenerated from a meowcaller checkout by
[`../scripts/sync-meowcaller.sh`](../scripts/sync-meowcaller.sh): it copies a fixed file
set, rewrites `package meowcaller` → `package calls` and the import prefix
`github.com/purpshell/meowcaller` → `go.mau.fi/whatsmeow/calls`, and re-applies the one
hook patch. The CGO `audio/malgo` mic/speaker helper and the upstream `examples/` are not
ported (server media uses files/PCM/callbacks). See
[`../calls/UPSTREAM.md`](../calls/UPSTREAM.md).

To pull upstream fixes:

```bash
scripts/sync-meowcaller.sh                 # clones purpshell/meowcaller and re-ports
# or against a local checkout:
scripts/sync-meowcaller.sh /path/to/meowcaller
go build ./calls/... && go test ./calls/...
```

Keep the two upstreams trackable: the base library follows official whatsmeow
(`tulir/whatsmeow`); `calls/` follows `purpshell/meowcaller`.

---

## 4. Limitations

- **1:1 only** (no group calls).
- Audio→video **upgrade send** and **Opus** fallback are work-in-progress upstream.
- The relay/media path is only proven by a **live call** (the relay must answer within
  ~5–12 s; there is no retry).
