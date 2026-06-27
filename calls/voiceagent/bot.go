package voiceagent

import (
	"context"

	"github.com/rs/zerolog"

	"go.mau.fi/whatsmeow/calls"
)

// Script drives one answered call. It runs after media is flowing; return when done
// (the Bot then hangs up, unless AutoHangup is disabled). Use the Session helpers
// (Say, Listen, PlayAndWait, Record) to interact with the caller.
type Script func(ctx context.Context, s *Session) error

// BotConfig configures a Bot.
type BotConfig struct {
	// Script is the per-call behaviour. Required.
	Script Script
	// AutoAnswer answers inbound calls automatically. Default true. When false, the
	// Script must call Session.Call.Answer itself (e.g. after screening).
	AutoAnswer *bool
	// AutoHangup hangs up after the Script returns. Default true.
	AutoHangup *bool
	// SessionOptions configure each call's Session (WithSynthesizer/WithTranscriber/
	// WithVAD/WithLogger).
	SessionOptions []SessionOption
	// Context is the base context for call scripts; it is cancelled when the call ends.
	// Defaults to context.Background().
	Context context.Context
	// Logger receives Bot-level logs.
	Logger zerolog.Logger
}

// Bot auto-answers inbound calls and runs a fixed, deterministic [Script] on each — the
// "press play" use case: greet, play prompts, optionally record, hang up. For menus use
// [IVR]; for conversational AI use [Agent]. The Script can, of course, embed an IVR or
// Agent run.
type Bot struct {
	client     *calls.Client
	script     Script
	autoAnswer bool
	autoHangup bool
	sessOpts   []SessionOption
	baseCtx    context.Context
	log        zerolog.Logger
}

// NewBot creates a Bot bound to a calls.Client. Call Start to begin handling inbound
// calls.
func NewBot(client *calls.Client, cfg BotConfig) *Bot {
	answer := true
	if cfg.AutoAnswer != nil {
		answer = *cfg.AutoAnswer
	}
	hangup := true
	if cfg.AutoHangup != nil {
		hangup = *cfg.AutoHangup
	}
	baseCtx := cfg.Context
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	return &Bot{
		client:     client,
		script:     cfg.Script,
		autoAnswer: answer,
		autoHangup: hangup,
		sessOpts:   cfg.SessionOptions,
		baseCtx:    baseCtx,
		log:        cfg.Logger,
	}
}

// Start registers the inbound-call handler on the underlying calls.Client. It replaces
// any previously registered OnIncomingCall handler. Each inbound call is answered (if
// AutoAnswer) and its Script runs on its own goroutine.
func (b *Bot) Start() {
	b.client.OnIncomingCall(func(call *calls.Call) {
		s := NewSession(call, b.sessOpts...)
		if b.autoAnswer {
			if err := call.Answer(); err != nil {
				b.log.Error().Err(err).Str("call_id", call.ID()).Msg("voiceagent: answer failed")
				return
			}
		}
		go b.run(call, s)
	})
}

func (b *Bot) run(call *calls.Call, s *Session) {
	ctx, cancel := context.WithCancel(b.baseCtx)
	defer cancel()
	// Cancel the script context when the call ends so blocked helpers unwind promptly.
	go func() {
		select {
		case <-s.Ended():
			cancel()
		case <-ctx.Done():
		}
	}()

	if err := s.WaitUntilReady(ctx); err != nil {
		b.log.Warn().Err(err).Str("call_id", call.ID()).Msg("voiceagent: call did not become ready")
		return
	}
	if b.script != nil {
		if err := b.script(ctx, s); err != nil {
			b.log.Warn().Err(err).Str("call_id", call.ID()).Msg("voiceagent: script ended with error")
		}
	}
	if b.autoHangup && s.EndReason() == "" {
		if err := call.Hangup(); err != nil {
			b.log.Debug().Err(err).Str("call_id", call.ID()).Msg("voiceagent: hangup failed")
		}
	}
}

// PlayScript builds a Script that plays each source in order, then returns (the Bot
// hangs up afterwards if AutoHangup is on). build is called per call to produce the
// sources, so it can pick a recording or synthesize fresh audio each time.
func PlayScript(build func(ctx context.Context, s *Session) ([]calls.AudioSource, error)) Script {
	return func(ctx context.Context, s *Session) error {
		sources, err := build(ctx, s)
		if err != nil {
			return err
		}
		for _, src := range sources {
			if err := s.PlayAndWait(ctx, src); err != nil {
				return err
			}
		}
		return nil
	}
}

// SayScript builds a Script that speaks each line in order via the Session's
// Synthesizer. Requires WithSynthesizer.
func SayScript(lines ...string) Script {
	return func(ctx context.Context, s *Session) error {
		for _, line := range lines {
			if err := s.Say(ctx, line); err != nil {
				return err
			}
		}
		return nil
	}
}
