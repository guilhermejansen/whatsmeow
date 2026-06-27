package calls

import (
	"context"
	"sync"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/calls/diag"
)

// Client is the managed entry point to the WhatsApp 1:1 calling stack. It wraps a
// connected *whatsmeow.Client and drives the whole call lifecycle — signaling, keying,
// relay election, and media — under the hood, behind a small surface:
// place a call with Call, handle inbound calls from an OnIncomingCall listener, and
// attach a Player (outbound audio) and a sink (inbound audio) to each Call.
//
// The library never configures logging; pass WithLogger to surface its debug/trace.
type Client struct {
	wa   *whatsmeow.Client
	log  zerolog.Logger
	diag *diag.Recorder
	eng  *engine

	mu             sync.Mutex
	onIncomingCall func(*Call)
}

// NewClient wraps a connected whatsmeow client and installs the call event handlers.
// Construct it before the whatsmeow client connects so the low-level <ack>/<call>
// interception is in place before the receive loop starts.
func NewClient(wa *whatsmeow.Client, opts ...Option) *Client {
	cfg := resolveConfig(opts)
	c := &Client{wa: wa, log: cfg.log, diag: cfg.diag}
	c.eng = newEngine(c)
	c.eng.install()
	return c
}

// Call places a 1:1 call to target (a phone number, a phone JID, or an @lid JID),
// returning the live Call once the offer is on the wire. Attach a Player and listeners
// to the returned Call; media starts automatically once the peer answers and the relay
// endpoint arrives.
func (c *Client) Call(ctx context.Context, target string) (*Call, error) {
	return c.eng.placeCall(ctx, target)
}

// OnIncomingCall registers the listener fired for each inbound call offer. The handler
// receives a Call that has not been answered yet; call Answer or Reject on it. Only the
// most recently registered listener is used.
func (c *Client) OnIncomingCall(fn func(*Call)) {
	c.mu.Lock()
	c.onIncomingCall = fn
	c.mu.Unlock()
}

// AbortAll ends every in-flight call immediately, tearing down each call's media
// and firing its OnEnd listener with reason. The Client stays installed and can
// place or receive new calls afterwards — use it to drop all active calls without
// detaching from the underlying whatsmeow client (e.g. on a transient network drop).
func (c *Client) AbortAll(reason string) {
	c.eng.abortAll(reason)
}

// Close tears the call Client down: it aborts all in-flight calls (firing each
// Call's OnEnd with "client closed") and detaches the low-level whatsmeow handlers
// installed by NewClient. Call it when the underlying whatsmeow client disconnects
// for good; the Client must not be used after Close.
func (c *Client) Close() {
	c.eng.close("client closed")
}

// incomingCallHandler returns the registered inbound-call listener, or nil.
func (c *Client) incomingCallHandler() func(*Call) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.onIncomingCall
}
