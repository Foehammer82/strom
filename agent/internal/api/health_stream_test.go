package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAPIHealthStreamRequiresLocalSession(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/health/stream", nil)
	unauthenticatedRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(unauthenticatedRecorder, unauthenticated)
	if unauthenticatedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthenticatedRecorder.Code, http.StatusUnauthorized)
	}
}

func TestAPIHealthStreamSendsInitialHistoryEvent(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, DisableAuth: true})
	service.history.add(metricSample{
		Timestamp:       time.Now(),
		MemoryUsedBytes: 1024,
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/health/stream", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Handler().ServeHTTP(recorder, req)
	}()

	// The initial "history" event is written synchronously before the
	// handler enters its ticker loop, so a short grace period is enough to
	// observe it before cancelling the request context to stop the stream.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not exit after context cancellation")
	}

	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: history\n") {
		t.Fatalf("body = %q, want an initial history event", body)
	}
	if !strings.Contains(body, `"memory_used_bytes":1024`) {
		t.Fatalf("body = %q, want the seeded sample", body)
	}
}

func TestAPIHealthHistoryLongReturnsSnapshot(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, DisableAuth: true})
	service.longHistory.add(metricSample{
		Timestamp:       time.Now(),
		MemoryUsedBytes: 2048,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/health/history/long", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"memory_used_bytes":2048`) {
		t.Fatalf("body = %q, want the seeded long-history sample", body)
	}
}

func TestAPIHealthHistoryLongRequiresLocalSession(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})

	req := httptest.NewRequest(http.MethodGet, "/api/health/history/long", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestAPIHealthHistoryLongRejectsUnsupportedMethods(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, DisableAuth: true})

	req := httptest.NewRequest(http.MethodPost, "/api/health/history/long", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func TestAPIHealthStreamRejectsUnsupportedMethods(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, DisableAuth: true})

	req := httptest.NewRequest(http.MethodPost, "/api/health/stream", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}
