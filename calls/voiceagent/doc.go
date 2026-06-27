// Package voiceagent builds Bot, IVR and AI voice-agent behaviours on top of the
// go.mau.fi/whatsmeow/calls 1:1 media engine.
//
// The calls package gives you the raw call primitives — answer/reject/hangup, play an
// AudioSource out, receive the peer's decoded 16 kHz mono PCM, and lifecycle callbacks.
// voiceagent layers turn-based conversation on top of those:
//
//   - [Session] wraps a live *calls.Call with high-level helpers: Say (text→speech),
//     Listen (record the caller until they stop speaking, via VAD), Record (fixed
//     duration), and PlayAndWait. It supports barge-in (the caller interrupting TTS).
//   - [Bot] auto-answers inbound calls and runs a fixed, deterministic script.
//   - [IVR] runs a voice menu (no DTMF — WhatsApp has no touch tones): play a prompt,
//     listen, transcribe, match keywords/intents, branch.
//   - [Agent] runs a real-time AI loop: listen → transcribe → LLM → speak, with barge-in.
//
// Speech-to-text, the LLM, and text-to-speech are injected through the [Transcriber],
// [Responder] and [Synthesizer] interfaces, so there is no provider lock-in: wire in
// OpenAI, Groq, Whisper, Azure, a local model, or anything else. This package
// deliberately ships no concrete AI-provider implementations, keeping the whatsmeow
// module free of AI SDK dependencies; implement the interfaces in your application (see
// the examples under calls/examples).
//
// All audio crosses these interfaces as 16 kHz mono PCM — the call's native format
// ([calls.SampleRate] / [calls.FrameSamples]). Helpers ([PCM16Source], [FramesToWAV],
// [FramesToS16LE]) convert between that and the byte formats provider APIs expect.
package voiceagent
