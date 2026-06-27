// Command ivr logs in and runs a voice-menu (IVR) on inbound calls. WhatsApp calls have
// no DTMF, so the menu is voice-driven: it speaks a prompt, listens, transcribes, and
// branches on keywords.
//
// The Synthesizer/Transcriber here are dependency-free stubs so the example compiles and
// runs; swap exampleutil.StubSynthesizer / StubTranscriber for real TTS/STT providers.
//
//	go run .
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.mau.fi/whatsmeow/calls"
	"go.mau.fi/whatsmeow/calls/examples/exampleutil"
	"go.mau.fi/whatsmeow/calls/voiceagent"
)

func main() {
	ctx := context.Background()
	wa, err := exampleutil.NewClient(ctx, "ivr.db")
	if err != nil {
		panic(err)
	}
	callClient := calls.NewClient(wa)

	flow := voiceagent.NewIVR("main",
		voiceagent.IVRNode{
			ID:     "main",
			Prompt: "Welcome. Say sales, support, or goodbye.",
			Options: []voiceagent.IVROption{
				{Keywords: []string{"sales", "buy"}, Next: "sales"},
				{Keywords: []string{"support", "help"}, Next: "support"},
				{Keywords: []string{"bye", "goodbye"}, Next: "bye"},
				{Next: "main"}, // catch-all: repeat the menu
			},
			Reprompt: "Sorry, I did not catch that. Say sales, support, or goodbye.",
		},
		voiceagent.IVRNode{ID: "sales", Prompt: "Connecting you to sales. Goodbye.", Terminal: true},
		voiceagent.IVRNode{ID: "support", Prompt: "Connecting you to support. Goodbye.", Terminal: true},
		voiceagent.IVRNode{ID: "bye", Prompt: "Thanks for calling. Goodbye.", Terminal: true},
	)

	bot := voiceagent.NewBot(callClient, voiceagent.BotConfig{
		SessionOptions: []voiceagent.SessionOption{
			voiceagent.WithSynthesizer(exampleutil.StubSynthesizer()),
			// Rotates through these "spoken" replies so the menu advances.
			voiceagent.WithTranscriber(exampleutil.StubTranscriber("sales", "goodbye")),
		},
		Script: func(ctx context.Context, s *voiceagent.Session) error {
			return flow.Run(ctx, s)
		},
	})
	bot.Start()

	if err := exampleutil.Login(ctx, wa); err != nil {
		panic(err)
	}
	fmt.Println("IVR running; call this account. Ctrl+C to quit.")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	wa.Disconnect()
}
