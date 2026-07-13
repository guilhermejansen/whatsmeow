// Fork-local additions to the message-secret layer.
//
// This file intentionally lives OUTSIDE msgsecret.go so that merging upstream
// whatsmeow never conflicts with it (same package, so the unexported
// decryptMsgSecret primitive is still reachable).
//
// Upstream ships DecryptReaction / DecryptComment / DecryptPollVote /
// DecryptSecretEncryptedMessage, but no decryptor for calendar-event RSVPs,
// even though the EncSecretEventResponse use-case label already exists in
// msgsecret.go. DecryptEventResponse fills that gap using the exact same
// primitive — no duplicated crypto, no reimplemented PN<->LID sender
// resolution.

package whatsmeow

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
)

// ErrNotEventResponseMessage is returned by DecryptEventResponse when the given
// message doesn't carry an EncEventResponseMessage.
var ErrNotEventResponseMessage = errors.New("given message isn't an encrypted event response message")

// DecryptEventResponse decrypts an RSVP to a WhatsApp calendar event.
//
// Responses to an event message are encrypted with the message-secret of the
// event-creation message (the same scheme as reactions, comments and poll
// votes), so the original event must have been received while this device was
// linked — otherwise the secret is unknown and this returns
// ErrOriginalMessageSecretNotFound.
//
//	if evt.Message.GetEncEventResponseMessage() != nil {
//		resp, err := cli.DecryptEventResponse(ctx, evt)
//		if err != nil {
//			fmt.Println(":(", err)
//			return
//		}
//		// resp.GetResponse() is GOING / NOT_GOING / MAYBE
//		fmt.Printf("%s responded %s (+%d guests)\n",
//			evt.Info.Sender, resp.GetResponse(), resp.GetExtraGuestCount())
//	}
func (cli *Client) DecryptEventResponse(ctx context.Context, evt *events.Message) (*waE2E.EventResponseMessage, error) {
	if evt == nil || evt.Message == nil {
		return nil, ErrNotEventResponseMessage
	}
	encResp := evt.Message.GetEncEventResponseMessage()
	if encResp == nil {
		return nil, ErrNotEventResponseMessage
	}
	plaintext, err := cli.decryptMsgSecret(ctx, evt, EncSecretEventResponse, encResp, encResp.GetEventCreationMessageKey())
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt event response: %w", err)
	}
	var msg waE2E.EventResponseMessage
	if err = proto.Unmarshal(plaintext, &msg); err != nil {
		return nil, fmt.Errorf("failed to decode event response protobuf: %w", err)
	}
	return &msg, nil
}
