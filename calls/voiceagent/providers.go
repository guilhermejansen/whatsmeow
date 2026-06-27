package voiceagent

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"

	"go.mau.fi/whatsmeow/calls"
)

// Role is the speaker role of a conversation [Turn].
type Role string

const (
	// RoleSystem is the system/instruction prompt that primes the assistant.
	RoleSystem Role = "system"
	// RoleUser is the human caller.
	RoleUser Role = "user"
	// RoleAssistant is the agent/LLM.
	RoleAssistant Role = "assistant"
)

// Turn is one entry in a conversation history passed to a [Responder].
type Turn struct {
	Role Role
	Text string
}

// Transcriber converts captured caller audio to text (speech-to-text). pcm is 16 kHz
// mono float32 in [-1, 1); sampleRate is always [calls.SampleRate] and is passed for
// clarity so implementations can encode to whatever their API expects (use
// [FramesToWAV] or [FramesToS16LE]). Wire any STT backend (Whisper, Groq, Google,
// Azure, a local model) behind this.
type Transcriber interface {
	Transcribe(ctx context.Context, pcm []float32, sampleRate int) (string, error)
}

// Responder produces the assistant's next reply from the conversation history (the
// LLM step). The history is ordered oldest-first and includes the system prompt (if
// any) and all prior user/assistant turns. Wire any chat model behind this.
type Responder interface {
	Respond(ctx context.Context, history []Turn) (string, error)
}

// Synthesizer converts text to speech (text-to-speech) as a playable [calls.AudioSource]
// in the call's native 16 kHz mono format. Implementations decode their TTS output to
// that format; [PCM16Source] wraps raw s16le mono 16 kHz PCM, which most TTS APIs can
// emit directly. Wire any TTS backend behind this.
type Synthesizer interface {
	Synthesize(ctx context.Context, text string) (calls.AudioSource, error)
}

// TranscriberFunc adapts a plain function to a [Transcriber].
type TranscriberFunc func(ctx context.Context, pcm []float32, sampleRate int) (string, error)

// Transcribe calls f.
func (f TranscriberFunc) Transcribe(ctx context.Context, pcm []float32, sampleRate int) (string, error) {
	return f(ctx, pcm, sampleRate)
}

// ResponderFunc adapts a plain function to a [Responder].
type ResponderFunc func(ctx context.Context, history []Turn) (string, error)

// Respond calls f.
func (f ResponderFunc) Respond(ctx context.Context, history []Turn) (string, error) {
	return f(ctx, history)
}

// SynthesizerFunc adapts a plain function to a [Synthesizer].
type SynthesizerFunc func(ctx context.Context, text string) (calls.AudioSource, error)

// Synthesize calls f.
func (f SynthesizerFunc) Synthesize(ctx context.Context, text string) (calls.AudioSource, error) {
	return f(ctx, text)
}

// VAD is a voice-activity detector. Voiced reports whether one 16 kHz mono frame
// (FrameSamples long) contains speech; [Session.Listen] uses it to find the end of a
// caller's utterance. Reset clears any internal state between utterances. A built-in
// energy detector is available via [NewEnergyVAD]; wire a smarter one (e.g. WebRTC VAD,
// Silero) by implementing this interface.
type VAD interface {
	Voiced(frame []float32) bool
	Reset()
}

// EnergyVADConfig configures the built-in RMS energy [VAD].
type EnergyVADConfig struct {
	// Threshold is the RMS amplitude (0..1) above which a frame counts as speech.
	// Typical speech sits around 0.02–0.2; background noise below ~0.01. Default 0.015.
	Threshold float64
}

type energyVAD struct {
	threshold float64
}

// NewEnergyVAD returns a simple RMS-energy [VAD]. It is dependency-free and good enough
// for clean audio; for noisy environments inject a spectral/ML detector instead.
func NewEnergyVAD(cfg EnergyVADConfig) VAD {
	t := cfg.Threshold
	if t <= 0 {
		t = 0.015
	}
	return &energyVAD{threshold: t}
}

// Voiced reports whether the frame's RMS energy exceeds the threshold.
func (v *energyVAD) Voiced(frame []float32) bool {
	if len(frame) == 0 {
		return false
	}
	var sum float64
	for _, s := range frame {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(frame)))
	return rms >= v.threshold
}

// Reset is a no-op for the stateless energy VAD.
func (v *energyVAD) Reset() {}

// PCM16Source wraps in-memory signed-16-bit little-endian mono 16 kHz PCM as a playable
// [calls.AudioSource]. Use it in a [Synthesizer] when the TTS backend returns raw PCM.
func PCM16Source(pcm []byte) calls.AudioSource {
	return calls.PCMStream(io.NopCloser(bytes.NewReader(pcm)))
}

// FramesToS16LE flattens 16 kHz mono float32 frames into signed-16-bit little-endian
// PCM bytes — the format most STT APIs accept as raw input.
func FramesToS16LE(pcm []float32) []byte {
	out := make([]byte, len(pcm)*2)
	for i, s := range pcm {
		v := s * 32768.0
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(v)))
	}
	return out
}

// FramesToWAV encodes 16 kHz mono float32 PCM as a canonical 16-bit PCM WAV file (44-byte
// header + samples) — convenient for STT APIs that want an uploadable WAV.
func FramesToWAV(pcm []float32) []byte {
	body := FramesToS16LE(pcm)
	dataLen := uint32(len(body))
	buf := make([]byte, 44+len(body))
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], 36+dataLen)
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], 1) // mono
	binary.LittleEndian.PutUint32(buf[24:28], calls.SampleRate)
	binary.LittleEndian.PutUint32(buf[28:32], calls.SampleRate*2) // byte rate
	binary.LittleEndian.PutUint16(buf[32:34], 2)                  // block align
	binary.LittleEndian.PutUint16(buf[34:36], 16)                 // bits/sample
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], dataLen)
	copy(buf[44:], body)
	return buf
}
