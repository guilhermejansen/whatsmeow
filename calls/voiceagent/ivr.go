package voiceagent

import (
	"context"
	"errors"
	"strings"

	"github.com/rs/zerolog"

	"go.mau.fi/whatsmeow/calls"
)

// IVRAction runs when an option is matched, before moving to the next node. Optional.
type IVRAction func(ctx context.Context, s *Session) error

// IVROption is one branch of an [IVRNode]: when the caller's transcribed reply matches
// any Keyword (case-insensitive substring), the optional Action runs and the flow moves
// to Next. An option with no Keywords is a catch-all/default (place it last).
type IVROption struct {
	Keywords []string
	Next     string
	Action   IVRAction
}

// IVRNode is one menu step: speak a prompt, listen for the caller, match an [IVROption].
type IVRNode struct {
	// ID identifies the node within the flow.
	ID string
	// Prompt is spoken via the Session's Synthesizer. Ignored if PromptSource is set.
	Prompt string
	// PromptSource plays a pre-recorded prompt instead of synthesizing Prompt.
	PromptSource func(ctx context.Context, s *Session) (calls.AudioSource, error)
	// Options are the branches, tried in order. The first whose keyword matches wins.
	Options []IVROption
	// Reprompt is spoken when nothing matches (or no speech), before retrying. If empty
	// the Prompt is repeated.
	Reprompt string
	// MaxRetries is how many times to re-listen on no-match/no-speech. Default 3.
	MaxRetries int
	// Listen tunes utterance capture for this node.
	Listen ListenOptions
	// Terminal ends the flow after the prompt (no listening). Use for a final message.
	Terminal bool
}

// IVR is a voice-driven menu (no DTMF — WhatsApp calls carry no touch tones). It plays
// a prompt, captures the caller's spoken reply, transcribes it, and branches on matched
// keywords. It needs a Session configured with both a Synthesizer (for prompts) and a
// Transcriber (for replies); pre-recorded prompts (PromptSource) avoid the Synthesizer.
type IVR struct {
	nodes map[string]*IVRNode
	start string
	log   zerolog.Logger
}

// ErrIVRNodeNotFound is returned by Run when a node references an unknown Next id.
var ErrIVRNodeNotFound = errors.New("voiceagent: IVR node not found")

// NewIVR builds an IVR flow starting at startID from the given nodes.
func NewIVR(startID string, nodes ...IVRNode) *IVR {
	m := make(map[string]*IVRNode, len(nodes))
	for i := range nodes {
		n := nodes[i]
		m[n.ID] = &n
	}
	return &IVR{nodes: m, start: startID}
}

// WithLogger sets the IVR logger.
func (f *IVR) WithLogger(l zerolog.Logger) *IVR {
	f.log = l
	return f
}

// Run walks the flow from the start node until a terminal node, an unmatched node that
// exhausts its retries, the call ending, or ctx being cancelled.
func (f *IVR) Run(ctx context.Context, s *Session) error {
	cur := f.start
	for cur != "" {
		node := f.nodes[cur]
		if node == nil {
			return ErrIVRNodeNotFound
		}
		if err := f.playPrompt(ctx, s, node); err != nil {
			return err
		}
		if node.Terminal {
			return nil
		}
		next, err := f.collect(ctx, s, node)
		if err != nil {
			return err
		}
		cur = next
	}
	return nil
}

func (f *IVR) playPrompt(ctx context.Context, s *Session, node *IVRNode) error {
	if node.PromptSource != nil {
		src, err := node.PromptSource(ctx, s)
		if err != nil {
			return err
		}
		return s.PlayAndWait(ctx, src)
	}
	if node.Prompt != "" {
		return s.Say(ctx, node.Prompt)
	}
	return nil
}

// collect listens, matches an option, and returns the next node id (empty to end).
func (f *IVR) collect(ctx context.Context, s *Session, node *IVRNode) (string, error) {
	retries := node.MaxRetries
	if retries <= 0 {
		retries = 3
	}
	for attempt := 0; attempt < retries; attempt++ {
		text, err := s.ListenText(ctx, node.Listen)
		switch {
		case errors.Is(err, ErrNoSpeech):
			f.log.Debug().Str("node", node.ID).Msg("voiceagent: IVR heard no speech")
		case errors.Is(err, ErrCallEnded):
			return "", ErrCallEnded
		case err != nil:
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			f.log.Warn().Err(err).Str("node", node.ID).Msg("voiceagent: IVR transcription failed")
		default:
			if opt := matchOption(node.Options, text); opt != nil {
				f.log.Debug().Str("node", node.ID).Str("heard", text).Str("next", opt.Next).Msg("voiceagent: IVR matched")
				if opt.Action != nil {
					if aerr := opt.Action(ctx, s); aerr != nil {
						return "", aerr
					}
				}
				return opt.Next, nil
			}
			f.log.Debug().Str("node", node.ID).Str("heard", text).Msg("voiceagent: IVR no keyword match")
		}
		// Re-prompt before the next attempt.
		reprompt := node.Reprompt
		if reprompt == "" {
			reprompt = node.Prompt
		}
		if reprompt != "" && node.PromptSource == nil {
			if serr := s.Say(ctx, reprompt); serr != nil {
				return "", serr
			}
		} else if node.PromptSource != nil {
			if perr := f.playPrompt(ctx, s, node); perr != nil {
				return "", perr
			}
		}
	}
	return "", nil // retries exhausted → end the flow
}

// matchOption returns the first option whose keyword is a case-insensitive substring of
// the transcript. An option with no keywords is a catch-all and matches anything.
func matchOption(options []IVROption, transcript string) *IVROption {
	lc := strings.ToLower(strings.TrimSpace(transcript))
	for i := range options {
		opt := &options[i]
		if len(opt.Keywords) == 0 {
			return opt
		}
		for _, kw := range opt.Keywords {
			kw = strings.ToLower(strings.TrimSpace(kw))
			if kw != "" && strings.Contains(lc, kw) {
				return opt
			}
		}
	}
	return nil
}
