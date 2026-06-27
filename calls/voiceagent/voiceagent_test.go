package voiceagent

import (
	"math"
	"testing"

	"go.mau.fi/whatsmeow/calls"
)

func TestEnergyVAD(t *testing.T) {
	vad := NewEnergyVAD(EnergyVADConfig{Threshold: 0.05})

	silence := make([]float32, calls.FrameSamples)
	if vad.Voiced(silence) {
		t.Fatal("silence should not be voiced")
	}

	loud := make([]float32, calls.FrameSamples)
	for i := range loud {
		loud[i] = float32(0.5 * math.Sin(float64(i)*0.2))
	}
	if !vad.Voiced(loud) {
		t.Fatal("a 0.5-amplitude tone should be voiced")
	}

	quiet := make([]float32, calls.FrameSamples)
	for i := range quiet {
		quiet[i] = float32(0.001 * math.Sin(float64(i)*0.2))
	}
	if vad.Voiced(quiet) {
		t.Fatal("a 0.001-amplitude tone should be below threshold")
	}
}

func TestMatchOption(t *testing.T) {
	opts := []IVROption{
		{Keywords: []string{"sales", "buy"}, Next: "sales"},
		{Keywords: []string{"support", "help"}, Next: "support"},
		{Next: "fallback"}, // catch-all
	}
	cases := map[string]string{
		"I want to talk to SALES please": "sales",
		"can you help me":                "support",
		"something unrelated":            "fallback",
	}
	for transcript, want := range cases {
		opt := matchOption(opts, transcript)
		if opt == nil {
			t.Fatalf("no option matched %q", transcript)
		}
		if opt.Next != want {
			t.Fatalf("transcript %q matched %q, want %q", transcript, opt.Next, want)
		}
	}

	// No catch-all → genuinely unmatched returns nil.
	noDefault := []IVROption{{Keywords: []string{"yes"}, Next: "y"}}
	if matchOption(noDefault, "absolutely not") != nil {
		t.Fatal("expected no match without a catch-all option")
	}
}

func TestFramesToS16LERoundTrip(t *testing.T) {
	frame := make([]float32, calls.FrameSamples)
	for i := range frame {
		frame[i] = float32(math.Sin(float64(i) * 0.1))
	}
	pcm := FramesToS16LE(frame)
	if len(pcm) != len(frame)*2 {
		t.Fatalf("s16le length = %d, want %d", len(pcm), len(frame)*2)
	}

	src := PCM16Source(pcm)
	defer src.Close()
	got, err := src.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if len(got) != calls.FrameSamples {
		t.Fatalf("decoded frame len = %d, want %d", len(got), calls.FrameSamples)
	}
	for i := range got {
		if d := math.Abs(float64(got[i] - frame[i])); d > 1.0/32768.0*2 {
			t.Fatalf("sample %d roundtrip drift %v too large", i, d)
		}
	}
}

func TestFramesToWAVHeader(t *testing.T) {
	wav := FramesToWAV(make([]float32, calls.FrameSamples))
	if len(wav) != 44+calls.FrameSamples*2 {
		t.Fatalf("wav length = %d, want %d", len(wav), 44+calls.FrameSamples*2)
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || string(wav[36:40]) != "data" {
		t.Fatal("WAV header chunk ids are wrong")
	}
}
