package registry

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreUpsertAndListNodes(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertDiscoveredNode(context.Background(), Node{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  "192.168.1.50",
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 2,
		LastSeen: now,
	}); err != nil {
		t.Fatalf("UpsertDiscoveredNode() error = %v", err)
	}

	nodes, err := store.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].ID != "serial-1234" || nodes[0].UPSCount != 2 || nodes[0].Adopted {
		t.Fatalf("node = %#v, want discovered pending node", nodes[0])
	}

	loaded, err := store.GetNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if !loaded.LastSeen.Equal(now) {
		t.Fatalf("LastSeen = %v, want %v", loaded.LastSeen, now)
	}

	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}

	adopted, err := store.GetNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("GetNode() after adopt error = %v", err)
	}
	if !adopted.Adopted {
		t.Fatalf("Adopted = %t, want true", adopted.Adopted)
	}
	if adopted.AdoptedAt.IsZero() {
		t.Fatal("AdoptedAt = zero, want timestamp")
	}

	trust := Trust{
		ControllerURL:  "https://controller.local",
		TLSPort:        8443,
		TLSFingerprint: "fingerprint",
		NUTUser:        "controller",
		APITokenEnc:    "enc-token",
		NUTPasswordEnc: "enc-pass",
	}
	if err := store.SaveNodeTrust(context.Background(), "serial-1234", trust); err != nil {
		t.Fatalf("SaveNodeTrust() error = %v", err)
	}
	loadedTrust, err := store.LoadNodeTrust(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("LoadNodeTrust() error = %v", err)
	}
	if loadedTrust != trust {
		t.Fatalf("trust = %#v, want %#v", loadedTrust, trust)
	}
}
