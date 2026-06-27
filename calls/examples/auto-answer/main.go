// Command auto-answer logs in, then auto-answers every inbound call and plays a
// greeting audio file (.wav/.mp3/.opus) to the caller before hanging up.
//
//	go run . path/to/greeting.wav
//
// This is the simplest "bot" use case: a fixed, deterministic script.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"go.mau.fi/whatsmeow/calls"
	"go.mau.fi/whatsmeow/calls/examples/exampleutil"
	"go.mau.fi/whatsmeow/calls/voiceagent"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: auto-answer <greeting.wav|.mp3|.opus>")
		os.Exit(1)
	}
	greeting := os.Args[1]
	ctx := context.Background()

	wa, err := exampleutil.NewClient(ctx, "auto-answer.db")
	if err != nil {
		panic(err)
	}
	// Construct the calls client BEFORE logging in so the call-node hook is installed.
	callClient := calls.NewClient(wa)

	bot := voiceagent.NewBot(callClient, voiceagent.BotConfig{
		Script: voiceagent.PlayScript(func(_ context.Context, _ *voiceagent.Session) ([]calls.AudioSource, error) {
			src, err := openAudio(greeting)
			if err != nil {
				return nil, err
			}
			return []calls.AudioSource{src}, nil
		}),
	})
	bot.Start()

	if err := exampleutil.Login(ctx, wa); err != nil {
		panic(err)
	}
	fmt.Println("auto-answer running; call this account from another phone. Ctrl+C to quit.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	wa.Disconnect()
}

// openAudio decodes a .wav/.mp3/.opus file into a call AudioSource.
func openAudio(path string) (calls.AudioSource, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav":
		return calls.WAVFile(path)
	case ".mp3":
		return calls.MP3File(path)
	case ".opus", ".ogg":
		return calls.OpusFile(path)
	default:
		return nil, fmt.Errorf("unsupported audio format: %s", path)
	}
}
