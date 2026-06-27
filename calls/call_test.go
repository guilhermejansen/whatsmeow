package calls

import (
	"context"
	"testing"
)

// regSession builds an outgoing session for the registry tests (reuses the JID
// helpers from session_test.go in this package).
func regSession(id string) *CallSession {
	return NewOutgoingSession(id, peerJID(), creatorJID())
}

func isCancelled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// TestInsertTransitionRemove pins the registry bookkeeping contract.
func TestInsertTransitionRemove(t *testing.T) {
	reg := NewCallRegistry()
	if !reg.Insert(regSession("CID")) {
		t.Fatal("first insert should succeed")
	}
	if reg.Insert(regSession("CID")) {
		t.Error("duplicate insert should fail")
	}
	if ph, ok := reg.Phase("CID"); !ok || ph != CallPhaseIdle {
		t.Errorf("phase = (%d, %v), want (Idle, true)", ph, ok)
	}
	if !reg.Transition("CID", CallPhaseCalling) {
		t.Error("legal transition rejected")
	}
	if ph, _ := reg.Phase("CID"); ph != CallPhaseCalling {
		t.Errorf("phase = %d, want Calling", ph)
	}
	if reg.Transition("UNKNOWN", CallPhaseCalling) {
		t.Error("transition on unknown call should fail")
	}
	if !reg.Remove("CID") {
		t.Error("remove of existing call should return true")
	}
	if reg.Remove("CID") {
		t.Error("remove of absent call should return false")
	}
	if reg.ActiveCount() != 0 {
		t.Errorf("active count = %d, want 0", reg.ActiveCount())
	}
}

// TestRemoveCancelsMediaTask confirms Remove cancels the call's media task.
func TestRemoveCancelsMediaTask(t *testing.T) {
	reg := NewCallRegistry()
	reg.Insert(regSession("A"))
	ctx, cancel := context.WithCancel(context.Background())
	reg.SetMediaTask("A", cancel)
	if !reg.Remove("A") {
		t.Fatal("remove failed")
	}
	if !isCancelled(ctx) {
		t.Error("removed call's media task must be cancelled")
	}
}

// TestAbortAllCancelsMediaTasks confirms AbortAll cancels every task and empties the registry.
func TestAbortAllCancelsMediaTasks(t *testing.T) {
	reg := NewCallRegistry()
	reg.Insert(regSession("A"))
	reg.Insert(regSession("B"))
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	reg.SetMediaTask("A", cancelA)
	reg.SetMediaTask("B", cancelB)
	if reg.AbortAll() != 2 {
		t.Errorf("AbortAll returned %d, want 2", reg.AbortAll())
	}
	if !isCancelled(ctxA) || !isCancelled(ctxB) {
		t.Error("AbortAll must cancel every media task")
	}
	if reg.ActiveCount() != 0 {
		t.Error("AbortAll must empty the registry")
	}
}

// TestReplaceCancelsOldMediaTask confirms a replacing SetMediaTask cancels the prior handle.
func TestReplaceCancelsOldMediaTask(t *testing.T) {
	reg := NewCallRegistry()
	reg.Insert(regSession("A"))
	oldCtx, oldCancel := context.WithCancel(context.Background())
	reg.SetMediaTask("A", oldCancel)
	newCtx, newCancel := context.WithCancel(context.Background())
	reg.SetMediaTask("A", newCancel)
	if !isCancelled(oldCtx) {
		t.Error("replaced media task must be cancelled")
	}
	if isCancelled(newCtx) {
		t.Error("replacement task must stay live")
	}
	reg.Remove("A")
	if !isCancelled(newCtx) {
		t.Error("replacement cancelled on remove")
	}
}

// TestSetMediaTaskOnUnknownCallCancels confirms an orphan handle is cancelled immediately.
func TestSetMediaTaskOnUnknownCallCancels(t *testing.T) {
	reg := NewCallRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	reg.SetMediaTask("GONE", cancel) // never inserted
	if !isCancelled(ctx) {
		t.Error("orphan media task must be cancelled immediately")
	}
}

// TestEngineAbortAllEndsCallsCancelsMediaFiresOnEnd pins Client.AbortAll / engine.abortAll:
// every in-flight call's media task is cancelled, its phase advances to Ended, its OnEnd
// fires with the reason, and the engine's live-call map is emptied.
func TestEngineAbortAllEndsCallsCancelsMediaFiresOnEnd(t *testing.T) {
	e := newEngine(&Client{})

	type fixture struct {
		ec     *engineCall
		ctx    context.Context
		reason *string
	}
	mk := func(id string) fixture {
		ctx, cancel := context.WithCancel(context.Background())
		call := &Call{eng: e, id: id}
		reason := new(string)
		call.OnEnd(func(r string) { *reason = r })
		return fixture{ec: &engineCall{call: call, cancel: cancel}, ctx: ctx, reason: reason}
	}

	a, b := mk("A"), mk("B")
	e.calls["A"], e.calls["B"] = a.ec, b.ec

	e.abortAll("bye")

	if !isCancelled(a.ctx) || !isCancelled(b.ctx) {
		t.Error("abortAll must cancel every call's media task")
	}
	if *a.reason != "bye" || *b.reason != "bye" {
		t.Errorf("OnEnd reasons = (%q, %q), want both \"bye\"", *a.reason, *b.reason)
	}
	if a.ec.call.State() != CallPhaseEnded || b.ec.call.State() != CallPhaseEnded {
		t.Error("abortAll must end every call")
	}
	if len(e.calls) != 0 {
		t.Errorf("abortAll must empty e.calls, got %d entries", len(e.calls))
	}
}

// TestEngineCloseDetachesHandlers pins Client.Close / engine.close: it aborts in-flight
// calls and releases the low-level call-node hook (removeHook). A zero eventHandlerID
// skips RemoveEventHandler, so the unit test needs no live whatsmeow client.
func TestEngineCloseDetachesHandlers(t *testing.T) {
	e := newEngine(&Client{})
	removed := false
	e.removeHook = func() { removed = true }
	e.eventHandlerID = 0 // skip RemoveEventHandler (no wa client here)

	ctx, cancel := context.WithCancel(context.Background())
	call := &Call{eng: e, id: "X"}
	reason := ""
	call.OnEnd(func(r string) { reason = r })
	e.calls["X"] = &engineCall{call: call, cancel: cancel}

	e.close("client closed")

	if !isCancelled(ctx) {
		t.Error("close must cancel in-flight media")
	}
	if reason != "client closed" {
		t.Errorf("OnEnd reason = %q, want \"client closed\"", reason)
	}
	if !removed {
		t.Error("close must call the call-node hook remover")
	}
	if e.removeHook != nil {
		t.Error("close must clear removeHook")
	}
	if len(e.calls) != 0 {
		t.Error("close must empty e.calls")
	}
}
