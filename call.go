// Copyright (c) 2021 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package whatsmeow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// CallNodeHandler is invoked for raw <call> and <ack class="call"> signaling nodes
// before whatsmeow performs its default handling, giving a media/calls layer direct
// access to the raw binary node — including the stanza id and the
// <ack class="call" type="offer"> relay allocation that the parsed call events and
// the default ack path drop.
//
// For a <call> node, returning true means the handler fully processed the node:
// whatsmeow then skips its own acknowledgement and event dispatch for that node. This
// is needed for a video upgrade, which requires a typed type="video" ack that the
// generic ack does not satisfy. Returning false lets whatsmeow proceed normally (the
// handler may still have observed the node for a side effect, e.g. a deferred accept).
//
// For an <ack> node the return value is ignored — whatsmeow never dispatches acks.
//
// The handler runs on the node-handling goroutine and must not block.
type CallNodeHandler func(ctx context.Context, node *waBinary.Node) (handled bool)

// RegisterCallNodeHandler installs fn as the low-level handler for raw call signaling
// nodes (<call> and <ack class="call">) and returns a function that removes it.
//
// This is the supported, race-free replacement for reaching into the client's
// internals to intercept call nodes. It may be called at any time, including after
// Connect; registration is a single atomic store and never races the receive loop.
// Only one handler may be registered at a time — registering replaces any previous
// one. The returned remove func clears the handler only if it is still the current
// one. A nil fn is a no-op that returns a no-op remover.
//
// The companion package go.mau.fi/whatsmeow/calls uses this to drive the full media
// (audio/video) call engine.
func (cli *Client) RegisterCallNodeHandler(fn CallNodeHandler) (remove func()) {
	if fn == nil {
		return func() {}
	}
	cli.callNodeHandler.Store(&fn)
	return func() {
		cli.callNodeHandler.CompareAndSwap(&fn, nil)
	}
}

// callNodeInterceptor runs the registered call-node handler (if any) for a raw node
// and reports whether the caller should skip whatsmeow's default processing.
func (cli *Client) callNodeInterceptor(ctx context.Context, node *waBinary.Node) bool {
	if h := cli.callNodeHandler.Load(); h != nil {
		return (*h)(ctx, node)
	}
	return false
}

// handleAckNode dispatches <ack class="call"> nodes to the registered call-node
// handler. whatsmeow has no general <ack> handling — it drops ack nodes — so this is
// a no-op for every ack except class="call" when a calls layer is active. The relay
// allocation for an outbound call arrives only inside <ack class="call" type="offer">,
// which this surfaces.
func (cli *Client) handleAckNode(ctx context.Context, node *waBinary.Node) {
	if node.AttrGetter().String("class") != "call" {
		return
	}
	cli.callNodeInterceptor(ctx, node)
}

func (cli *Client) handleCallEvent(ctx context.Context, node *waBinary.Node) {
	// Give a registered calls/media layer first look at the raw <call> node (it
	// carries the stanza id, which the parsed events below drop). If it reports the
	// node fully handled — including sending its own typed ack — skip whatsmeow's
	// default acknowledgement and event dispatch.
	if cli.callNodeInterceptor(ctx, node) {
		return
	}
	defer cli.maybeDeferredAck(ctx, node)()

	if len(node.GetChildren()) != 1 {
		cli.dispatchEvent(&events.UnknownCallEvent{Node: node})
		return
	}
	ag := node.AttrGetter()
	child := node.GetChildren()[0]
	cag := child.AttrGetter()
	basicMeta := types.BasicCallMeta{
		From:        ag.JID("from"),
		Timestamp:   ag.UnixTime("t"),
		CallCreator: cag.JID("call-creator"),
		CallID:      cag.String("call-id"),
		GroupJID:    cag.OptionalJIDOrEmpty("group-jid"),
	}
	if basicMeta.CallCreator.Server == types.HiddenUserServer {
		basicMeta.CallCreatorAlt = cag.OptionalJIDOrEmpty("caller_pn")
	} else {
		// This may not actually exist
		basicMeta.CallCreatorAlt = cag.OptionalJIDOrEmpty("caller_lid")
	}
	switch child.Tag {
	case "offer":
		cli.dispatchEvent(&events.CallOffer{
			BasicCallMeta: basicMeta,
			CallRemoteMeta: types.CallRemoteMeta{
				RemotePlatform: ag.String("platform"),
				RemoteVersion:  ag.String("version"),
			},
			Data: &child,
		})
	case "offer_notice":
		cli.dispatchEvent(&events.CallOfferNotice{
			BasicCallMeta: basicMeta,
			Media:         cag.String("media"),
			Type:          cag.String("type"),
			Data:          &child,
		})
	case "relaylatency":
		cli.dispatchEvent(&events.CallRelayLatency{
			BasicCallMeta: basicMeta,
			Data:          &child,
		})
	case "accept":
		cli.dispatchEvent(&events.CallAccept{
			BasicCallMeta: basicMeta,
			CallRemoteMeta: types.CallRemoteMeta{
				RemotePlatform: ag.String("platform"),
				RemoteVersion:  ag.String("version"),
			},
			Data: &child,
		})
	case "preaccept":
		cli.dispatchEvent(&events.CallPreAccept{
			BasicCallMeta: basicMeta,
			CallRemoteMeta: types.CallRemoteMeta{
				RemotePlatform: ag.String("platform"),
				RemoteVersion:  ag.String("version"),
			},
			Data: &child,
		})
	case "transport":
		cli.dispatchEvent(&events.CallTransport{
			BasicCallMeta: basicMeta,
			CallRemoteMeta: types.CallRemoteMeta{
				RemotePlatform: ag.String("platform"),
				RemoteVersion:  ag.String("version"),
			},
			Data: &child,
		})
	case "terminate":
		cli.dispatchEvent(&events.CallTerminate{
			BasicCallMeta: basicMeta,
			Reason:        cag.String("reason"),
			Data:          &child,
		})
	case "reject":
		cli.dispatchEvent(&events.CallReject{
			BasicCallMeta: basicMeta,
			Data:          &child,
		})
	default:
		cli.dispatchEvent(&events.UnknownCallEvent{Node: node})
	}
}

// RejectCall reject an incoming call.
func (cli *Client) RejectCall(ctx context.Context, callFrom types.JID, callID string) error {
	ownID := cli.getOwnID()
	if ownID.IsEmpty() {
		return ErrNotLoggedIn
	}
	ownID, callFrom = ownID.ToNonAD(), callFrom.ToNonAD()
	rejectNode := waBinary.Node{
		Tag:     "reject",
		Attrs:   waBinary.Attrs{"call-id": callID, "call-creator": callFrom, "count": "0"},
		Content: nil,
	}
	if token, err := cli.ensureTCToken(ctx, callFrom); err != nil {
		cli.Log.Warnf("Failed to get privacy token for call reject to %s: %v", callFrom, err)
	} else if len(token) > 0 {
		rejectNode.Content = []waBinary.Node{{
			Tag:     "tctoken",
			Content: token,
		}}
	}
	return cli.sendNode(ctx, waBinary.Node{
		Tag:     "call",
		Attrs:   waBinary.Attrs{"id": cli.GenerateMessageID(), "from": ownID, "to": callFrom},
		Content: []waBinary.Node{rejectNode},
	})
}

// PreAcceptCall sends a <preaccept> stanza for an incoming call (ringing).
// Must be followed by <accept> or <reject>; an idle preaccept can lead to throttling.
func (cli *Client) PreAcceptCall(ctx context.Context, callFrom types.JID, callID string) error {
	ownID := cli.getOwnID()
	if ownID.IsEmpty() {
		return ErrNotLoggedIn
	}
	ownID, callFrom = ownID.ToNonAD(), callFrom.ToNonAD()
	return cli.sendNode(ctx, waBinary.Node{
		Tag:   "call",
		Attrs: waBinary.Attrs{"id": cli.GenerateMessageID(), "from": ownID, "to": callFrom},
		Content: []waBinary.Node{{
			Tag:     "preaccept",
			Attrs:   waBinary.Attrs{"call-id": callID, "call-creator": callFrom, "count": "0"},
			Content: nil,
		}},
	})
}

// AcceptCall sends an <accept> stanza for an incoming call.
// The full handshake needs WebRTC media; calling this without a backing
// media stack will cause the call to time out and may trigger throttling.
func (cli *Client) AcceptCall(ctx context.Context, callFrom types.JID, callID string) error {
	ownID := cli.getOwnID()
	if ownID.IsEmpty() {
		return ErrNotLoggedIn
	}
	ownID, callFrom = ownID.ToNonAD(), callFrom.ToNonAD()
	return cli.sendNode(ctx, waBinary.Node{
		Tag:   "call",
		Attrs: waBinary.Attrs{"id": cli.GenerateMessageID(), "from": ownID, "to": callFrom},
		Content: []waBinary.Node{{
			Tag:     "accept",
			Attrs:   waBinary.Attrs{"call-id": callID, "call-creator": callFrom, "count": "0"},
			Content: nil,
		}},
	})
}

// TerminateCall sends a <terminate> stanza. Reason defaults to "hangup" when empty.
func (cli *Client) TerminateCall(ctx context.Context, callFrom types.JID, callID string, reason string) error {
	ownID := cli.getOwnID()
	if ownID.IsEmpty() {
		return ErrNotLoggedIn
	}
	if reason == "" {
		reason = "hangup"
	}
	ownID, callFrom = ownID.ToNonAD(), callFrom.ToNonAD()
	return cli.sendNode(ctx, waBinary.Node{
		Tag:   "call",
		Attrs: waBinary.Attrs{"id": cli.GenerateMessageID(), "from": ownID, "to": callFrom},
		Content: []waBinary.Node{{
			Tag:     "terminate",
			Attrs:   waBinary.Attrs{"call-id": callID, "call-creator": callFrom, "reason": reason},
			Content: nil,
		}},
	})
}

// callCapabilityOffer is the capability blob carried in an outbound <offer>/<accept>
// (ver=1). Mirrors signaling.CapabilityOffer in the calls package.
//
// Source of truth: https://github.com/oxidezap/whatsapp-rust/blob/41095d4e6ba4610e054e9ede3af1d5e88a83faee/wacore/src/voip/stanza.rs
var callCapabilityOffer = []byte{0x01, 0x05, 0xf7, 0x09, 0xe4, 0xbb, 0x13}

// MakeCallParams configures the outbound call offer sent by [Client.MakeCallOffer].
type MakeCallParams struct {
	// To is the callee. It may be a phone JID (e.g. 12345@s.whatsapp.net), which is
	// resolved to a LID, or a LID directly (12345@lid).
	To types.JID
	// Video advertises a video call (adds the <video> child to the offer).
	Video bool
	// CallID, when non-empty, is used as the call-id (32 uppercase hex chars).
	// A random one is generated when empty.
	CallID string
	// CallKey, when 32 bytes, is the end-to-end call key. A random key is generated
	// when empty. The media layer derives the SRTP keys from this value.
	CallKey []byte
}

// CallOfferInfo describes the offer sent by [Client.MakeCallOffer].
type CallOfferInfo struct {
	// CallID is the call-id used for the offer (32 uppercase hex chars).
	CallID string
	// CallKey is the 32-byte end-to-end call key carried in the offer. A media layer
	// derives the SRTP keys from it.
	CallKey []byte
	// To is the resolved callee LID the offer was addressed to.
	To types.JID
}

// MakeCall originates an outbound call by sending a call <offer> to the given JID.
//
// This is the base-library, signaling-only primitive: it makes the callee's device
// ring, but it does not negotiate media. Bringing up audio/video requires a media
// engine — use the go.mau.fi/whatsmeow/calls package (calls.NewClient(cli).Call(...)),
// which drives this offer, the relay election, keying and the per-frame media loop end
// to end. For finer control over the offer (and to obtain the generated call-id and
// call key), use [Client.MakeCallOffer].
func (cli *Client) MakeCall(ctx context.Context, to types.JID, video bool) error {
	_, err := cli.MakeCallOffer(ctx, MakeCallParams{To: to, Video: video})
	return err
}

// MakeCallOffer builds and sends a call <offer> to params.To and returns the call-id
// and call key that were used. Like [Client.MakeCall] it is signaling-only — it makes
// the peer ring but does not negotiate media; pair it with a media engine
// (go.mau.fi/whatsmeow/calls) to actually connect audio/video.
//
// The offer's child order is load-bearing (the server rejects a malformed offer), and
// must stay in sync with signaling.BuildOffer in the calls package. The call key is
// encrypted to every device of the callee via its Signal session (fetching a pre-key
// bundle when no session exists yet); a fresh session (pkmsg) additionally carries the
// signed device identity, without which the server drops the offer.
func (cli *Client) MakeCallOffer(ctx context.Context, params MakeCallParams) (*CallOfferInfo, error) {
	ownLID := cli.getOwnLID()
	if ownLID.IsEmpty() {
		return nil, ErrNotLoggedIn
	}
	peerLID, err := cli.resolveCallPeerLID(ctx, params.To)
	if err != nil {
		return nil, err
	}
	devices, err := cli.GetUserDevices(ctx, []types.JID{peerLID})
	if err != nil {
		return nil, fmt.Errorf("call offer: device discovery for %s: %w", peerLID, err)
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("call offer: peer %s has no devices (unreachable or not on WhatsApp)", peerLID)
	}

	callKey := params.CallKey
	if len(callKey) == 0 {
		callKey = make([]byte, 32)
		if _, err := rand.Read(callKey); err != nil {
			return nil, err
		}
	}
	// The callKey travels as the Signal message body Message{Call{CallKey}}.
	plaintext, err := proto.Marshal(&waE2E.Message{Call: &waE2E.Call{CallKey: callKey}})
	if err != nil {
		return nil, err
	}

	type encDevice struct {
		jid     types.JID
		ct      []byte
		encType string
	}
	encDevices := make([]encDevice, 0, len(devices))
	needIdentity := false
	for _, dev := range devices {
		enc, ni, encErr := cli.encryptMessageForDevice(ctx, plaintext, dev, nil, nil, nil)
		if encErr != nil {
			bundles := cli.fetchPreKeysNoError(ctx, []types.JID{dev})
			enc, ni, encErr = cli.encryptMessageForDevice(ctx, plaintext, dev, bundles[dev], nil, nil)
			if encErr != nil {
				return nil, fmt.Errorf("call offer: encrypt call key for %s: %w", dev, encErr)
			}
		}
		ct, ok := enc.Content.([]byte)
		if !ok {
			return nil, fmt.Errorf("call offer: enc node for %s has no ciphertext", dev)
		}
		needIdentity = needIdentity || ni
		encDevices = append(encDevices, encDevice{jid: dev, ct: ct, encType: enc.AttrGetter().String("type")})
	}

	// A pkmsg offer must carry our signed device identity so the peer can verify the
	// new session; the server drops the offer otherwise.
	var deviceIdentity []byte
	if needIdentity {
		deviceIdentity, err = proto.Marshal(cli.Store.Account)
		if err != nil {
			return nil, fmt.Errorf("call offer: marshal device identity: %w", err)
		}
	}

	// Include the peer's privacy token when we have one (required to call a contact
	// with privacy enabled).
	var privacyToken []byte
	if pt, ptErr := cli.Store.PrivacyTokens.GetPrivacyToken(ctx, peerLID); ptErr == nil && pt != nil {
		privacyToken = pt.Token
	}

	callID := params.CallID
	if callID == "" {
		callID = generateCallID()
	}

	// Mandatory child order: privacy → audio(8k) → audio(16k) → [video] → net →
	// capability → destination|enc → encopt → [device-identity].
	children := make([]waBinary.Node, 0, 9)
	if privacyToken != nil {
		children = append(children, waBinary.Node{Tag: "privacy", Content: privacyToken})
	}
	children = append(children,
		waBinary.Node{Tag: "audio", Attrs: waBinary.Attrs{"enc": "opus", "rate": "8000"}},
		waBinary.Node{Tag: "audio", Attrs: waBinary.Attrs{"enc": "opus", "rate": "16000"}},
	)
	if params.Video {
		children = append(children, waBinary.Node{Tag: "video", Attrs: waBinary.Attrs{
			"enc": "h264", "dec": "h264", "orientation": "0",
			"screen_width": "1920", "screen_height": "1080", "device_orientation": "0",
		}})
	}
	children = append(children, waBinary.Node{Tag: "net", Attrs: waBinary.Attrs{"medium": "3"}})
	children = append(children, waBinary.Node{Tag: "capability", Attrs: waBinary.Attrs{"ver": "1"}, Content: callCapabilityOffer})
	if len(encDevices) > 1 {
		tos := make([]waBinary.Node, len(encDevices))
		for i, e := range encDevices {
			tos[i] = waBinary.Node{Tag: "to", Attrs: waBinary.Attrs{"jid": e.jid}, Content: []waBinary.Node{callEncNode(e.encType, e.ct)}}
		}
		children = append(children, waBinary.Node{Tag: "destination", Content: tos})
	} else {
		children = append(children, callEncNode(encDevices[0].encType, encDevices[0].ct))
	}
	children = append(children, waBinary.Node{Tag: "encopt", Attrs: waBinary.Attrs{"keygen": "2"}})
	if deviceIdentity != nil {
		children = append(children, waBinary.Node{Tag: "device-identity", Content: deviceIdentity})
	}

	offer := waBinary.Node{
		Tag:   "call",
		Attrs: waBinary.Attrs{"id": cli.GenerateMessageID(), "to": peerLID},
		Content: []waBinary.Node{{
			Tag:     "offer",
			Attrs:   waBinary.Attrs{"call-id": callID, "call-creator": ownLID},
			Content: children,
		}},
	}
	if err := cli.sendNode(ctx, offer); err != nil {
		return nil, fmt.Errorf("call offer: send: %w", err)
	}
	return &CallOfferInfo{CallID: callID, CallKey: callKey, To: peerLID}, nil
}

// callEncNode builds one <enc v=2 type=… count=0> child carrying the encrypted call key.
func callEncNode(encType string, ciphertext []byte) waBinary.Node {
	return waBinary.Node{
		Tag:     "enc",
		Attrs:   waBinary.Attrs{"v": "2", "type": encType, "count": "0"},
		Content: ciphertext,
	}
}

// generateCallID returns a call-id in WhatsApp's shape: 16 random bytes as uppercase hex.
func generateCallID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return strings.ToUpper(hex.EncodeToString(b[:]))
}

// resolveCallPeerLID turns a callee JID (phone number JID or LID) into the peer's LID,
// the address a call's E2E keys derive from. A LID is returned directly; a phone JID is
// mapped via the LID store, seeded by a usync query when not cached.
func (cli *Client) resolveCallPeerLID(ctx context.Context, target types.JID) (types.JID, error) {
	if target.IsEmpty() {
		return types.EmptyJID, fmt.Errorf("call offer: empty target")
	}
	if target.Server == types.HiddenUserServer {
		return target, nil // already a LID
	}
	if lid, err := cli.Store.LIDs.GetLIDForPN(ctx, target); err == nil && !lid.IsEmpty() {
		return lid, nil
	}
	info, err := cli.GetUserInfo(ctx, []types.JID{target})
	if err != nil {
		return types.EmptyJID, fmt.Errorf("call offer: usync %s: %w", target.User, err)
	}
	for _, ui := range info {
		if !ui.LID.IsEmpty() {
			return ui.LID, nil
		}
	}
	if lid, err := cli.Store.LIDs.GetLIDForPN(ctx, target); err == nil && !lid.IsEmpty() {
		return lid, nil
	}
	return types.EmptyJID, fmt.Errorf("call offer: no LID for %s (peer unreachable or not on WhatsApp)", target.User)
}
