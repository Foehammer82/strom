package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Foehammer82/wattkeeper/controller/internal/browse"
	"github.com/Foehammer82/wattkeeper/controller/internal/ca"
	"github.com/Foehammer82/wattkeeper/controller/internal/registry"
	"github.com/Foehammer82/wattkeeper/controller/internal/securestore"
)

func TestAdoptNodeCallsAgentAndMarksRegistryAdopted(t *testing.T) {
	t.Parallel()

	tlsAgent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/health" {
			t.Fatalf("unexpected TLS request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Fatalf("missing bearer token: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer tlsAgent.Close()
	tlsHostPort := strings.TrimPrefix(tlsAgent.URL, "https://")
	_, tlsPortText, _ := strings.Cut(tlsHostPort, ":")
	tlsPort, err := strconv.Atoi(tlsPortText)
	if err != nil {
		t.Fatalf("parse TLS port: %v", err)
	}
	certificate := tlsAgent.Certificate()
	fingerprint := computeFingerprintHex(certificate.Raw)

	var request agentAdoptRequest
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/adopt" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(agentAdoptResponse{
			Serial:         "serial-1234",
			Version:        "v0.3.0",
			ControllerURL:  request.ControllerURL,
			TLSPort:        tlsPort,
			TLSFingerprint: fingerprint,
			TokenSHA256:    "fingerprint",
		})
	}))
	defer agent.Close()

	hostPort := strings.TrimPrefix(agent.URL, "http://")
	host, portText, _ := strings.Cut(hostPort, ":")
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	store, err := registry.Open(filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	defer store.Close()
	if err := store.UpsertDiscoveredNode(context.Background(), registry.Node{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  host,
		Port:     port,
		Version:  "v0.3.0",
		UPSCount: 2,
		LastSeen: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	authority, err := ca.Ensure(t.TempDir())
	if err != nil {
		t.Fatalf("ensure CA: %v", err)
	}
	application := &app{
		registry: store,
		browser:  browse.New(nil),
		ca:       authority,
		client:   agent.Client(),
		vault: func() *securestore.Store {
			vault, err := securestore.Ensure(t.TempDir())
			if err != nil {
				t.Fatalf("ensure secure store: %v", err)
			}
			return vault
		}(),
	}

	requestRecorder := httptest.NewRequest(http.MethodPost, "http://controller.local/api/nodes/serial-1234/adopt", nil)
	response, err := application.adoptNode(context.Background(), requestRecorder, nodeResponse{
		ID:      "serial-1234",
		Address: host,
		Port:    port,
		Live:    true,
	})
	if err != nil {
		t.Fatalf("adoptNode() error = %v", err)
	}
	if response.Node.ID != "serial-1234" || !response.Node.Adopted || response.NUTUser != "controller" {
		t.Fatalf("response = %#v, want adopted controller response", response)
	}
	if request.ControllerURL != "http://controller.local" || request.CAPEM == "" || request.APIToken == "" || request.NUTPassword == "" {
		t.Fatalf("request = %#v, want populated adopt request", request)
	}
	stored, err := store.GetNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if !stored.Adopted {
		t.Fatalf("stored adopted = %t, want true", stored.Adopted)
	}
	trust, err := store.LoadNodeTrust(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("LoadNodeTrust() error = %v", err)
	}
	if trust.TLSFingerprint != fingerprint || trust.APITokenEnc == "" || trust.NUTPasswordEnc == "" {
		t.Fatalf("trust = %#v, want persisted TLS fingerprint and encrypted secrets", trust)
	}
	health, err := application.fetchTrustedNodeHealth(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("fetchTrustedNodeHealth() error = %v", err)
	}
	if health.NodeID != "serial-1234" || health.Health["status"] != "ok" {
		t.Fatalf("health = %#v, want trusted node health payload", health)
	}
}

func computeFingerprintHex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return strings.ToLower(fmt.Sprintf("%x", sum[:]))
}
