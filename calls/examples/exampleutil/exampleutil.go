// Package exampleutil holds the whatsmeow login boilerplate shared by the calls
// examples so each example file can focus on the calling API.
//
// It uses the pure-Go modernc.org/sqlite driver (no CGO) and stores the session in a
// local file so subsequent runs skip the QR scan.
package exampleutil

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	_ "modernc.org/sqlite"
)

// NewClient opens (or creates) the session store at dbPath and returns an UNCONNECTED
// whatsmeow client. Construct your calls.Client from it before calling Login so the
// call-node hook is installed before the receive loop starts.
func NewClient(ctx context.Context, dbPath string) (*whatsmeow.Client, error) {
	dbLog := waLog.Stdout("Database", "WARN", true)
	container, err := sqlstore.New(ctx, "sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)", dbLog)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("load device: %w", err)
	}
	clientLog := waLog.Stdout("Client", "INFO", true)
	return whatsmeow.NewClient(device, clientLog), nil
}

// Login connects the client, printing a QR code payload to scan on first run. On
// subsequent runs (already paired) it just connects.
func Login(ctx context.Context, client *whatsmeow.Client) error {
	if client.Store.ID != nil {
		return client.Connect()
	}
	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("qr channel: %w", err)
	}
	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	for evt := range qrChan {
		switch evt.Event {
		case whatsmeow.QRChannelEventCode:
			// Render this string as a QR code (e.g. with github.com/mdp/qrterminal)
			// and scan it in WhatsApp → Linked devices.
			fmt.Println("Scan this QR code (paste into a QR generator):")
			fmt.Println(evt.Code)
		default:
			fmt.Println("Login event:", evt.Event)
		}
	}
	return nil
}
