// Command outbound-tts places an outbound call to a number and, once answered, speaks a
// message via TTS, then hangs up — the "voice notification / OTP by voice" use case.
//
// The Synthesizer here is a dependency-free stub (silence); swap exampleutil.StubSynthesizer
// for a real TTS provider that returns 16 kHz mono PCM.
//
//	go run . <number-or-jid> "your spoken message"
//
// number-or-jid: a phone number (digits, e.g. 15551234567), a phone JID
// (15551234567@s.whatsapp.net), or a LID (123@lid).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.mau.fi/whatsmeow/calls"
	"go.mau.fi/whatsmeow/calls/examples/exampleutil"
	"go.mau.fi/whatsmeow/calls/voiceagent"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println(`usage: outbound-tts <number-or-jid> "message"`)
		os.Exit(1)
	}
	target, message := os.Args[1], os.Args[2]
	ctx := context.Background()

	wa, err := exampleutil.NewClient(ctx, "outbound-tts.db")
	if err != nil {
		panic(err)
	}
	callClient := calls.NewClient(wa)
	if err := exampleutil.Login(ctx, wa); err != nil {
		panic(err)
	}
	// Give the connection a moment to settle after login.
	time.Sleep(2 * time.Second)

	call, err := callClient.Call(ctx, target)
	if err != nil {
		panic(err)
	}
	fmt.Println("calling", target, "call-id", call.ID())

	sess := voiceagent.NewSession(call, voiceagent.WithSynthesizer(exampleutil.StubSynthesizer()))
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if err := sess.WaitUntilReady(callCtx); err != nil {
		fmt.Println("call not answered:", err)
		_ = call.Hangup()
		return
	}
	if err := sess.Say(callCtx, message); err != nil {
		fmt.Println("say failed:", err)
	}
	_ = call.Hangup()
	wa.Disconnect()
}
