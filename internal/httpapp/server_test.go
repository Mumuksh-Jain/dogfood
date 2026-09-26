package httpapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	server, err := NewServer(Config{Port: 8080})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var res map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode json response: %v", err)
	}

	if res["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%s'", res["status"])
	}
}

func TestStaticAndIndex(t *testing.T) {
	server, err := NewServer(Config{Port: 8080})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Index test
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("index returned status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Dogfood 2026") {
		t.Errorf("index response missing expected title: %s", rr.Body.String())
	}

	// Static asset test
	reqStatic := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	rrStatic := httptest.NewRecorder()
	server.Handler().ServeHTTP(rrStatic, reqStatic)

	if rrStatic.Code != http.StatusOK {
		t.Fatalf("static asset returned status %d", rrStatic.Code)
	}
	if !strings.Contains(rrStatic.Body.String(), "--bg-color") {
		t.Errorf("static asset missing expected css: %s", rrStatic.Body.String())
	}
}
