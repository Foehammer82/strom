package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Foehammer82/strom/agent/internal/updates"
)

func buildTestAgentArchive(t *testing.T, binaryContent []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "strom-agent-v1.1.0-linux-arm64/strom-agent", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(binaryContent))}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(binaryContent); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

func testSHA256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// newFakeReleaseServer serves a single stable release with a manifest signed
// by priv, matching the "linux/arm64" artifact the test Checker targets.
func newFakeReleaseServer(t *testing.T, priv ed25519.PrivateKey, version string, archiveBytes []byte, binaryContent []byte) *httptest.Server {
	t.Helper()

	artifact := updates.Artifact{
		OS:           "linux",
		Arch:         "arm64",
		Filename:     "strom-agent-" + version + "-linux-arm64.tar.gz",
		Size:         int64(len(archiveBytes)),
		SHA256:       testSHA256Hex(archiveBytes),
		BinarySHA256: testSHA256Hex(binaryContent),
	}
	manifest := updates.Manifest{SchemaVersion: updates.ManifestSchemaVersion, Version: version, KeyID: "test-key", Artifacts: []updates.Artifact{artifact}}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	signature := ed25519.Sign(priv, manifestBytes)

	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		release := map[string]any{
			"tag_name":   version,
			"draft":      false,
			"prerelease": false,
			"html_url":   "https://github.com/owner/repo/releases/" + version,
			"assets": []map[string]any{
				{"name": "strom-agent-manifest.json", "size": len(manifestBytes), "browser_download_url": server.URL + "/assets/manifest.json"},
				{"name": "strom-agent-manifest.json.sig", "size": len(signature), "browser_download_url": server.URL + "/assets/manifest.json.sig"},
				{"name": artifact.Filename, "size": len(archiveBytes), "browser_download_url": server.URL + "/assets/" + artifact.Filename},
			},
		}
		_ = json.NewEncoder(w).Encode(release)
	})
	mux.HandleFunc("/assets/manifest.json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(manifestBytes) })
	mux.HandleFunc("/assets/manifest.json.sig", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(signature) })
	mux.HandleFunc("/assets/"+artifact.Filename, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archiveBytes) })

	server = httptest.NewServer(mux)
	return server
}

func newTestUpdatesChecker(t *testing.T, updatesRoot string, server *httptest.Server, pub ed25519.PublicKey, installedVersion string) *updates.Checker {
	t.Helper()
	github := &updates.GitHubClient{
		BaseURL:             server.URL,
		Repository:          "owner/repo",
		ValidateDownloadURL: func(string) error { return nil },
	}
	return &updates.Checker{
		Store:            updates.NewStore(updatesRoot),
		GitHub:           github,
		PublicKey:        pub,
		InstalledVersion: installedVersion,
		GOOS:             "linux",
		GOARCH:           "arm64",
	}
}

func TestHandleUpdatesStatusRequiresReadAccess(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json"), UpdatesRoot: filepath.Join(tempDir, "updates")})

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/agent/updates/status", nil)
	unauthenticatedRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(unauthenticatedRecorder, unauthenticated)
	if unauthenticatedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthenticatedRecorder.Code, http.StatusUnauthorized)
	}

	cookies := loginAsDefaultAdmin(t, service)
	authenticated := httptest.NewRequest(http.MethodGet, "/api/agent/updates/status", nil)
	authenticated.AddCookie(cookieByName(cookies, sessionCookieName))
	authenticatedRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(authenticatedRecorder, authenticated)
	if authenticatedRecorder.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want %d body=%s", authenticatedRecorder.Code, http.StatusOK, authenticatedRecorder.Body.String())
	}

	var response updatesStatusResponse
	if err := json.Unmarshal(authenticatedRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.UpdatesSupported {
		t.Fatal("expected UpdatesSupported to be false when no checker is configured")
	}
}

func TestHandleUpdatesCheckAndInstallEndToEnd(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	binaryContent := []byte("fake-binary-v1.1.0")
	archive := buildTestAgentArchive(t, binaryContent)
	server := newFakeReleaseServer(t, priv, "v1.1.0", archive, binaryContent)
	defer server.Close()

	tempDir := t.TempDir()
	updatesRoot := filepath.Join(tempDir, "updates")
	checker := newTestUpdatesChecker(t, updatesRoot, server, pub, "v1.0.0")

	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json"), UpdatesRoot: updatesRoot, UpdatesChecker: checker})
	cookies := loginAsDefaultAdmin(t, service)
	sessionCookie := cookieByName(cookies, sessionCookieName)

	checkRequest := httptest.NewRequest(http.MethodPost, "/api/agent/updates/check", nil)
	checkRequest.AddCookie(sessionCookie)
	checkRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(checkRecorder, checkRequest)
	if checkRecorder.Code != http.StatusOK {
		t.Fatalf("check status = %d, want %d body=%s", checkRecorder.Code, http.StatusOK, checkRecorder.Body.String())
	}
	var checkResponse updatesStatusResponse
	if err := json.Unmarshal(checkRecorder.Body.Bytes(), &checkResponse); err != nil {
		t.Fatalf("decode check response: %v", err)
	}
	if checkResponse.AvailableVersion != "v1.1.0" {
		t.Fatalf("AvailableVersion = %q, want v1.1.0", checkResponse.AvailableVersion)
	}

	installRequest := httptest.NewRequest(http.MethodPost, "/api/agent/updates/install", strings.NewReader(`{"version":"v1.1.0"}`))
	installRequest.Header.Set("Content-Type", "application/json")
	installRequest.AddCookie(sessionCookie)
	installRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(installRecorder, installRequest)
	if installRecorder.Code != http.StatusOK {
		t.Fatalf("install status = %d, want %d body=%s", installRecorder.Code, http.StatusOK, installRecorder.Body.String())
	}
	var installResponse updatesInstallResponse
	if err := json.Unmarshal(installRecorder.Body.Bytes(), &installResponse); err != nil {
		t.Fatalf("decode install response: %v", err)
	}
	if installResponse.Version != "v1.1.0" || !installResponse.RestartRequired {
		t.Fatalf("install response = %+v", installResponse)
	}

	installedVersion, ok := service.updatesStore.InstalledVersion()
	if !ok || installedVersion != "v1.1.0" {
		t.Fatalf("InstalledVersion() = %q, %v; want v1.1.0, true", installedVersion, ok)
	}
}

func TestHandleUpdatesInstallRejectsAPIKey(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	binaryContent := []byte("fake-binary-v1.1.0")
	archive := buildTestAgentArchive(t, binaryContent)
	server := newFakeReleaseServer(t, priv, "v1.1.0", archive, binaryContent)
	defer server.Close()

	tempDir := t.TempDir()
	updatesRoot := filepath.Join(tempDir, "updates")
	checker := newTestUpdatesChecker(t, updatesRoot, server, pub, "v1.0.0")
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json"), UpdatesRoot: updatesRoot, UpdatesChecker: checker})

	// Bootstrap admin, then mint a write-scoped local API key.
	cookies := loginAsDefaultAdmin(t, service)
	sessionCookie := cookieByName(cookies, sessionCookieName)

	csrfCarrier := httptest.NewRequest(http.MethodGet, "/settings", nil)
	csrfCarrier.AddCookie(sessionCookie)
	csrfToken, err := service.auth.SessionCSRFToken(csrfCarrier)
	if err != nil {
		t.Fatalf("SessionCSRFToken: %v", err)
	}
	keyRequest := httptest.NewRequest(http.MethodPost, "/settings/api-key", strings.NewReader(`{"scope":"write","action":"regenerate","password":"`+testAdminPassword+`"}`))
	keyRequest.Header.Set("Content-Type", "application/json")
	keyRequest.AddCookie(sessionCookie)
	keyRequest.Header.Set("X-CSRF-Token", csrfToken)
	keyRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(keyRecorder, keyRequest)
	if keyRecorder.Code != http.StatusOK {
		t.Fatalf("create api key status = %d, want %d body=%s", keyRecorder.Code, http.StatusOK, keyRecorder.Body.String())
	}
	var keyResponse struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(keyRecorder.Body.Bytes(), &keyResponse); err != nil {
		t.Fatalf("decode api key response: %v", err)
	}
	if keyResponse.Key == "" {
		t.Fatal("expected a write API key to be returned")
	}

	installRequest := httptest.NewRequest(http.MethodPost, "/api/agent/updates/install", strings.NewReader(`{"version":"v1.1.0"}`))
	installRequest.Header.Set("Content-Type", "application/json")
	installRequest.Header.Set("Authorization", "Bearer "+keyResponse.Key)
	installRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(installRecorder, installRequest)
	if installRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("install with API key status = %d, want %d body=%s", installRecorder.Code, http.StatusUnauthorized, installRecorder.Body.String())
	}
}

func TestHandleUpdatesCheckDisabledWithoutChecker(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json"), UpdatesRoot: filepath.Join(tempDir, "updates")})
	cookies := loginAsDefaultAdmin(t, service)

	checkRequest := httptest.NewRequest(http.MethodPost, "/api/agent/updates/check", nil)
	checkRequest.AddCookie(cookieByName(cookies, sessionCookieName))
	checkRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(checkRecorder, checkRequest)
	if checkRecorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", checkRecorder.Code, http.StatusNotFound, checkRecorder.Body.String())
	}
}
