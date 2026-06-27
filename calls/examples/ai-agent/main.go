// Command ai-agent logs in and runs a real-time AI voice agent on inbound calls:
// listen → transcribe → LLM → speak, looping with optional barge-in.
//
// The STT/LLM/TTS here are dependency-free stubs so the example compiles and runs; swap
// exampleutil.StubTranscriber/StubResponder/StubSynthesizer for real providers (e.g.
// Whisper/Groq for STT, GPT/Claude/Gemini for the LLM, OpenAI/Azure for TTS).
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
	wa, err := exampleutil.NewClient(ctx, "ai-agent.db")
	if err != nil {
		panic(err)
	}
	callClient := calls.NewClient(wa)

	agent, err := voiceagent.NewAgent(voiceagent.AgentConfig{
		Responder: exampleutil.StubResponder(), // replace with a real LLM
		System:    "You are a helpful WhatsApp voice assistant. Keep replies short.",
		Greeting:  "Hi! How can I help you today?",
		MaxTurns:  10,
		BargeIn:   true,
	})
	if err != nil {
		panic(err)
	}

	bot := voiceagent.NewBot(callClient, voiceagent.BotConfig{
		SessionOptions: []voiceagent.SessionOption{
			voiceagent.WithSynthesizer(exampleutil.StubSynthesizer()),
			voiceagent.WithTranscriber(exampleutil.StubTranscriber("what are your hours", "thanks, goodbye")),
		},
		Script: func(ctx context.Context, s *voiceagent.Session) error {
			return agent.Run(ctx, s)
		},
	})
	bot.Start()

	if err := exampleutil.Login(ctx, wa); err != nil {
		panic(err)
	}
	fmt.Println("AI agent running; call this account. Ctrl+C to quit.")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	wa.Disconnect()
}
