package calls

// video_fork.go — fork-owned overlay (NOT from purpshell/meowcaller).
//
// This file holds the whatsmeow-private fork's OUTBOUND-video additions on top of the
// vendored meowcaller engine: placing a video call (CallVideo/placeCallVideo) and
// signaling our own camera state to the peer (SetVideoState/sendVideoState). They are
// pure additions (new methods on existing meowcaller types), so keeping them in a
// separate file lets scripts/sync-meowcaller.sh regenerate calls/ from upstream without
// wiping them — the sync script preserves this file across the rm -rf regeneration
// (see FORK_FILES in scripts/sync-meowcaller.sh). The one in-file divergence the sync
// cannot express this way — the accept-video toggle in engine.go sendAccept — is
// re-applied by the sync script as a patch instead.
//
// NOT VALIDATED: the WhatsApp server's handling of these outbound-video stanzas is
// unproven end-to-end (meowcaller's video support is "initial/experimental"). The RTP
// egress is real, but the peer-side bridge has never been confirmed with a captured
// vector. Do not remove the NOT VALIDATED markers until a live capture proves the path.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow/calls/signaling"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// CallVideo places a 1:1 video call to target. It is identical to Call but advertises
// H.264 video capability in the offer so the peer sees a video call.
func (c *Client) CallVideo(ctx context.Context, target string) (*Call, error) {
	return c.eng.placeCallVideo(ctx, target)
}

// SetVideoState signals our own camera state to the peer via a standalone <video> stanza —
// active=true when the local camera turns on, false when it turns off. orientation is our
// device orientation (0..3, rotate by ×90°). This is the outbound analog of OnVideoState:
// without it, the peer never learns our camera came up and keeps rendering us audio-only.
// It reuses the exact BuildVideoState path the mid-call upgrade-accept already uses.
//
// NOT VALIDATED: the outbound <video> state signaling path is unproven.
func (c *Call) SetVideoState(active bool, orientation int) error {
	return c.eng.sendVideoState(c.id, active, orientation)
}

// sendVideoState emits a standalone <video> state stanza announcing our own camera
// on/off + orientation to the peer. It mirrors the upgrade-accept path in onVideoStanza
// (same BuildVideoState, same from/creator, same SendNode), so it is well-formed for both
// call directions (from/creator are populated on placeCall and onOffer alike).
//
// NOT VALIDATED: no captured outbound <video> state vector confirms server acceptance.
func (e *engine) sendVideoState(callID string, active bool, orientation int) error {
	m := e.lookup(callID)
	if m == nil {
		return errors.New("meowcaller: unknown call")
	}
	state := signaling.VideoStateActive
	if !active {
		// state 0 = camera off; the peer stops rendering our video.
		state = 0
	}
	node := signaling.BuildVideoState(callID, m.from, m.creator,
		e.c.wa.GenerateMessageID(), state, orientation, signaling.VideoCodecH264)
	return e.c.wa.DangerousInternals().SendNode(context.Background(), node)
}

// placeCallVideo is identical to placeCall but advertises H.264 video in the offer.
func (e *engine) placeCallVideo(ctx context.Context, target string) (*Call, error) {
	cli := e.c.wa
	self := cli.Store.GetLID()
	if self.IsEmpty() {
		return nil, errors.New("meowcaller: no own LID on this session")
	}
	peerLID, err := resolvePeerLID(ctx, cli, target)
	if err != nil {
		return nil, err
	}
	e.c.log.Info().Str("peer_lid", peerLID.String()).Str("self_lid", self.String()).Msg("resolved peer LID for video call")

	devices, err := cli.GetUserDevices(ctx, []types.JID{peerLID})
	if err != nil {
		return nil, fmt.Errorf("device discovery: %w", err)
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("peer %s has no devices (unreachable / not on WhatsApp)", peerLID)
	}

	var callKey [32]byte
	if _, err := rand.Read(callKey[:]); err != nil {
		return nil, err
	}
	deviceKeys := make([]signaling.OfferDeviceKey, 0, len(devices))
	needIdentity := false
	for _, dev := range devices {
		ct, encType, ni, err := encryptCallKeyForDevice(ctx, cli, dev, callKey[:])
		if err != nil {
			return nil, fmt.Errorf("encrypt callKey for %s: %w", dev, err)
		}
		needIdentity = needIdentity || ni
		deviceKeys = append(deviceKeys, signaling.OfferDeviceKey{DeviceJid: dev, Ciphertext: ct, EncType: encType})
	}

	var deviceIdentity []byte
	if needIdentity {
		deviceIdentity, err = proto.Marshal(cli.Store.Account)
		if err != nil {
			return nil, fmt.Errorf("marshal device identity: %w", err)
		}
	}

	var privacyToken []byte
	if pt, err := cli.Store.PrivacyTokens.GetPrivacyToken(ctx, peerLID); err == nil && pt != nil {
		privacyToken = pt.Token
	}

	callID := newCallID()
	offer := signaling.BuildOffer(&signaling.OfferParams{
		CallID:         callID,
		To:             peerLID,
		CallCreator:    self,
		DeviceKeys:     deviceKeys,
		PrivacyToken:   privacyToken,
		Capability:     signaling.CapabilityOffer,
		DeviceIdentity: deviceIdentity,
		Video:          true,
	})
	offer.Attrs["id"] = cli.GenerateMessageID()

	call := &Call{eng: e, id: callID, peer: peerLID, phase: CallPhaseCalling}

	e.mu.Lock()
	m := e.entry(callID)
	m.call = call
	m.callKey = callKey[:]
	m.isVideo = true
	m.selfLID = self.String()
	m.peerLID = peerLID.String()
	m.creator = self
	m.from = peerLID
	m.direction = CallDirectionOutgoing
	e.mu.Unlock()

	e.c.diag.Emit("keying", map[string]any{
		"call_id": callID, "direction": "out", "self_lid": self.String(),
		"peer_lid": peerLID.String(), "device_count": len(deviceKeys),
		"call_key_hex": hex.EncodeToString(callKey[:]), "video": true,
	})

	if err := cli.DangerousInternals().SendNode(ctx, offer); err != nil {
		return nil, fmt.Errorf("send video offer: %w", err)
	}
	e.c.log.Info().Str("call_id", callID).Msg("video offer sent; media starts when the relay endpoint arrives")
	e.c.diag.Emit("meta", map[string]any{"event": "video_offer_sent", "call_id": callID, "peer_lid": peerLID.String(), "direction": "out"})
	return call, nil
}
