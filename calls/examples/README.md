# calls examples

Runnable demos of `go.mau.fi/whatsmeow/calls` and `calls/voiceagent`. This is a
**separate Go module** (own `go.mod`, `replace go.mau.fi/whatsmeow => ../..`) so it can
use a database driver and demo helpers without adding anything to the whatsmeow module.
It uses the pure-Go `modernc.org/sqlite` driver (no CGO).

On first run each example prints a QR payload — render it as a QR code (e.g. paste into
any QR generator, or wire in `github.com/mdp/qrterminal`) and scan it under WhatsApp →
**Linked devices**. The session is saved to a local `*.db` file, so later runs skip the
scan.

| Example | What it shows |
|---------|---------------|
| [`auto-answer`](auto-answer)   | Auto-answer inbound calls and play a greeting file. The simplest fixed-script bot. |
| [`recording`](recording)       | Auto-answer and record the caller to a WAV file (voicemail). |
| [`ivr`](ivr)                   | Voice menu (no DTMF): prompt → listen → transcribe → branch on keywords. |
| [`ai-agent`](ai-agent)         | Real-time AI voice agent: listen → STT → LLM → TTS, with barge-in. |
| [`outbound-tts`](outbound-tts) | Place an outbound call and speak a message via TTS. |

```bash
cd calls/examples
go run ./auto-answer  greeting.wav
go run ./recording
go run ./ivr
go run ./ai-agent
go run ./outbound-tts 15551234567 "Hello from a Go voice bot"
```

## Providers (STT / LLM / TTS)

The `ivr`, `ai-agent` and `outbound-tts` demos wire **dependency-free stub** providers
(`exampleutil.StubTranscriber` / `StubResponder` / `StubSynthesizer`) so they compile and
run with no external services. The stubs emit silence and canned text — replace them with
real implementations of the `voiceagent.Transcriber`, `voiceagent.Responder` and
`voiceagent.Synthesizer` interfaces to get real speech recognition, an LLM, and TTS.

A real provider is just an adapter, e.g.:

```go
tts := voiceagent.SynthesizerFunc(func(ctx context.Context, text string) (calls.AudioSource, error) {
    pcm, err := myTTS.Speak(ctx, text) // returns 16 kHz mono s16le PCM
    if err != nil {
        return nil, err
    }
    return voiceagent.PCM16Source(pcm), nil
})

stt := voiceagent.TranscriberFunc(func(ctx context.Context, pcm []float32, sr int) (string, error) {
    return myWhisper.Transcribe(ctx, voiceagent.FramesToWAV(pcm)) // upload a WAV
})
```

Choose providers per your stack (OpenAI, Groq, Whisper, Azure, Google, a local model);
the whatsmeow module itself stays free of any AI SDK dependency.
