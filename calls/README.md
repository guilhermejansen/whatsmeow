# calls — native WhatsApp VoIP for whatsmeow

`go.mau.fi/whatsmeow/calls` makes and receives **real WhatsApp 1:1 calls** — actual
audio in and out (and inbound video) — in **pure Go**, with no external media server.

It is a faithful, re-homed copy of [purpshell/meowcaller](https://github.com/purpshell/meowcaller)
(MIT, © Rajeh Taher) wired into the whatsmeow module, with its only behavioural change
being that it drives the call signaling through the supported
`whatsmeow.Client.RegisterCallNodeHandler` API instead of reaching into whatsmeow's
internals with `reflect`/`unsafe`. See [UPSTREAM.md](UPSTREAM.md) and
[`../scripts/sync-meowcaller.sh`](../scripts/sync-meowcaller.sh) for how it is vendored
and how to pull upstream fixes.

## What it does

- Place outbound 1:1 calls and answer/reject inbound ones.
- Stream audio **out** (MP3/WAV/Opus/raw PCM, or your own `AudioSource`).
- Receive the peer's audio **in** as 16 kHz mono PCM (record to WAV or forward to a callback).
- Receive H.264 video (recording works; the send/upgrade path is work-in-progress upstream).
- Full signaling, end-to-end keying, relay election, MLOW codec, RTP + E2E-SRTP.

## Quick start

```go
import (
    "go.mau.fi/whatsmeow"
    "go.mau.fi/whatsmeow/calls"
)

// 1. Build an UNCONNECTED whatsmeow client.
wa := whatsmeow.NewClient(device, log)

// 2. Wrap it BEFORE connecting so the call-node hook is installed first.
cc := calls.NewClient(wa)

// 3. Connect / log in as usual.
wa.Connect()

// Receive calls:
cc.OnIncomingCall(func(c *calls.Call) {
    _ = c.Answer()
    c.OnReady(func() {
        src, _ := calls.WAVFile("greeting.wav")
        c.Play(src)
    })
    c.Receive(calls.SinkFunc(func(frame []float32) { /* peer audio, 16 kHz mono */ }))
})

// Place a call (phone number, phone JID, or @lid):
c, _ := cc.Call(ctx, "15551234567")
c.OnReady(func() { src, _ := calls.MP3File("message.mp3"); c.Play(src) })
```

## Audio format

All audio crosses the API as **16 kHz mono float32 PCM in 60 ms frames**
(`calls.SampleRate` = 16000, `calls.FrameSamples` = 960). The built-in sources decode
WAV/MP3/Opus/raw PCM into that format; `WAVRecorder` writes it back to a WAV file.

| Direction | API |
|-----------|-----|
| Play out  | `Call.Play(AudioSource)` / `Call.Subscribe(*Player)`; sources: `WAVFile`, `MP3File`, `OpusFile`, `PCMStream` |
| Receive in| `Call.Receive(AudioSink)`; sinks: `WAVRecorder`, `SinkFunc` |
| Video in  | `Call.ReceiveVideo(VideoSink)` (recording; unvalidated) |

## Bot / IVR / AI voice agents

The [`voiceagent`](voiceagent) sub-package layers turn-based conversation on top:

- **Bot** — auto-answer + fixed script (play prompts, record, hang up).
- **IVR** — voice menu (no DTMF): prompt → listen → transcribe → match keywords → branch.
- **Agent** — real-time `listen → STT → LLM → TTS → speak` loop with barge-in.

Speech-to-text, the LLM and text-to-speech are injected via the `Transcriber`,
`Responder` and `Synthesizer` interfaces — **no provider lock-in**, and no AI SDK is
pulled into the whatsmeow module. See [`examples/`](examples) for runnable wiring
(auto-answer, ivr, ai-agent, outbound-tts, recording).

## Base library vs. this package

The base whatsmeow library has a **signaling-only** `Client.MakeCall` /
`Client.MakeCallOffer`: it sends a real offer (the peer rings) but negotiates no media.
This package is what connects the media. Use `calls.Client.Call` for real audio calls.

## Limitations

- **1:1 only** — no group calls.
- The audio→video **upgrade send** path and the **Opus** codec fallback are
  work-in-progress upstream and unvalidated.
- The end-to-end media path can only be proven against a **live relay** (a real call).

## License / credit

This package is MIT-licensed (see [LICENSE.meowcaller](LICENSE.meowcaller)), distributed
inside the MPL-2.0 whatsmeow module. Credit to meowcaller (Rajeh Taher) and the
WaCalls / whatsapp-rust projects it draws on.
