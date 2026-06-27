package voiceagent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/calls"
)

// frameDuration is the wall-clock length of one decoded audio frame (60 ms at 16 kHz).
const frameDuration = time.Duration(calls.FrameSamples) * time.Second / calls.SampleRate

var (
	// ErrCallEnded is returned by Session helpers when the call ends mid-operation.
	ErrCallEnded = errors.New("voiceagent: call ended")
	// ErrNoSpeech is returned by Listen when no speech began before the start timeout.
	ErrNoSpeech = errors.New("voiceagent: no speech detected")
	// ErrNoSynthesizer is returned by Say when no Synthesizer is configured.
	ErrNoSynthesizer = errors.New("voiceagent: no synthesizer configured")
	// ErrNoTranscriber is returned by ListenText when no Transcriber is configured.
	ErrNoTranscriber = errors.New("voiceagent: no transcriber configured")
)

// Session wraps a single live *calls.Call with turn-based helpers used by Bot, IVR and
// Agent: Say (TTS), Listen (record until the caller stops speaking), Record (fixed
// duration) and PlayAndWait. It owns the call's inbound sink and its OnReady/OnEnd
// callbacks, so use Session.OnEnd / Session.WaitUntilReady rather than setting those on
// the underlying Call. A Session's helpers are meant to be called sequentially (one
// turn at a time), not concurrently with each other.
type Session struct {
	Call *calls.Call

	stt    Transcriber
	tts    Synthesizer
	newVAD func() VAD
	log    zerolog.Logger

	tap *frameTap

	readyOnce sync.Once
	ready     chan struct{}
	endedOnce sync.Once
	ended     chan struct{}

	mu          sync.Mutex
	userOnEnd   func(reason string)
	userOnReady func()
	endReason   string
}

// SessionOption configures a Session.
type SessionOption func(*Session)

// WithTranscriber sets the speech-to-text backend (enables Listen-to-text).
func WithTranscriber(t Transcriber) SessionOption { return func(s *Session) { s.stt = t } }

// WithSynthesizer sets the text-to-speech backend (enables Say).
func WithSynthesizer(t Synthesizer) SessionOption { return func(s *Session) { s.tts = t } }

// WithVAD sets the voice-activity-detector factory used by Listen (one VAD per
// utterance). Defaults to NewEnergyVAD with default settings.
func WithVAD(factory func() VAD) SessionOption { return func(s *Session) { s.newVAD = factory } }

// WithLogger sets the session logger.
func WithLogger(l zerolog.Logger) SessionOption { return func(s *Session) { s.log = l } }

// NewSession wraps call and attaches the inbound audio tap and lifecycle callbacks.
// Create it as soon as you have the Call (for inbound, inside OnIncomingCall before
// Answer; for outbound, right after Client.Call), so OnReady/OnEnd are not missed.
func NewSession(call *calls.Call, opts ...SessionOption) *Session {
	s := &Session{
		Call:   call,
		ready:  make(chan struct{}),
		ended:  make(chan struct{}),
		tap:    newFrameTap(256),
		newVAD: func() VAD { return NewEnergyVAD(EnergyVADConfig{}) },
	}
	for _, o := range opts {
		o(s)
	}
	call.Receive(s.tap)
	call.OnReady(s.handleReady)
	call.OnEnd(s.handleEnd)
	if call.State() == calls.CallPhaseActive {
		s.handleReady()
	}
	return s
}

func (s *Session) handleReady() {
	s.readyOnce.Do(func() { close(s.ready) })
	s.mu.Lock()
	fn := s.userOnReady
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (s *Session) handleEnd(reason string) {
	s.mu.Lock()
	s.endReason = reason
	fn := s.userOnEnd
	s.mu.Unlock()
	s.endedOnce.Do(func() { close(s.ended) })
	if fn != nil {
		fn(reason)
	}
}

// OnReady registers a callback fired once media is flowing. Use this instead of
// Call.OnReady (the Session owns that callback).
func (s *Session) OnReady(fn func()) {
	s.mu.Lock()
	s.userOnReady = fn
	s.mu.Unlock()
}

// OnEnd registers a callback fired when the call ends. Use this instead of Call.OnEnd.
func (s *Session) OnEnd(fn func(reason string)) {
	s.mu.Lock()
	s.userOnEnd = fn
	s.mu.Unlock()
}

// Ended returns a channel closed when the call ends.
func (s *Session) Ended() <-chan struct{} { return s.ended }

// EndReason returns the reason the call ended (empty while still live).
func (s *Session) EndReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endReason
}

// WaitUntilReady blocks until media is flowing, the call ends, or ctx is done.
func (s *Session) WaitUntilReady(ctx context.Context) error {
	select {
	case <-s.ready:
		return nil
	case <-s.ended:
		return ErrCallEnded
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Hangup ends the call.
func (s *Session) Hangup() error { return s.Call.Hangup() }

// PlayAndWait plays src to the caller and blocks until it finishes, the call ends, or
// ctx is done (which stops playback).
func (s *Session) PlayAndWait(ctx context.Context, src calls.AudioSource) error {
	done := make(chan struct{})
	var once sync.Once
	p := calls.NewPlayer()
	p.OnFinish(func() { once.Do(func() { close(done) }) })
	s.Call.Subscribe(p)
	p.Play(src)
	select {
	case <-done:
		return nil
	case <-s.ended:
		p.Stop()
		return ErrCallEnded
	case <-ctx.Done():
		p.Stop()
		return ctx.Err()
	}
}

// Say synthesizes text to speech and plays it, blocking until playback finishes.
func (s *Session) Say(ctx context.Context, text string) error {
	if s.tts == nil {
		return ErrNoSynthesizer
	}
	src, err := s.tts.Synthesize(ctx, text)
	if err != nil {
		return err
	}
	return s.PlayAndWait(ctx, src)
}

// SayInterruptible synthesizes and plays text while listening for the caller to start
// speaking (barge-in). If the caller speaks (per the VAD), playback stops immediately
// and it returns interrupted=true; otherwise it returns when playback finishes. Drain
// the caller's reply with Listen afterwards.
func (s *Session) SayInterruptible(ctx context.Context, text string) (interrupted bool, err error) {
	if s.tts == nil {
		return false, ErrNoSynthesizer
	}
	src, err := s.tts.Synthesize(ctx, text)
	if err != nil {
		return false, err
	}
	done := make(chan struct{})
	var once sync.Once
	p := calls.NewPlayer()
	p.OnFinish(func() { once.Do(func() { close(done) }) })
	s.Call.Subscribe(p)
	s.drain()
	p.Play(src)

	vad := s.newVAD()
	// Require a couple of consecutive voiced frames so a single noise blip does not
	// abort the prompt.
	const bargeInFrames = 2
	voiced := 0
	for {
		select {
		case <-done:
			return false, nil
		case <-s.ended:
			p.Stop()
			return false, ErrCallEnded
		case <-ctx.Done():
			p.Stop()
			return false, ctx.Err()
		case frame := <-s.tap.ch:
			if vad.Voiced(frame) {
				voiced++
				if voiced >= bargeInFrames {
					p.Stop()
					return true, nil
				}
			} else {
				voiced = 0
			}
		}
	}
}

// RecordOptions configures Record.
type RecordOptions struct {
	// MaxDuration caps the recording length. Default 10s.
	MaxDuration time.Duration
}

// Record captures the caller's decoded audio for a fixed duration and returns it as
// 16 kHz mono PCM. Use Listen for VAD-bounded utterance capture instead.
func (s *Session) Record(ctx context.Context, opts RecordOptions) ([]float32, error) {
	maxDur := opts.MaxDuration
	if maxDur <= 0 {
		maxDur = 10 * time.Second
	}
	frames := int(maxDur/frameDuration) + 1
	s.drain()
	out := make([]float32, 0, frames*calls.FrameSamples)
	for i := 0; i < frames; i++ {
		select {
		case f := <-s.tap.ch:
			out = append(out, f...)
		case <-s.ended:
			return out, ErrCallEnded
		case <-ctx.Done():
			return out, ctx.Err()
		}
	}
	return out, nil
}

// ListenOptions configures Listen.
type ListenOptions struct {
	// StartTimeout is how long to wait for the caller to begin speaking before
	// returning ErrNoSpeech. Default 5s.
	StartTimeout time.Duration
	// SilenceTimeout is how much trailing silence ends the utterance. Default 800ms.
	SilenceTimeout time.Duration
	// MaxDuration caps the whole utterance. Default 20s.
	MaxDuration time.Duration
}

func (o ListenOptions) withDefaults() ListenOptions {
	if o.StartTimeout <= 0 {
		o.StartTimeout = 5 * time.Second
	}
	if o.SilenceTimeout <= 0 {
		o.SilenceTimeout = 800 * time.Millisecond
	}
	if o.MaxDuration <= 0 {
		o.MaxDuration = 20 * time.Second
	}
	return o
}

// Listen records one caller utterance: it waits for speech to begin (per the VAD), then
// captures until the caller falls silent for SilenceTimeout (or MaxDuration is hit),
// and returns the captured 16 kHz mono PCM. Returns ErrNoSpeech if the caller never
// speaks within StartTimeout.
func (s *Session) Listen(ctx context.Context, opts ListenOptions) ([]float32, error) {
	opts = opts.withDefaults()
	vad := s.newVAD()
	startFrames := int(opts.StartTimeout / frameDuration)
	silenceFrames := int(opts.SilenceTimeout/frameDuration) + 1
	maxFrames := int(opts.MaxDuration/frameDuration) + 1

	s.drain()
	out := make([]float32, 0, maxFrames*calls.FrameSamples)
	speechStarted := false
	waited := 0
	silence := 0
	for total := 0; total < maxFrames; {
		var frame []float32
		select {
		case frame = <-s.tap.ch:
		case <-s.ended:
			return out, ErrCallEnded
		case <-ctx.Done():
			return out, ctx.Err()
		}
		voiced := vad.Voiced(frame)
		if !speechStarted {
			if voiced {
				speechStarted = true
				out = append(out, frame...)
				total++
			} else {
				waited++
				if waited >= startFrames {
					return nil, ErrNoSpeech
				}
			}
			continue
		}
		out = append(out, frame...)
		total++
		if voiced {
			silence = 0
		} else {
			silence++
			if silence >= silenceFrames {
				break
			}
		}
	}
	return out, nil
}

// ListenText records one caller utterance with Listen and transcribes it to text.
func (s *Session) ListenText(ctx context.Context, opts ListenOptions) (string, error) {
	if s.stt == nil {
		return "", ErrNoTranscriber
	}
	pcm, err := s.Listen(ctx, opts)
	if err != nil {
		return "", err
	}
	return s.stt.Transcribe(ctx, pcm, calls.SampleRate)
}

// frameTap is the per-session inbound audio sink: it copies each decoded frame onto a
// buffered channel that the Listen/Record/barge-in loops read. When the consumer falls
// behind it drops the oldest frame to keep latency bounded.
type frameTap struct {
	ch chan []float32
}

func newFrameTap(buffer int) *frameTap {
	return &frameTap{ch: make(chan []float32, buffer)}
}

// WriteFrame copies frame onto the channel (the engine reuses its buffer after return).
func (t *frameTap) WriteFrame(frame []float32) error {
	cp := make([]float32, len(frame))
	copy(cp, frame)
	select {
	case t.ch <- cp:
	default:
		select { // make room by dropping the oldest, then enqueue the newest
		case <-t.ch:
		default:
		}
		select {
		case t.ch <- cp:
		default:
		}
	}
	return nil
}

// Close is a no-op; the channel is GC'd with the session.
func (t *frameTap) Close() error { return nil }

// drain discards any buffered inbound frames so the next capture starts fresh.
func (s *Session) drain() {
	for {
		select {
		case <-s.tap.ch:
		default:
			return
		}
	}
}
