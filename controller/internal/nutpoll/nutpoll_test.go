package nutpoll

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Foehammer82/wattkeeper/controller/internal/registry"
	"github.com/Foehammer82/wattkeeper/controller/internal/securestore"
)

func TestClientPollReadsUPSesAndVariables(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	defer listener.Close()
	go serveFakeNUT(t, listener, "controller", "secret")

	host, _, _ := net.SplitHostPort(listener.Addr().String())
	client := &Client{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
		},
	}
	snapshots, err := client.Poll(context.Background(), host, "controller", "secret")
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("len(snapshots) = %d, want 2", len(snapshots))
	}
	if snapshots[0].Name != "ups-a" || snapshots[0].Driver != "usbhid-ups" {
		t.Fatalf("snapshot[0] = %#v, want ups-a/usbhid-ups", snapshots[0])
	}
	if snapshots[1].Variables["battery.charge"] != "76" {
		t.Fatalf("snapshot[1] = %#v, want battery.charge 76", snapshots[1])
	}
}

func TestPollerPollOnceStoresSamplesAndPrunesRetention(t *testing.T) {
	t.Parallel()

	store, err := registry.Open(filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	vault, err := securestore.Ensure(t.TempDir())
	if err != nil {
		t.Fatalf("Ensure() secure store error = %v", err)
	}
	if err := store.UpsertDiscoveredNode(context.Background(), registry.Node{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  "192.168.1.50",
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 1,
		LastSeen: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertDiscoveredNode() error = %v", err)
	}
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	sealedPassword, err := vault.SealString("secret")
	if err != nil {
		t.Fatalf("SealString() error = %v", err)
	}
	if err := store.SaveNodeTrust(context.Background(), "serial-1234", registry.Trust{
		ControllerURL:  "http://controller.local",
		TLSPort:        8443,
		TLSFingerprint: "fingerprint",
		NUTUser:        "controller",
		APITokenEnc:    "token",
		NUTPasswordEnc: sealedPassword,
	}); err != nil {
		t.Fatalf("SaveNodeTrust() error = %v", err)
	}

	oldAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if err := store.RecordUPSSnapshots(context.Background(), "serial-1234", oldAt, []registry.UPSSnapshot{{Name: "ups-old", Variables: map[string]string{"ups.status": "OL"}}}); err != nil {
		t.Fatalf("RecordUPSSnapshots() seed error = %v", err)
	}

	poller := &Poller{
		Logger: log.New(&strings.Builder{}, "", 0),
		Store:  store,
		Vault:  vault,
		Client: fakeClient{snapshots: []registry.UPSSnapshot{{Name: "ups-a", Driver: "usbhid-ups", Variables: map[string]string{"battery.charge": "99", "ups.status": "OL"}}}},
		Now: func() time.Time {
			return oldAt.Add(2 * time.Hour)
		},
		Retention: 30 * time.Minute,
	}
	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce() error = %v", err)
	}

	upsCount, err := store.CountUPSForNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("CountUPSForNode() error = %v", err)
	}
	if upsCount != 1 {
		t.Fatalf("ups row count = %d, want 1", upsCount)
	}
	sampleCount, err := store.CountSamples(context.Background())
	if err != nil {
		t.Fatalf("CountSamples() error = %v", err)
	}
	if sampleCount != 2 {
		t.Fatalf("sample row count = %d, want 2", sampleCount)
	}
	node, err := store.GetNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if node.CommsState != registry.CommsStateHealthy || node.PollFailures != 0 || !node.LastPolledAt.Equal(oldAt.Add(2*time.Hour)) {
		t.Fatalf("node = %#v, want healthy poll state", node)
	}
}

func TestPollerPollOnceMarksNodeOfflineAfterRepeatedFailures(t *testing.T) {
	t.Parallel()

	store, err := registry.Open(filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	vault, err := securestore.Ensure(t.TempDir())
	if err != nil {
		t.Fatalf("Ensure() secure store error = %v", err)
	}
	if err := store.UpsertDiscoveredNode(context.Background(), registry.Node{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  "192.168.1.50",
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 1,
		LastSeen: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertDiscoveredNode() error = %v", err)
	}
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	sealedPassword, err := vault.SealString("secret")
	if err != nil {
		t.Fatalf("SealString() error = %v", err)
	}
	if err := store.SaveNodeTrust(context.Background(), "serial-1234", registry.Trust{
		ControllerURL:  "http://controller.local",
		TLSPort:        8443,
		TLSFingerprint: "fingerprint",
		NUTUser:        "controller",
		APITokenEnc:    "token",
		NUTPasswordEnc: sealedPassword,
	}); err != nil {
		t.Fatalf("SaveNodeTrust() error = %v", err)
	}

	poller := &Poller{
		Logger:           log.New(&strings.Builder{}, "", 0),
		Store:            store,
		Vault:            vault,
		Client:           fakeClient{err: fmt.Errorf("dial timeout")},
		OfflineThreshold: 3,
		Now: func() time.Time {
			return time.Date(2026, 7, 3, 15, 0, 0, 0, time.UTC)
		},
	}
	for range 3 {
		if err := poller.PollOnce(context.Background()); err != nil {
			t.Fatalf("PollOnce() error = %v", err)
		}
	}
	node, err := store.GetNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if node.PollFailures != 3 || node.CommsState != registry.CommsStateOffline || !strings.Contains(node.LastPollError, "dial timeout") {
		t.Fatalf("node = %#v, want offline comms state after repeated failures", node)
	}
}

type fakeClient struct {
	snapshots []registry.UPSSnapshot
	err       error
}

func (f fakeClient) Poll(context.Context, string, string, string) ([]registry.UPSSnapshot, error) {
	return f.snapshots, f.err
}

func serveFakeNUT(t *testing.T, listener net.Listener, username, password string) {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	authedUser := false
	authedPass := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "USERNAME "+username:
			authedUser = true
			_, _ = writer.WriteString("OK\n")
		case line == "PASSWORD "+password:
			authedPass = true
			_, _ = writer.WriteString("OK\n")
		case line == "LIST UPS" && authedUser && authedPass:
			_, _ = writer.WriteString("BEGIN LIST UPS\n")
			_, _ = writer.WriteString("UPS ups-b \"CyberPower\"\n")
			_, _ = writer.WriteString("UPS ups-a \"APC\"\n")
			_, _ = writer.WriteString("END LIST UPS\n")
		case line == "LIST VAR ups-a" && authedUser && authedPass:
			_, _ = writer.WriteString("BEGIN LIST VAR ups-a\n")
			_, _ = writer.WriteString("VAR ups-a driver.name \"usbhid-ups\"\n")
			_, _ = writer.WriteString("VAR ups-a battery.charge \"100\"\n")
			_, _ = writer.WriteString("VAR ups-a ups.status \"OL\"\n")
			_, _ = writer.WriteString("END LIST VAR ups-a\n")
		case line == "LIST VAR ups-b" && authedUser && authedPass:
			_, _ = writer.WriteString("BEGIN LIST VAR ups-b\n")
			_, _ = writer.WriteString("VAR ups-b driver.name \"blazer_usb\"\n")
			_, _ = writer.WriteString("VAR ups-b battery.charge \"76\"\n")
			_, _ = writer.WriteString("END LIST VAR ups-b\n")
		default:
			_, _ = writer.WriteString(fmt.Sprintf("ERR UNKNOWN-COMMAND %s\n", line))
		}
		_ = writer.Flush()
	}
}
