package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Foehammer82/wattkeeper/agent/nodeapi"
	"github.com/Foehammer82/wattkeeper/controller/internal/aggregatenut"
	"github.com/Foehammer82/wattkeeper/controller/internal/alerts"
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

func TestPushTrustedAgentUpdateAppliesSignedBinaryOnNode(t *testing.T) {
	t.Parallel()

	node := newInProcessAgentNode(t)
	application, _ := newTestAppWithDiscoveredNode(t, node.httpURL(), nil)
	request := httptest.NewRequest(http.MethodPost, "http://controller.local/api/nodes/serial-1234/adopt", nil)
	if _, err := application.adoptNode(context.Background(), request, node.nodeResponse()); err != nil {
		t.Fatalf("adoptNode() error = %v", err)
	}

	updatePayloadPath := filepath.Join(t.TempDir(), "wattkeeper-agent-update")
	updateContent := []byte("updated-agent-binary")
	if err := os.WriteFile(updatePayloadPath, updateContent, 0o755); err != nil {
		t.Fatalf("write update payload: %v", err)
	}

	response, err := application.pushTrustedAgentUpdate(context.Background(), "serial-1234", updatePayloadPath, "v0.4.0")
	if err != nil {
		t.Fatalf("pushTrustedAgentUpdate() error = %v", err)
	}
	if response.Status != "applied" || response.Version != "v0.4.0" || response.SHA256 == "" {
		t.Fatalf("response = %#v, want applied ota metadata", response)
	}
	if !response.RestartRequired {
		t.Fatalf("response = %#v, want restart_required=true", response)
	}

	actualContent, err := os.ReadFile(node.agentBinary)
	if err != nil {
		t.Fatalf("read node agent binary: %v", err)
	}
	if !bytes.Equal(actualContent, updateContent) {
		t.Fatalf("node agent binary content = %q, want %q", string(actualContent), string(updateContent))
	}
}

func TestRunBackupCommandCopiesControllerDB(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	dbPath := controllerDBPath(dataDir)
	if err := os.WriteFile(dbPath, []byte("sqlite-data"), 0o600); err != nil {
		t.Fatalf("WriteFile(controller db) error = %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "controller-backup.db")

	err := runBackupCommand(log.New(io.Discard, "", 0), []string{"--data-dir", dataDir, "--output", outputPath})
	if err != nil {
		t.Fatalf("runBackupCommand() error = %v", err)
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile(output) error = %v", err)
	}
	if string(content) != "sqlite-data" {
		t.Fatalf("backup content = %q, want sqlite-data", string(content))
	}
}

func TestRunBackupCommandRequiresOutputPath(t *testing.T) {
	t.Parallel()

	err := runBackupCommand(log.New(io.Discard, "", 0), nil)
	if err == nil || !strings.Contains(err.Error(), "output path") {
		t.Fatalf("runBackupCommand() error = %v, want missing output path error", err)
	}
}

func TestRunRestoreCommandRequiresForceWhenDBExists(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	if err := os.WriteFile(controllerDBPath(dataDir), []byte("existing-db"), 0o600); err != nil {
		t.Fatalf("WriteFile(existing db) error = %v", err)
	}
	inputPath := filepath.Join(t.TempDir(), "controller-backup.db")
	if err := os.WriteFile(inputPath, []byte("backup-db"), 0o600); err != nil {
		t.Fatalf("WriteFile(input) error = %v", err)
	}

	err := runRestoreCommand(log.New(io.Discard, "", 0), []string{"--data-dir", dataDir, "--input", inputPath})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("runRestoreCommand() error = %v, want --force guidance", err)
	}
}

func TestRunRestoreCommandOverwritesControllerDBWithForce(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	destination := controllerDBPath(dataDir)
	if err := os.WriteFile(destination, []byte("old-db"), 0o600); err != nil {
		t.Fatalf("WriteFile(destination) error = %v", err)
	}
	inputPath := filepath.Join(t.TempDir(), "controller-backup.db")
	if err := os.WriteFile(inputPath, []byte("new-db"), 0o600); err != nil {
		t.Fatalf("WriteFile(input) error = %v", err)
	}

	err := runRestoreCommand(log.New(io.Discard, "", 0), []string{"--data-dir", dataDir, "--input", inputPath, "--force"})
	if err != nil {
		t.Fatalf("runRestoreCommand() error = %v", err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile(destination) error = %v", err)
	}
	if string(content) != "new-db" {
		t.Fatalf("destination content = %q, want new-db", string(content))
	}
}

func TestAdoptNodeReturnsConflictWhenAgentIsAlreadyAdopted(t *testing.T) {
	t.Parallel()

	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONError(w, http.StatusConflict, "node already adopted: serial-1234")
	}))
	defer agent.Close()

	hostPort := strings.TrimPrefix(agent.URL, "http://")
	host, portText, _ := strings.Cut(hostPort, ":")
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	application, _ := newTestAppWithDiscoveredNode(t, agent.URL, nil)
	request := httptest.NewRequest(http.MethodPost, "http://controller.local/api/nodes/serial-1234/adopt", nil)

	_, err = application.adoptNode(context.Background(), request, nodeResponse{
		ID:      "serial-1234",
		Address: host,
		Port:    port,
		Live:    true,
	})
	if !errors.Is(err, errNodeAlreadyAdopted) {
		t.Fatalf("adoptNode() error = %v, want errNodeAlreadyAdopted", err)
	}
}

func TestAdoptNodeRetriesTransientTimeout(t *testing.T) {
	t.Parallel()

	tlsAgent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	fingerprint := computeFingerprintHex(tlsAgent.Certificate().Raw)

	attempts := 0
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			time.Sleep(220 * time.Millisecond)
			return
		}
		_ = json.NewEncoder(w).Encode(agentAdoptResponse{
			Serial:         "serial-1234",
			Version:        "v0.3.0",
			ControllerURL:  "http://controller.local",
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
	application, _ := newTestAppWithDiscoveredNode(t, agent.URL, nil)
	application.client = &http.Client{Timeout: 100 * time.Millisecond}

	request := httptest.NewRequest(http.MethodPost, "http://controller.local/api/nodes/serial-1234/adopt", nil)
	response, err := application.adoptNode(context.Background(), request, nodeResponse{
		ID:      "serial-1234",
		Address: host,
		Port:    port,
		Live:    true,
	})
	if err != nil {
		t.Fatalf("adoptNode() error = %v", err)
	}
	if !response.Node.Adopted {
		t.Fatalf("response = %#v, want adopted node", response)
	}
	if attempts < 2 {
		t.Fatalf("attempts = %d, want retry", attempts)
	}
}

func TestFetchTrustedNodeHealthRejectsBadStoredToken(t *testing.T) {
	t.Parallel()

	tlsAgent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer expected-token" {
			writeJSONError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer tlsAgent.Close()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", tlsAgent)
	if err := saveTestTrust(t, application, store, tlsAgent, "wrong-token"); err != nil {
		t.Fatalf("saveTestTrust() error = %v", err)
	}

	_, err := application.fetchTrustedNodeHealth(context.Background(), "serial-1234")
	if !errors.Is(err, errTrustedNodeUnauthorized) {
		t.Fatalf("fetchTrustedNodeHealth() error = %v, want errTrustedNodeUnauthorized", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://controller.local/api/nodes/serial-1234/health", nil)
	recorder := httptest.NewRecorder()
	application.handleTrustedNodeHealth(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
	}
}

func TestSyncLiveNodesPreservesPersistedAdoptedState(t *testing.T) {
	t.Parallel()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}

	setBrowserSnapshot(t, application.browser, browse.LiveNode{
		ID:       "serial-1234",
		Instance: "seed-wattkeeper-agent",
		Hostname: "wattkeeper-agent",
		Address:  "172.20.0.2",
		Port:     80,
		Version:  "dev",
		UPSCount: 2,
		Adopted:  false,
		LastSeen: time.Now().UTC(),
	})

	if err := application.syncLiveNodes(context.Background()); err != nil {
		t.Fatalf("syncLiveNodes() error = %v", err)
	}

	node, err := store.GetNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if !node.Adopted {
		t.Fatalf("node.Adopted = %t, want true", node.Adopted)
	}
}

func TestFetchTrustedNodeHealthRejectsFingerprintMismatch(t *testing.T) {
	t.Parallel()

	tlsAgent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer tlsAgent.Close()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", tlsAgent)
	if err := saveTestTrust(t, application, store, tlsAgent, "expected-token"); err != nil {
		t.Fatalf("saveTestTrust() error = %v", err)
	}
	if err := store.SaveNodeTrust(context.Background(), "serial-1234", registry.Trust{
		ControllerURL:  "http://controller.local",
		TLSPort:        tlsServerPort(t, tlsAgent),
		TLSFingerprint: strings.Repeat("0", 64),
		NUTUser:        "controller",
		APITokenEnc:    mustSealString(t, application.vault, "expected-token"),
		NUTPasswordEnc: mustSealString(t, application.vault, "nut-secret"),
	}); err != nil {
		t.Fatalf("SaveNodeTrust() overwrite error = %v", err)
	}

	_, err := application.fetchTrustedNodeHealth(context.Background(), "serial-1234")
	if !errors.Is(err, errTrustedNodeFingerprintDrift) {
		t.Fatalf("fetchTrustedNodeHealth() error = %v, want errTrustedNodeFingerprintDrift", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://controller.local/api/nodes/serial-1234/health", nil)
	recorder := httptest.NewRecorder()
	application.handleTrustedNodeHealth(recorder, req)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
}

func TestHandleForgetNodeDeletesRegistryEntry(t *testing.T) {
	t.Parallel()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodDelete, "http://controller.local/api/nodes/serial-1234", nil)
	recorder := httptest.NewRecorder()

	application.handleForgetNode(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusNoContent, recorder.Body.String())
	}
	if _, err := store.GetNode(context.Background(), "serial-1234"); !errors.Is(err, registry.ErrNodeNotFound) {
		t.Fatalf("GetNode() error = %v, want ErrNodeNotFound", err)
	}
}

func TestHandleForgetNodeReturnsNotFoundForUnknownNode(t *testing.T) {
	t.Parallel()

	application, _ := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	req := httptest.NewRequest(http.MethodDelete, "http://controller.local/api/nodes/missing-node", nil)
	recorder := httptest.NewRecorder()

	application.handleForgetNode(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
}

func TestHandleUpdateNodeMetadataPersistsControllerLabels(t *testing.T) {
	t.Parallel()

	application, _ := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	body := strings.NewReader(`{"display_name":"Lab Rack Node","location_label":"Utility Closet","site_label":"Home"}`)
	req := httptest.NewRequest(http.MethodPatch, "http://controller.local/api/nodes/serial-1234", body)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	application.handleUpdateNodeMetadata(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response nodeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.DisplayName != "Lab Rack Node" || response.LocationLabel != "Utility Closet" || response.SiteLabel != "Home" {
		t.Fatalf("response = %#v, want updated metadata", response)
	}
}

func TestHandleUpdateNodeMetadataRejectsEmptyPatch(t *testing.T) {
	t.Parallel()

	application, _ := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	req := httptest.NewRequest(http.MethodPatch, "http://controller.local/api/nodes/serial-1234", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	application.handleUpdateNodeMetadata(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestHandleUpdateNodeMetadataPersistsLocalUIPolicyForAdoptedNode(t *testing.T) {
	t.Parallel()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "http://controller.local/api/nodes/serial-1234", strings.NewReader(`{"local_ui_policy_managed":true,"local_ui_policy_enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	application.handleUpdateNodeMetadata(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response nodeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !response.LocalUIManaged || response.LocalUIEnabled {
		t.Fatalf("response = %#v, want local_ui_policy_managed=true and local_ui_policy_enabled=false", response)
	}
}

func TestHandleUpdateNodeMetadataRejectsLocalUIPolicyForPendingNode(t *testing.T) {
	t.Parallel()

	application, _ := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	req := httptest.NewRequest(http.MethodPatch, "http://controller.local/api/nodes/serial-1234", strings.NewReader(`{"local_ui_policy_managed":true,"local_ui_policy_enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	application.handleUpdateNodeMetadata(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestHandleUpdateNodeMetadataRejectsLocalUIEnabledWithoutManaged(t *testing.T) {
	t.Parallel()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "http://controller.local/api/nodes/serial-1234", strings.NewReader(`{"local_ui_policy_enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	application.handleUpdateNodeMetadata(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestHandleUpdateNodeMetadataPushesLocalUIPolicyToLiveAdoptedNode(t *testing.T) {
	t.Parallel()

	policyRequests := 0
	tlsAgent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer expected-token" {
			writeJSONError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/settings/ui/policy" {
			t.Fatalf("unexpected trusted policy request %s %s", r.Method, r.URL.Path)
		}
		var request nodeLocalUIPolicyRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode local UI policy request: %v", err)
		}
		if !request.Managed || request.Enabled {
			t.Fatalf("policy request = %#v, want managed=true enabled=false", request)
		}
		policyRequests++
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "updated"})
	}))
	defer tlsAgent.Close()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	hostPort := strings.TrimPrefix(tlsAgent.URL, "https://")
	host, _, _ := strings.Cut(hostPort, ":")
	if err := store.UpsertDiscoveredNode(context.Background(), registry.Node{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  host,
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 1,
		LastSeen: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertDiscoveredNode() refresh error = %v", err)
	}
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	if err := saveTestTrust(t, application, store, tlsAgent, "expected-token"); err != nil {
		t.Fatalf("saveTestTrust() error = %v", err)
	}
	setBrowserSnapshot(t, application.browser, browse.LiveNode{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  host,
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 1,
		Adopted:  true,
		LastSeen: time.Now().UTC(),
	})

	req := httptest.NewRequest(http.MethodPatch, "http://controller.local/api/nodes/serial-1234", strings.NewReader(`{"local_ui_policy_managed":true,"local_ui_policy_enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	application.handleUpdateNodeMetadata(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if policyRequests != 1 {
		t.Fatalf("policy requests = %d, want 1", policyRequests)
	}
}

func TestHandleUpdateNodeMetadataPushesLocalUIPolicyReleaseToLiveAdoptedNode(t *testing.T) {
	t.Parallel()

	policyRequests := 0
	tlsAgent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer expected-token" {
			writeJSONError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/settings/ui/policy" {
			t.Fatalf("unexpected trusted policy request %s %s", r.Method, r.URL.Path)
		}
		var request nodeLocalUIPolicyRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode local UI policy request: %v", err)
		}
		if request.Managed || !request.Enabled {
			t.Fatalf("policy request = %#v, want managed=false enabled=true", request)
		}
		policyRequests++
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "updated"})
	}))
	defer tlsAgent.Close()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	hostPort := strings.TrimPrefix(tlsAgent.URL, "https://")
	host, _, _ := strings.Cut(hostPort, ":")
	if err := store.UpsertDiscoveredNode(context.Background(), registry.Node{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  host,
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 1,
		LastSeen: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertDiscoveredNode() refresh error = %v", err)
	}
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	if err := store.UpdateNodeLocalUIPolicy(context.Background(), "serial-1234", true, true); err != nil {
		t.Fatalf("UpdateNodeLocalUIPolicy() seed error = %v", err)
	}
	if err := saveTestTrust(t, application, store, tlsAgent, "expected-token"); err != nil {
		t.Fatalf("saveTestTrust() error = %v", err)
	}
	setBrowserSnapshot(t, application.browser, browse.LiveNode{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  host,
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 1,
		Adopted:  true,
		LastSeen: time.Now().UTC(),
	})

	req := httptest.NewRequest(http.MethodPatch, "http://controller.local/api/nodes/serial-1234", strings.NewReader(`{"local_ui_policy_managed":false}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	application.handleUpdateNodeMetadata(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if policyRequests != 1 {
		t.Fatalf("policy requests = %d, want 1", policyRequests)
	}
}

func TestHandleUpdateNodeMetadataRollsBackLocalUIPolicyWhenTrustedPushFails(t *testing.T) {
	t.Parallel()

	tlsAgent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONError(w, http.StatusUnauthorized, "invalid bearer token")
	}))
	defer tlsAgent.Close()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	hostPort := strings.TrimPrefix(tlsAgent.URL, "https://")
	host, _, _ := strings.Cut(hostPort, ":")
	if err := store.UpsertDiscoveredNode(context.Background(), registry.Node{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  host,
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 1,
		LastSeen: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertDiscoveredNode() refresh error = %v", err)
	}
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	if err := store.UpdateNodeLocalUIPolicy(context.Background(), "serial-1234", true, true); err != nil {
		t.Fatalf("UpdateNodeLocalUIPolicy() seed error = %v", err)
	}
	if err := saveTestTrust(t, application, store, tlsAgent, "expected-token"); err != nil {
		t.Fatalf("saveTestTrust() error = %v", err)
	}
	setBrowserSnapshot(t, application.browser, browse.LiveNode{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  host,
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 1,
		Adopted:  true,
		LastSeen: time.Now().UTC(),
	})

	req := httptest.NewRequest(http.MethodPatch, "http://controller.local/api/nodes/serial-1234", strings.NewReader(`{"local_ui_policy_managed":false}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	application.handleUpdateNodeMetadata(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
	}
	node, err := store.GetNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if !node.LocalUIManaged || !node.LocalUIEnabled {
		t.Fatalf("node policy = managed:%t enabled:%t, want rollback to managed=true enabled=true", node.LocalUIManaged, node.LocalUIEnabled)
	}
}

func TestBuildNodeResponsesSyncsLiveDiscoveryIntoRegistry(t *testing.T) {
	t.Parallel()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	liveSeen := time.Date(2026, 7, 3, 14, 30, 0, 0, time.UTC)
	setBrowserSnapshot(t, application.browser, browse.LiveNode{
		ID:       "serial-1234",
		Instance: "wkeeper-node-lab",
		Hostname: "wkeeper-node-lab.local",
		Address:  "192.168.1.55",
		Port:     8080,
		Version:  "v0.3.1",
		UPSCount: 3,
		Adopted:  false,
		LastSeen: liveSeen,
	})

	nodes, err := application.buildNodeResponses(context.Background())
	if err != nil {
		t.Fatalf("buildNodeResponses() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].Status != "pending" || !nodes[0].Live {
		t.Fatalf("node = %#v, want live pending node", nodes[0])
	}
	if nodes[0].Instance != "wkeeper-node-lab" || nodes[0].Address != "192.168.1.55" || nodes[0].Port != 8080 || nodes[0].UPSCount != 3 {
		t.Fatalf("node = %#v, want refreshed discovery fields", nodes[0])
	}
	stored, err := store.GetNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if stored.Instance != "wkeeper-node-lab" || stored.Address != "192.168.1.55" || !stored.LastSeen.Equal(liveSeen) {
		t.Fatalf("stored node = %#v, want registry refreshed from live discovery", stored)
	}
}

func TestBuildNodeResponsesPreservesStoredAdoptionDuringReconciliation(t *testing.T) {
	t.Parallel()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	setBrowserSnapshot(t, application.browser, browse.LiveNode{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  "192.168.1.50",
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 2,
		Adopted:  false,
		LastSeen: time.Now().UTC(),
	})

	nodes, err := application.buildNodeResponses(context.Background())
	if err != nil {
		t.Fatalf("buildNodeResponses() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if !nodes[0].Adopted || nodes[0].Status != "adopted-online" {
		t.Fatalf("node = %#v, want adopted-online with stored adoption preserved", nodes[0])
	}
}

func TestBuildNodeResponsesMarksAdoptedNodeOfflineWhenDiscoveryMissing(t *testing.T) {
	t.Parallel()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	setBrowserSnapshot(t, application.browser)

	nodes, err := application.buildNodeResponses(context.Background())
	if err != nil {
		t.Fatalf("buildNodeResponses() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].Live || nodes[0].Status != "adopted-offline" {
		t.Fatalf("node = %#v, want adopted-offline node", nodes[0])
	}

	node, err := application.buildNodeResponse(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("buildNodeResponse() error = %v", err)
	}
	if node.Status != "adopted-offline" {
		t.Fatalf("node = %#v, want adopted-offline from single-node response", node)
	}
}

func TestBuildNodeResponseSurfacesPollDerivedCommsState(t *testing.T) {
	t.Parallel()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	polledAt := time.Date(2026, 7, 3, 16, 0, 0, 0, time.UTC)
	if err := store.UpdateNodePollState(context.Background(), "serial-1234", registry.PollState{
		CommsState:    registry.CommsStateOffline,
		PollFailures:  3,
		LastPolledAt:  polledAt,
		LastPollError: "dial timeout",
	}); err != nil {
		t.Fatalf("UpdateNodePollState() error = %v", err)
	}
	node, err := application.buildNodeResponse(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("buildNodeResponse() error = %v", err)
	}
	if node.CommsState != registry.CommsStateOffline || node.PollFailures != 3 || !node.LastPolledAt.Equal(polledAt) || node.LastPollError != "dial timeout" {
		t.Fatalf("node = %#v, want surfaced poll comms state", node)
	}
}

func TestBuildNodeResponseIncludesLatestUPSSummaries(t *testing.T) {
	t.Parallel()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	capturedAt := time.Date(2026, 7, 3, 17, 0, 0, 0, time.UTC)
	if err := store.RecordUPSSnapshots(context.Background(), "serial-1234", capturedAt, []registry.UPSSnapshot{
		{
			Name:   "ups-a",
			Driver: "usbhid-ups",
			Variables: map[string]string{
				"ups.status":      "OL",
				"battery.charge":  "98",
				"ups.load":        "34",
				"battery.runtime": "1800",
			},
		},
	}); err != nil {
		t.Fatalf("RecordUPSSnapshots() error = %v", err)
	}
	node, err := application.buildNodeResponse(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("buildNodeResponse() error = %v", err)
	}
	if len(node.UPSSummaries) != 1 {
		t.Fatalf("len(node.UPSSummaries) = %d, want 1", len(node.UPSSummaries))
	}
	summary := node.UPSSummaries[0]
	if summary.Name != "ups-a" || summary.Driver != "usbhid-ups" || summary.Status != "OL" {
		t.Fatalf("summary = %#v, want ups-a/usbhid-ups/OL", summary)
	}
	if summary.BatteryChargePercent == nil || *summary.BatteryChargePercent != 98 {
		t.Fatalf("summary = %#v, want battery charge 98", summary)
	}
	if summary.LoadPercent == nil || *summary.LoadPercent != 34 {
		t.Fatalf("summary = %#v, want load 34", summary)
	}
	if summary.RuntimeSeconds == nil || *summary.RuntimeSeconds != 1800 {
		t.Fatalf("summary = %#v, want runtime 1800", summary)
	}
}

func TestHandleNodeUPSReturnsDetailAndHistory(t *testing.T) {
	t.Parallel()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	firstAt := time.Now().UTC().Add(-3 * time.Hour)
	secondAt := firstAt.Add(5 * time.Minute)
	if err := store.RecordUPSSnapshots(context.Background(), "serial-1234", firstAt, []registry.UPSSnapshot{{Name: "ups-a", Driver: "usbhid-ups", Variables: map[string]string{"battery.charge": "100", "ups.status": "OL"}}}); err != nil {
		t.Fatalf("RecordUPSSnapshots() first error = %v", err)
	}
	if err := store.RecordUPSSnapshots(context.Background(), "serial-1234", secondAt, []registry.UPSSnapshot{{Name: "ups-a", Driver: "usbhid-ups", Variables: map[string]string{"battery.charge": "96", "ups.status": "OB DISCHRG"}}}); err != nil {
		t.Fatalf("RecordUPSSnapshots() second error = %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "http://controller.local/api/nodes/serial-1234/ups", nil)
	listRecorder := httptest.NewRecorder()
	application.handleNodeUPS(listRecorder, listReq)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d body=%s", listRecorder.Code, http.StatusOK, listRecorder.Body.String())
	}

	detailReq := httptest.NewRequest(http.MethodGet, "http://controller.local/api/nodes/serial-1234/ups/ups-a", nil)
	detailRecorder := httptest.NewRecorder()
	application.handleNodeUPS(detailRecorder, detailReq)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want %d body=%s", detailRecorder.Code, http.StatusOK, detailRecorder.Body.String())
	}
	var detail nodeUPSDetailResponse
	if err := json.Unmarshal(detailRecorder.Body.Bytes(), &detail); err != nil {
		t.Fatalf("Unmarshal(detail) error = %v", err)
	}
	if detail.Name != "ups-a" || detail.Variables["battery.charge"] != "96" || detail.Variables["ups.status"] != "OB DISCHRG" {
		t.Fatalf("detail = %#v, want latest ups-a variables", detail)
	}

	historyReq := httptest.NewRequest(http.MethodGet, "http://controller.local/api/nodes/serial-1234/ups/ups-a/history?limit=3", nil)
	historyRecorder := httptest.NewRecorder()
	application.handleNodeUPS(historyRecorder, historyReq)
	if historyRecorder.Code != http.StatusOK {
		t.Fatalf("history status = %d, want %d body=%s", historyRecorder.Code, http.StatusOK, historyRecorder.Body.String())
	}
	var history nodeUPSHistoryResponse
	if err := json.Unmarshal(historyRecorder.Body.Bytes(), &history); err != nil {
		t.Fatalf("Unmarshal(history) error = %v", err)
	}
	if len(history.Samples) == 0 || history.Samples[0].Value != "96" {
		t.Fatalf("history = %#v, want newest sample first", history)
	}

	thirdAt := secondAt.Add(2 * time.Hour)
	if err := store.RecordUPSSnapshots(context.Background(), "serial-1234", thirdAt, []registry.UPSSnapshot{{Name: "ups-a", Driver: "usbhid-ups", Variables: map[string]string{"battery.charge": "91", "battery.runtime": "1500"}}}); err != nil {
		t.Fatalf("RecordUPSSnapshots() third error = %v", err)
	}
	filteredReq := httptest.NewRequest(http.MethodGet, "http://controller.local/api/nodes/serial-1234/ups/ups-a/history?var=battery.runtime&hours=24&limit=5", nil)
	filteredRecorder := httptest.NewRecorder()
	application.handleNodeUPS(filteredRecorder, filteredReq)
	if filteredRecorder.Code != http.StatusOK {
		t.Fatalf("filtered history status = %d, want %d body=%s", filteredRecorder.Code, http.StatusOK, filteredRecorder.Body.String())
	}
	var filtered nodeUPSHistoryResponse
	if err := json.Unmarshal(filteredRecorder.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("Unmarshal(filtered history) error = %v", err)
	}
	if len(filtered.Samples) != 1 || filtered.Samples[0].Variable != "battery.runtime" || filtered.Samples[0].Value != "1500" {
		t.Fatalf("filtered history = %#v, want only recent battery.runtime sample", filtered)
	}
}

func TestHandleNodeUPSDetailIncludesBatteryRuntimeTrend(t *testing.T) {
	t.Parallel()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	base := time.Now().UTC().Add(-7 * 24 * time.Hour)
	samples := []struct {
		offsetDays int
		runtime    string
	}{
		{offsetDays: 0, runtime: "2400"},
		{offsetDays: 1, runtime: "2200"},
		{offsetDays: 2, runtime: "2000"},
		{offsetDays: 3, runtime: "1800"},
		{offsetDays: 4, runtime: "1600"},
	}
	for _, sample := range samples {
		capturedAt := base.Add(time.Duration(sample.offsetDays) * 24 * time.Hour)
		if err := store.RecordUPSSnapshots(context.Background(), "serial-1234", capturedAt, []registry.UPSSnapshot{{
			Name:   "ups-a",
			Driver: "usbhid-ups",
			Variables: map[string]string{
				"battery.charge":  "95",
				"battery.runtime": sample.runtime,
				"ups.status":      "OL",
			},
		}}); err != nil {
			t.Fatalf("RecordUPSSnapshots() error = %v", err)
		}
	}

	detailReq := httptest.NewRequest(http.MethodGet, "http://controller.local/api/nodes/serial-1234/ups/ups-a", nil)
	detailRecorder := httptest.NewRecorder()
	application.handleNodeUPS(detailRecorder, detailReq)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want %d body=%s", detailRecorder.Code, http.StatusOK, detailRecorder.Body.String())
	}
	var detail nodeUPSDetailResponse
	if err := json.Unmarshal(detailRecorder.Body.Bytes(), &detail); err != nil {
		t.Fatalf("Unmarshal(detail) error = %v", err)
	}
	if detail.BatteryRuntimeTrend == nil {
		t.Fatalf("detail = %#v, want battery runtime trend", detail)
	}
	if detail.BatteryRuntimeTrend.BaselineRuntimeSeconds != 2400 {
		t.Fatalf("baseline runtime = %d, want 2400", detail.BatteryRuntimeTrend.BaselineRuntimeSeconds)
	}
	if detail.BatteryRuntimeTrend.LatestRuntimeSeconds != 1600 {
		t.Fatalf("latest runtime = %d, want 1600", detail.BatteryRuntimeTrend.LatestRuntimeSeconds)
	}
	if detail.BatteryRuntimeTrend.SamplesUsed < 5 {
		t.Fatalf("samples used = %d, want at least 5", detail.BatteryRuntimeTrend.SamplesUsed)
	}
	if detail.BatteryRuntimeTrend.TrendSecondsPer30Days >= 0 {
		t.Fatalf("trend seconds/30d = %f, want negative", detail.BatteryRuntimeTrend.TrendSecondsPer30Days)
	}
	if detail.BatteryRuntimeTrend.EstimatedReplaceBy.IsZero() {
		t.Fatalf("expected estimated replace-by timestamp in trend response")
	}
}

func TestHandleNodeUPSReturnsLiveTrustedDetailAndRunsCommand(t *testing.T) {
	t.Parallel()

	tlsAgent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer expected-token" {
			writeJSONError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/ups/ups-a":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":   "ups-a",
				"driver": "usbhid-ups",
				"status": "OL",
				"metrics": map[string]any{
					"name":                   "ups-a",
					"driver":                 "usbhid-ups",
					"status":                 "OL",
					"battery_charge_percent": 97,
				},
				"variables": map[string]string{"battery.charge": "97", "ups.status": "OL"},
				"commands":  []map[string]any{{"name": "test.battery.start.quick", "description": "Quick self test", "destructive": false}},
				"writable":  []map[string]any{{"name": "ups.delay.shutdown", "editor": "RANGE", "current_value": "120"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/ups/ups-a/command":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode command request: %v", err)
			}
			if request["cmd"] != "test.battery.start.quick" {
				t.Fatalf("command request = %#v, want test.battery.start.quick", request)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ups": "ups-a", "command": "test.battery.start.quick", "output": "OK"})
		default:
			t.Fatalf("unexpected trusted UPS request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer tlsAgent.Close()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	hostPort := strings.TrimPrefix(tlsAgent.URL, "https://")
	host, _, _ := strings.Cut(hostPort, ":")
	if err := store.UpsertDiscoveredNode(context.Background(), registry.Node{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  host,
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 1,
		LastSeen: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertDiscoveredNode() refresh error = %v", err)
	}
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	if err := saveTestTrust(t, application, store, tlsAgent, "expected-token"); err != nil {
		t.Fatalf("saveTestTrust() error = %v", err)
	}
	if err := store.RecordUPSSnapshots(context.Background(), "serial-1234", time.Now().UTC(), []registry.UPSSnapshot{{Name: "ups-a", Driver: "usbhid-ups", Variables: map[string]string{"battery.charge": "95", "ups.status": "OL"}}}); err != nil {
		t.Fatalf("RecordUPSSnapshots() error = %v", err)
	}
	setBrowserSnapshot(t, application.browser, browse.LiveNode{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  host,
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 1,
		Adopted:  true,
		LastSeen: time.Now().UTC(),
	})

	detailReq := httptest.NewRequest(http.MethodGet, "http://controller.local/api/nodes/serial-1234/ups/ups-a", nil)
	detailRecorder := httptest.NewRecorder()
	application.handleNodeUPS(detailRecorder, detailReq)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want %d body=%s", detailRecorder.Code, http.StatusOK, detailRecorder.Body.String())
	}
	var detail nodeUPSDetailResponse
	if err := json.Unmarshal(detailRecorder.Body.Bytes(), &detail); err != nil {
		t.Fatalf("Unmarshal(detail) error = %v", err)
	}
	if !detail.Live || len(detail.Commands) != 1 || detail.Commands[0].Name != "test.battery.start.quick" {
		t.Fatalf("detail = %#v, want trusted live commands", detail)
	}

	commandReq := httptest.NewRequest(http.MethodPost, "http://controller.local/api/nodes/serial-1234/ups/ups-a/command", strings.NewReader(`{"cmd":"test.battery.start.quick"}`))
	commandReq.Header.Set("Content-Type", "application/json")
	commandRecorder := httptest.NewRecorder()
	application.handleNodeUPS(commandRecorder, commandReq)
	if commandRecorder.Code != http.StatusOK {
		t.Fatalf("command status = %d, want %d body=%s", commandRecorder.Code, http.StatusOK, commandRecorder.Body.String())
	}
	var command nodeUPSCommandResponse
	if err := json.Unmarshal(commandRecorder.Body.Bytes(), &command); err != nil {
		t.Fatalf("Unmarshal(command) error = %v", err)
	}
	if command.Command != "test.battery.start.quick" || command.Output != "OK" {
		t.Fatalf("command = %#v, want executed trusted command", command)
	}
}

func TestHandleAlertRulesCRUDAndEvents(t *testing.T) {
	t.Parallel()
	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	application.alerts = &alerts.Engine{Store: store, Deliverer: &fakeAlertDeliverer{}, Now: func() time.Time { return time.Date(2026, 7, 3, 19, 0, 0, 0, time.UTC) }}

	createReq := httptest.NewRequest(http.MethodPost, "http://controller.local/api/alerts/rules", strings.NewReader(`{"kind":"node_offline","webhook_url":"http://example.invalid/hook","debounce_seconds":120}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRecorder := httptest.NewRecorder()
	application.handleAlertRules(createRecorder, createReq)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createRecorder.Code, http.StatusCreated, createRecorder.Body.String())
	}
	var created registry.AlertRule
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("Unmarshal(created) error = %v", err)
	}
	if created.ID == 0 || created.Kind != "node_offline" {
		t.Fatalf("created rule = %#v, want persisted node_offline rule", created)
	}

	listRecorder := httptest.NewRecorder()
	application.handleAlertRules(listRecorder, httptest.NewRequest(http.MethodGet, "http://controller.local/api/alerts/rules", nil))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d body=%s", listRecorder.Code, http.StatusOK, listRecorder.Body.String())
	}

	enabled := false
	patchPayload := fmt.Sprintf(`{"enabled":%t}`, enabled)
	patchReq := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("http://controller.local/api/alerts/rules/%d", created.ID), strings.NewReader(patchPayload))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRecorder := httptest.NewRecorder()
	application.handleAlertRules(patchRecorder, patchReq)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want %d body=%s", patchRecorder.Code, http.StatusOK, patchRecorder.Body.String())
	}

	testReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("http://controller.local/api/alerts/rules/%d/test", created.ID), nil)
	testRecorder := httptest.NewRecorder()
	application.handleAlertRules(testRecorder, testReq)
	if testRecorder.Code != http.StatusOK {
		t.Fatalf("test-fire status = %d, want %d body=%s", testRecorder.Code, http.StatusOK, testRecorder.Body.String())
	}

	eventsRecorder := httptest.NewRecorder()
	application.handleAlertEvents(eventsRecorder, httptest.NewRequest(http.MethodGet, "http://controller.local/api/alerts/events?limit=5", nil))
	if eventsRecorder.Code != http.StatusOK {
		t.Fatalf("events status = %d, want %d body=%s", eventsRecorder.Code, http.StatusOK, eventsRecorder.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("http://controller.local/api/alerts/rules/%d", created.ID), nil)
	deleteRecorder := httptest.NewRecorder()
	application.handleAlertRules(deleteRecorder, deleteReq)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d body=%s", deleteRecorder.Code, http.StatusNoContent, deleteRecorder.Body.String())
	}
}

func TestHandleControllerSettingsUpdatesAggregateListener(t *testing.T) {
	t.Parallel()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	application.aggregate = aggregatenut.NewManager(nil)
	if err := application.aggregate.Apply(context.Background(), true, "127.0.0.1:0"); err != nil {
		t.Fatalf("aggregate.Apply(start) error = %v", err)
	}
	t.Cleanup(application.aggregate.Close)
	if err := store.SaveControllerSettings(context.Background(), registry.ControllerSettings{AggregateNUTEnabled: true, AggregateNUTListen: "127.0.0.1:0"}); err != nil {
		t.Fatalf("SaveControllerSettings() seed error = %v", err)
	}

	getRecorder := httptest.NewRecorder()
	application.handleControllerSettings(getRecorder, httptest.NewRequest(http.MethodGet, "http://controller.local/api/settings/controller", nil))
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d body=%s", getRecorder.Code, http.StatusOK, getRecorder.Body.String())
	}
	var getResponse controllerSettingsResponse
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &getResponse); err != nil {
		t.Fatalf("Unmarshal(getResponse) error = %v", err)
	}
	if !getResponse.AggregateNUTEnabled || !getResponse.AggregateNUTActive {
		t.Fatalf("GET response = %#v, want enabled active listener", getResponse)
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "http://controller.local/api/settings/controller", strings.NewReader(`{"aggregate_nut_enabled":false}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRecorder := httptest.NewRecorder()
	application.handleControllerSettings(patchRecorder, patchReq)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want %d body=%s", patchRecorder.Code, http.StatusOK, patchRecorder.Body.String())
	}
	var patchResponse controllerSettingsResponse
	if err := json.Unmarshal(patchRecorder.Body.Bytes(), &patchResponse); err != nil {
		t.Fatalf("Unmarshal(patchResponse) error = %v", err)
	}
	if patchResponse.AggregateNUTEnabled || patchResponse.AggregateNUTActive {
		t.Fatalf("PATCH response = %#v, want disabled inactive listener", patchResponse)
	}

	persisted, err := store.LoadControllerSettings(context.Background(), registry.ControllerSettings{AggregateNUTEnabled: true, AggregateNUTListen: ":3493"})
	if err != nil {
		t.Fatalf("LoadControllerSettings() error = %v", err)
	}
	if persisted.AggregateNUTEnabled {
		t.Fatalf("persisted settings = %#v, want aggregate listener disabled", persisted)
	}
}

func TestAggregateNUTProtocolListsUPSAndRunsTrustedInstcmd(t *testing.T) {
	t.Parallel()

	tlsAgent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer expected-token" {
			writeJSONError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/ups/ups-a" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":   "ups-a",
				"driver": "usbhid-ups",
				"status": "OL",
				"metrics": map[string]any{
					"name":                   "ups-a",
					"driver":                 "usbhid-ups",
					"status":                 "OL",
					"battery_charge_percent": 95,
				},
				"variables": map[string]string{"battery.charge": "95", "ups.status": "OL"},
				"commands":  []map[string]any{{"name": "test.battery.start.quick", "description": "Quick self test", "destructive": false}},
				"writable":  []map[string]any{},
			})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/ups/ups-a/command" {
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode command request: %v", err)
			}
			if request["cmd"] != "test.battery.start.quick" {
				t.Fatalf("command request = %#v, want test.battery.start.quick", request)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ups": "ups-a", "command": "test.battery.start.quick", "output": "OK"})
			return
		}
		writeJSONError(w, http.StatusNotFound, "not found")
	}))
	defer tlsAgent.Close()

	application, store := newTestAppWithDiscoveredNode(t, "http://127.0.0.1:1", nil)
	hostPort := strings.TrimPrefix(tlsAgent.URL, "https://")
	host, _, _ := strings.Cut(hostPort, ":")
	if err := store.UpsertDiscoveredNode(context.Background(), registry.Node{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  host,
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 1,
		LastSeen: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertDiscoveredNode() refresh error = %v", err)
	}
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	if err := saveTestTrust(t, application, store, tlsAgent, "expected-token"); err != nil {
		t.Fatalf("saveTestTrust() error = %v", err)
	}
	if err := store.RecordUPSSnapshots(context.Background(), "serial-1234", time.Now().UTC(), []registry.UPSSnapshot{{Name: "ups-a", Driver: "usbhid-ups", Variables: map[string]string{"battery.charge": "95", "ups.status": "OL"}}}); err != nil {
		t.Fatalf("RecordUPSSnapshots() error = %v", err)
	}

	application.aggregate = aggregatenut.NewManager(nil)
	application.aggregate.SetBackend(&aggregateNUTBackend{app: application})
	application.aggregate.SetAuthenticator(func(username, password string) bool {
		return username == "controller" && password == "secret"
	})
	t.Cleanup(application.aggregate.Close)
	if err := application.aggregate.Apply(context.Background(), true, "127.0.0.1:0"); err != nil {
		t.Fatalf("aggregate.Apply(start) error = %v", err)
	}
	_, listen, active := application.aggregate.Status()
	if !active {
		t.Fatalf("aggregate listener active = %t, want true", active)
	}

	conn, err := net.DialTimeout("tcp", listen, 2*time.Second)
	if err != nil {
		t.Fatalf("DialTimeout() error = %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for _, command := range []string{"USERNAME controller\n", "PASSWORD secret\n"} {
		if _, err := conn.Write([]byte(command)); err != nil {
			t.Fatalf("Write(%q) error = %v", strings.TrimSpace(command), err)
		}
		response, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("ReadString(%q) error = %v", strings.TrimSpace(command), readErr)
		}
		if response != "OK\n" {
			t.Fatalf("auth response = %q, want OK", response)
		}
	}

	if _, err := conn.Write([]byte("LIST UPS\n")); err != nil {
		t.Fatalf("Write(LIST UPS) error = %v", err)
	}
	begin, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString(begin LIST UPS) error = %v", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString(UPS line) error = %v", err)
	}
	end, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString(end LIST UPS) error = %v", err)
	}
	if begin != "BEGIN LIST UPS\n" || !strings.HasPrefix(line, "UPS serial_1234__ups_a ") || end != "END LIST UPS\n" {
		t.Fatalf("LIST UPS response unexpected: %q%q%q", begin, line, end)
	}

	if _, err := conn.Write([]byte("LIST CMD serial_1234__ups_a\n")); err != nil {
		t.Fatalf("Write(LIST CMD) error = %v", err)
	}
	begin, err = reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString(begin LIST CMD) error = %v", err)
	}
	line, err = reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString(CMD line) error = %v", err)
	}
	end, err = reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString(end LIST CMD) error = %v", err)
	}
	if begin != "BEGIN LIST CMD serial_1234__ups_a\n" || line != "CMD serial_1234__ups_a test.battery.start.quick\n" || end != "END LIST CMD serial_1234__ups_a\n" {
		t.Fatalf("LIST CMD response unexpected: %q%q%q", begin, line, end)
	}

	if _, err := conn.Write([]byte("GET CMDDESC serial_1234__ups_a test.battery.start.quick\n")); err != nil {
		t.Fatalf("Write(GET CMDDESC) error = %v", err)
	}
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString(GET CMDDESC) error = %v", err)
	}
	if response != "CMDDESC serial_1234__ups_a test.battery.start.quick \"Quick self test\"\n" {
		t.Fatalf("GET CMDDESC response = %q, want command description", response)
	}

	if _, err := conn.Write([]byte("INSTCMD serial_1234__ups_a test.battery.start.quick\n")); err != nil {
		t.Fatalf("Write(INSTCMD) error = %v", err)
	}
	response, err = reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString(INSTCMD) error = %v", err)
	}
	if response != "OK\n" {
		t.Fatalf("INSTCMD response = %q, want OK", response)
	}
}

func TestAdoptionHandshakeAgainstInProcessAgent(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()

		node := newInProcessAgentNode(t)
		application, store := newTestAppWithDiscoveredNode(t, node.httpURL(), nil)

		response, err := application.adoptNode(context.Background(), httptest.NewRequest(http.MethodPost, "http://controller.local/api/nodes/serial-1234/adopt", nil), node.nodeResponse())
		if err != nil {
			t.Fatalf("adoptNode() error = %v", err)
		}
		if !response.Node.Adopted || response.Node.Status != "adopted-online" {
			t.Fatalf("response = %#v, want adopted-online", response)
		}
		if node.reloader.calls != 1 {
			t.Fatalf("reloader calls = %d, want 1", node.reloader.calls)
		}
		if values := node.adopted.values; len(values) != 1 || !values[0] {
			t.Fatalf("adopted updates = %v, want [true]", values)
		}
		if _, err := os.Stat(node.adoptionPath); err != nil {
			t.Fatalf("stat adoption path: %v", err)
		}
		content, err := os.ReadFile(filepath.Join(node.configDir, "upsd.users"))
		if err != nil {
			t.Fatalf("read upsd.users: %v", err)
		}
		if !strings.Contains(string(content), "[controller]") {
			t.Fatalf("upsd.users missing controller block: %s", string(content))
		}
		trust, err := store.LoadNodeTrust(context.Background(), "serial-1234")
		if err != nil {
			t.Fatalf("LoadNodeTrust() error = %v", err)
		}
		if trust.TLSFingerprint == "" || trust.APITokenEnc == "" || trust.NUTPasswordEnc == "" {
			t.Fatalf("trust = %#v, want persisted trust material", trust)
		}
		health, err := application.fetchTrustedNodeHealth(context.Background(), "serial-1234")
		if err != nil {
			t.Fatalf("fetchTrustedNodeHealth() error = %v", err)
		}
		if health.NodeID != "serial-1234" {
			t.Fatalf("health = %#v, want serial-1234", health)
		}
	})

	t.Run("double adopt rejection", func(t *testing.T) {
		t.Parallel()

		node := newInProcessAgentNode(t)
		application, _ := newTestAppWithDiscoveredNode(t, node.httpURL(), nil)
		request := httptest.NewRequest(http.MethodPost, "http://controller.local/api/nodes/serial-1234/adopt", nil)

		if _, err := application.adoptNode(context.Background(), request, node.nodeResponse()); err != nil {
			t.Fatalf("first adoptNode() error = %v", err)
		}
		if _, err := application.adoptNode(context.Background(), request, node.nodeResponse()); !errors.Is(err, errNodeAlreadyAdopted) {
			t.Fatalf("second adoptNode() error = %v, want errNodeAlreadyAdopted", err)
		}
	})

	t.Run("bad token", func(t *testing.T) {
		t.Parallel()

		node := newInProcessAgentNode(t)
		application, store := newTestAppWithDiscoveredNode(t, node.httpURL(), nil)
		if _, err := application.adoptNode(context.Background(), httptest.NewRequest(http.MethodPost, "http://controller.local/api/nodes/serial-1234/adopt", nil), node.nodeResponse()); err != nil {
			t.Fatalf("adoptNode() error = %v", err)
		}

		trust, err := store.LoadNodeTrust(context.Background(), "serial-1234")
		if err != nil {
			t.Fatalf("LoadNodeTrust() error = %v", err)
		}
		trust.APITokenEnc = mustSealString(t, application.vault, "wrong-token")
		if err := store.SaveNodeTrust(context.Background(), "serial-1234", trust); err != nil {
			t.Fatalf("SaveNodeTrust() error = %v", err)
		}

		if _, err := application.fetchTrustedNodeHealth(context.Background(), "serial-1234"); !errors.Is(err, errTrustedNodeUnauthorized) {
			t.Fatalf("fetchTrustedNodeHealth() error = %v, want errTrustedNodeUnauthorized", err)
		}
	})

	t.Run("fingerprint mismatch", func(t *testing.T) {
		t.Parallel()

		node := newInProcessAgentNode(t)
		application, store := newTestAppWithDiscoveredNode(t, node.httpURL(), nil)
		if _, err := application.adoptNode(context.Background(), httptest.NewRequest(http.MethodPost, "http://controller.local/api/nodes/serial-1234/adopt", nil), node.nodeResponse()); err != nil {
			t.Fatalf("adoptNode() error = %v", err)
		}

		trust, err := store.LoadNodeTrust(context.Background(), "serial-1234")
		if err != nil {
			t.Fatalf("LoadNodeTrust() error = %v", err)
		}
		trust.TLSFingerprint = strings.Repeat("0", 64)
		if err := store.SaveNodeTrust(context.Background(), "serial-1234", trust); err != nil {
			t.Fatalf("SaveNodeTrust() error = %v", err)
		}

		if _, err := application.fetchTrustedNodeHealth(context.Background(), "serial-1234"); !errors.Is(err, errTrustedNodeFingerprintDrift) {
			t.Fatalf("fetchTrustedNodeHealth() error = %v, want errTrustedNodeFingerprintDrift", err)
		}
	})
}

func computeFingerprintHex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return strings.ToLower(fmt.Sprintf("%x", sum[:]))
}

func newTestAppWithDiscoveredNode(t *testing.T, agentURL string, tlsAgent *httptest.Server) (*app, *registry.Store) {
	t.Helper()

	hostPort := strings.TrimPrefix(agentURL, "http://")
	host, portText, _ := strings.Cut(hostPort, ":")
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	tempDir := t.TempDir()
	store, err := registry.Open(filepath.Join(tempDir, "controller.db"))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
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
	authority, err := ca.Ensure(filepath.Join(tempDir, "ca"))
	if err != nil {
		t.Fatalf("ensure CA: %v", err)
	}
	vault, err := securestore.Ensure(filepath.Join(tempDir, "vault"))
	if err != nil {
		t.Fatalf("ensure secure store: %v", err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	if tlsAgent != nil {
		client = tlsAgent.Client()
	}
	application := &app{
		config: config{
			aggregateNUTEnabled: true,
			aggregateNUTListen:  ":3493",
		},
		registry:  store,
		browser:   browse.New(nil),
		ca:        authority,
		client:    client,
		vault:     vault,
		aggregate: aggregatenut.NewManager(nil),
	}
	t.Cleanup(application.aggregate.Close)
	return application, store
}

func saveTestTrust(t *testing.T, application *app, store *registry.Store, tlsAgent *httptest.Server, apiToken string) error {
	t.Helper()
	return store.SaveNodeTrust(context.Background(), "serial-1234", registry.Trust{
		ControllerURL:  "http://controller.local",
		TLSPort:        tlsServerPort(t, tlsAgent),
		TLSFingerprint: computeFingerprintHex(tlsAgent.Certificate().Raw),
		NUTUser:        "controller",
		APITokenEnc:    mustSealString(t, application.vault, apiToken),
		NUTPasswordEnc: mustSealString(t, application.vault, "nut-secret"),
	})
}

func tlsServerPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	hostPort := strings.TrimPrefix(server.URL, "https://")
	_, portText, _ := strings.Cut(hostPort, ":")
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse TLS port: %v", err)
	}
	return port
}

func mustSealString(t *testing.T, vault *securestore.Store, value string) string {
	t.Helper()
	sealed, err := vault.SealString(value)
	if err != nil {
		t.Fatalf("SealString() error = %v", err)
	}
	return sealed
}

type fakeAlertDeliverer struct{}

func (f *fakeAlertDeliverer) Deliver(context.Context, registry.AlertRule, registry.AlertEvent) error {
	return nil
}

func setBrowserSnapshot(t *testing.T, browser *browse.Browser, nodes ...browse.LiveNode) {
	t.Helper()
	if browser == nil {
		t.Fatal("browser = nil")
	}
	values := make(map[string]browse.LiveNode, len(nodes))
	for _, node := range nodes {
		values[node.ID] = node
	}
	field := reflect.ValueOf(browser).Elem().FieldByName("nodes")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(values))
}

type inProcessAgentNode struct {
	host         string
	httpPort     int
	tlsPort      int
	configDir    string
	adoptionPath string
	agentBinary  string
	httpServer   *http.Server
	tlsServer    *http.Server
	reloader     *noopReloader
	adopted      *adoptedRecorder
}

type noopReloader struct {
	calls int
}

func (n *noopReloader) Reload(context.Context, bool, []string) error {
	n.calls++
	return nil
}

type adoptedRecorder struct {
	values []bool
}

func (a *adoptedRecorder) UpdateAdopted(value bool) {
	a.values = append(a.values, value)
}

func newInProcessAgentNode(t *testing.T) *inProcessAgentNode {
	t.Helper()

	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "nut")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen http: %v", err)
	}
	tlsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = httpListener.Close()
		t.Fatalf("listen tls: %v", err)
	}
	host := "127.0.0.1"
	httpPort := httpListener.Addr().(*net.TCPAddr).Port
	tlsPort := tlsListener.Addr().(*net.TCPAddr).Port
	reloader := &noopReloader{}
	adopted := &adoptedRecorder{}
	adoptionPath := filepath.Join(tempDir, "adoption.json")
	agentBinaryPath := filepath.Join(tempDir, "wattkeeper-agent")
	if err := os.WriteFile(agentBinaryPath, []byte("original-agent-binary"), 0o755); err != nil {
		t.Fatalf("write test agent binary: %v", err)
	}
	tlsCertPath := filepath.Join(tempDir, "node-api.crt")
	tlsKeyPath := filepath.Join(tempDir, "node-api.key")
	adopter := &nodeapi.RuntimeAdopter{
		ConfigDir:    configDir,
		AdoptionPath: adoptionPath,
		Reloader:     reloader,
		Advertiser:   adopted,
		Version:      "v0.3.0",
		Serial:       "serial-1234",
		TLSPort:      tlsPort,
		TLSCertPath:  tlsCertPath,
		TLSKeyPath:   tlsKeyPath,
	}
	service := nodeapi.New(nil, nodeapi.Options{
		Version:      "v0.3.0",
		Serial:       "serial-1234",
		StartedAt:    time.Now().Add(-15 * time.Second),
		RootPath:     tempDir,
		AdoptionPath: adoptionPath,
		AuthPath:     filepath.Join(tempDir, "webui-auth.json"),
		AgentBinary:  agentBinaryPath,
		Adopter:      adopter,
	})
	httpServer := &http.Server{Handler: service.Handler()}
	tlsServer := &http.Server{Handler: service.Handler(), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: nodeapi.DynamicCertificateLoader(tlsCertPath, tlsKeyPath)}}
	go func() { _ = httpServer.Serve(httpListener) }()
	go func() { _ = tlsServer.ServeTLS(tlsListener, "", "") }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		_ = tlsServer.Shutdown(shutdownCtx)
	})
	return &inProcessAgentNode{
		host:         host,
		httpPort:     httpPort,
		tlsPort:      tlsPort,
		configDir:    configDir,
		adoptionPath: adoptionPath,
		agentBinary:  agentBinaryPath,
		httpServer:   httpServer,
		tlsServer:    tlsServer,
		reloader:     reloader,
		adopted:      adopted,
	}
}

func (n *inProcessAgentNode) httpURL() string {
	return fmt.Sprintf("http://%s:%d", n.host, n.httpPort)
}

func (n *inProcessAgentNode) nodeResponse() nodeResponse {
	return nodeResponse{ID: "serial-1234", Address: n.host, Port: n.httpPort, Live: true}
}
