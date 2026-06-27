// Package calls is a native WhatsApp 1:1 VoIP engine for whatsmeow: it makes and
// receives real audio (and receives video) calls in pure Go, with no external media
// server.
//
// It is a re-homed copy of github.com/purpshell/meowcaller (MIT, © Rajeh Taher) wired
// into this module so the whole calling stack ships with whatsmeow. The engine covers
// signaling (offer/preaccept/accept/relaylatency/transport/terminate), end-to-end keying,
// relay election, the MLOW audio codec, RTP and E2E-SRTP, and the per-frame media loop.
// See UPSTREAM.md for how it is vendored and re-synced.
//
// # Quick start
//
// Construct a Client around an UNCONNECTED whatsmeow client (so the low-level call-node
// hook is installed before the receive loop starts), then connect:
//
//	wa := whatsmeow.NewClient(device, log)   // not connected yet
//	cc := calls.NewClient(wa)                // installs the call hook
//	wa.Connect()
//
//	// Receive calls:
//	cc.OnIncomingCall(func(c *calls.Call) {
//		_ = c.Answer()
//		c.OnReady(func() { c.Play(mustSource("hello.wav")) })
//	})
//
//	// Place a call:
//	c, _ := cc.Call(ctx, "15551234567")
//	c.OnReady(func() { c.Play(mustSource("hello.wav")) })
//
// # Pieces
//
//   - [Client] / [Call] are the managed API: place/receive a call, attach a [Player]
//     (outbound audio) and an [AudioSink] (inbound audio), and react via OnReady/OnEnd/
//     OnStateChange.
//   - [AudioSource] producers: [WAVFile], [MP3File], [OpusFile], [PCMStream]. Recorders:
//     [WAVRecorder]. Frames are 16 kHz mono ([SampleRate] / [FrameSamples]).
//   - Sub-package go.mau.fi/whatsmeow/calls/voiceagent builds Bot, IVR and AI voice-agent
//     behaviours (Say/Listen with pluggable STT/LLM/TTS) on top of these primitives.
//
// # Relationship to whatsmeow.Client.MakeCall
//
// The base whatsmeow library has a signaling-only Client.MakeCall that sends a call
// offer (the peer rings) but negotiates no media. This package is what turns that into a
// connected audio call; prefer calls.Client.Call for real calls.
//
// # Limitations
//
// 1:1 only (no group calls). The audio→video upgrade send path and Opus codec fallback
// are work-in-progress upstream and unvalidated. A live relay is required to validate the
// end-to-end media path.
package calls
