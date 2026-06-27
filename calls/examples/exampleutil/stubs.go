package exampleutil

import (
	"context"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow/calls"
	"go.mau.fi/whatsmeow/calls/voiceagent"
)

// These stub providers let the IVR/AI examples compile and run end to end without any
// external AI SDK. Replace them with real implementations (OpenAI/Groq/Whisper/Azure/
// local models) in production — that is the whole point of the provider interfaces.

// StubSynthesizer "speaks" by emitting silence roughly proportional to the text length,
// so the call timing looks realistic. Swap for a real TTS that returns 16 kHz mono PCM.
func StubSynthesizer() voiceagent.Synthesizer {
	return voiceagent.SynthesizerFunc(func(_ context.Context, text string) (calls.AudioSource, error) {
		// ~50 ms per character, clamped to [0.5s, 6s].
		d := time.Duration(len(text)) * 50 * time.Millisecond
		if d < 500*time.Millisecond {
			d = 500 * time.Millisecond
		}
		if d > 6*time.Second {
			d = 6 * time.Second
		}
		samples := int(d.Seconds() * float64(calls.SampleRate))
		return voiceagent.PCM16Source(make([]byte, samples*2)), nil
	})
}

// StubTranscriber returns the given phrases in rotation, one per call, so an IVR/agent
// loop makes progress. Swap for a real STT (Whisper/Groq/Google).
func StubTranscriber(phrases ...string) voiceagent.Transcriber {
	if len(phrases) == 0 {
		phrases = []string{"hello"}
	}
	var i atomic.Int64
	return voiceagent.TranscriberFunc(func(_ context.Context, _ []float32, _ int) (string, error) {
		n := int(i.Add(1)-1) % len(phrases)
		return phrases[n], nil
	})
}

// StubResponder echoes a canned reply that quotes the last user turn. Swap for a real
// chat model (GPT/Claude/Gemini/Llama).
func StubResponder() voiceagent.Responder {
	return voiceagent.ResponderFunc(func(_ context.Context, history []voiceagent.Turn) (string, error) {
		last := ""
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == voiceagent.RoleUser {
				last = history[i].Text
				break
			}
		}
		return "You said: " + last + ". How else can I help?", nil
	})
}
