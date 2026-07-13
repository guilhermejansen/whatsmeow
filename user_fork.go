// Fork-local additions to the user/usync layer.
//
// Kept out of user.go so merging upstream whatsmeow doesn't conflict with it: the
// vendored file only needs the two call sites in IsOnWhatsApp's parse loop.
//
// IsOnWhatsApp's usync query already requests <username> and <disappearing_mode>
// (user.go: {Tag: "username"}, {Tag: "disappearing_mode"}), but upstream never
// reads either node back — the server sends the data and whatsmeow drops it on the
// floor. These parsers pick it back up.

package whatsmeow

import (
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

// parseUsernameNode reads the <username> child of a usync <user> node.
//
// WhatsApp is still rolling usernames out, so in practice the server answers with
// an empty self-closing node (<username/>) and this returns "". Once a contact
// actually has a username, it arrives here with no further changes needed.
func parseUsernameNode(user waBinary.Node) string {
	node, ok := user.GetOptionalChildByTag("username")
	if !ok {
		return ""
	}
	if content, contentOK := node.Content.([]byte); contentOK && len(content) > 0 {
		return string(content)
	}
	// Some server builds carry it as an attribute rather than node content.
	return node.AttrGetter().OptionalString("username")
}

// parseDisappearingModeNode reads the <disappearing_mode> child of a usync <user>
// node, e.g. <disappearing_mode duration="0" t="1777142576"/>.
//
// duration is in seconds (0 = disappearing messages off) and t is when the contact
// last changed the setting.
func parseDisappearingModeNode(user waBinary.Node) *types.DisappearingMode {
	node, ok := user.GetOptionalChildByTag("disappearing_mode")
	if !ok {
		return nil
	}
	ag := node.AttrGetter()
	setAt, _ := ag.GetUnixTime("t", false)
	return &types.DisappearingMode{
		Duration: time.Duration(ag.OptionalInt("duration")) * time.Second,
		SetAt:    setAt,
	}
}
