package voiceagent

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
)

// AgentConfig configures a conversational AI [Agent].
type AgentConfig struct {
	// Responder is the LLM that produces replies. Required.
	Responder Responder
	// System is the system prompt seeded as the first turn. Optional.
	System string
	// Greeting is spoken first, before listening, and recorded as the opening
	// assistant turn. Optional.
	Greeting string
	// MaxTurns caps the number of user→assistant exchanges. 0 = until the call ends.
	MaxTurns int
	// BargeIn lets the caller interrupt the assistant's speech. Default false.
	BargeIn bool
	// MaxSilentTurns ends the call after this many consecutive no-speech turns.
	// Default 3.
	MaxSilentTurns int
	// Listen tunes utterance capture.
	Listen ListenOptions
	// HangupOnEnd hangs up when the loop finishes (MaxTurns reached / silence). Default
	// true.
	HangupOnEnd *bool
	// Logger receives agent logs.
	Logger zerolog.Logger
}

// Agent runs a real-time voice conversation over a call: it listens for the caller,
// transcribes (Session Transcriber), asks the LLM ([Responder]) for a reply, and speaks
// it (Session Synthesizer), looping until the call ends or MaxTurns is reached. With
// BargeIn the caller can talk over the assistant. The Session must be configured with
// both a Transcriber and a Synthesizer.
type Agent struct {
	responder      Responder
	system         string
	greeting       string
	maxTurns       int
	bargeIn        bool
	maxSilentTurns int
	listen         ListenOptions
	hangupOnEnd    bool
	log            zerolog.Logger
}

// ErrNoResponder is returned by NewAgent when no Responder is supplied.
var ErrNoResponder = errors.New("voiceagent: no responder configured")

// NewAgent creates a conversational Agent. It returns ErrNoResponder if cfg.Responder
// is nil.
func NewAgent(cfg AgentConfig) (*Agent, error) {
	if cfg.Responder == nil {
		return nil, ErrNoResponder
	}
	maxSilent := cfg.MaxSilentTurns
	if maxSilent <= 0 {
		maxSilent = 3
	}
	hangup := true
	if cfg.HangupOnEnd != nil {
		hangup = *cfg.HangupOnEnd
	}
	return &Agent{
		responder:      cfg.Responder,
		system:         cfg.System,
		greeting:       cfg.Greeting,
		maxTurns:       cfg.MaxTurns,
		bargeIn:        cfg.BargeIn,
		maxSilentTurns: maxSilent,
		listen:         cfg.Listen,
		hangupOnEnd:    hangup,
		log:            cfg.Logger,
	}, nil
}

// Run drives the conversation on s until the call ends, MaxTurns is reached, the caller
// goes silent for MaxSilentTurns, or ctx is cancelled.
func (a *Agent) Run(ctx context.Context, s *Session) error {
	if s.tts == nil {
		return ErrNoSynthesizer
	}
	if s.stt == nil {
		return ErrNoTranscriber
	}
	if err := s.WaitUntilReady(ctx); err != nil {
		return err
	}

	history := make([]Turn, 0, 16)
	if a.system != "" {
		history = append(history, Turn{Role: RoleSystem, Text: a.system})
	}
	if a.greeting != "" {
		history = append(history, Turn{Role: RoleAssistant, Text: a.greeting})
		if err := a.speak(ctx, s, a.greeting); err != nil {
			return a.finish(ctx, s, err)
		}
	}

	silent := 0
	for turn := 0; a.maxTurns == 0 || turn < a.maxTurns; turn++ {
		text, err := s.ListenText(ctx, a.listen)
		switch {
		case errors.Is(err, ErrNoSpeech):
			silent++
			a.log.Debug().Int("consecutive_silent", silent).Msg("voiceagent: agent heard no speech")
			if silent >= a.maxSilentTurns {
				return a.finish(ctx, s, nil)
			}
			continue
		case errors.Is(err, ErrCallEnded):
			return ErrCallEnded
		case err != nil:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			a.log.Warn().Err(err).Msg("voiceagent: agent transcription failed")
			continue
		}
		silent = 0
		history = append(history, Turn{Role: RoleUser, Text: text})

		reply, err := a.responder.Respond(ctx, history)
		if err != nil {
			a.log.Warn().Err(err).Msg("voiceagent: responder failed")
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		history = append(history, Turn{Role: RoleAssistant, Text: reply})
		if err := a.speak(ctx, s, reply); err != nil {
			return a.finish(ctx, s, err)
		}
	}
	return a.finish(ctx, s, nil)
}

// speak says text, honoring barge-in when enabled.
func (a *Agent) speak(ctx context.Context, s *Session, text string) error {
	if a.bargeIn {
		_, err := s.SayInterruptible(ctx, text)
		return err
	}
	return s.Say(ctx, text)
}

// finish optionally hangs up and normalizes the terminal error.
func (a *Agent) finish(ctx context.Context, s *Session, cause error) error {
	if a.hangupOnEnd && s.EndReason() == "" {
		_ = s.Hangup()
	}
	if errors.Is(cause, ErrCallEnded) {
		return nil
	}
	return cause
}
