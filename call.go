// Copyright (c) 2021 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package whatsmeow

import (
	"context"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func (cli *Client) handleCallEvent(ctx context.Context, node *waBinary.Node) {
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
	return cli.sendNode(ctx, waBinary.Node{
		Tag:   "call",
		Attrs: waBinary.Attrs{"id": cli.GenerateMessageID(), "from": ownID, "to": callFrom},
		Content: []waBinary.Node{{
			Tag:     "reject",
			Attrs:   waBinary.Attrs{"call-id": callID, "call-creator": callFrom, "count": "0"},
			Content: nil,
		}},
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

// MakeCall originates an outbound call.
// Not yet implemented; returns ErrCallMakeNotImplemented.
func (cli *Client) MakeCall(ctx context.Context, to types.JID, video bool) error {
	_ = ctx
	_ = to
	_ = video
	return ErrCallMakeNotImplemented
}
