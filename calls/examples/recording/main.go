// Command recording logs in, auto-answers inbound calls, and records the caller's audio
// to a timestamped WAV file (a voicemail / secretary bot). It plays a short spoken
// prompt first if a Synthesizer is wired (here a silent stub keeps the example
// dependency-free).
//
//	go run .
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"go.mau.fi/whatsmeow/calls"
	"go.mau.fi/whatsmeow/calls/examples/exampleutil"
	"go.mau.fi/whatsmeow/calls/voiceagent"
)

var counter atomic.Int64

func main() {
	ctx := context.Background()
	wa, err := exampleutil.NewClient(ctx, "recording.db")
	if err != nil {
		panic(err)
	}
	callClient := calls.NewClient(wa)

	bot := voiceagent.NewBot(callClient, voiceagent.BotConfig{
		Script: func(ctx context.Context, s *voiceagent.Session) error {
			// Record up to 30s of the caller to a WAV file.
			path := fmt.Sprintf("recording-%d.wav", counter.Add(1))
			rec, err := calls.WAVRecorder(path)
			if err != nil {
				return err
			}
			defer rec.Close()
			s.Call.Receive(rec)
			fmt.Println("recording caller to", path)
			select {
			case <-time.After(30 * time.Second):
			case <-s.Ended():
			case <-ctx.Done():
			}
			return nil
		},
	})
	bot.Start()

	if err := exampleutil.Login(ctx, wa); err != nil {
		panic(err)
	}
	fmt.Println("recording bot running; call this account. Ctrl+C to quit.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	wa.Disconnect()
}
