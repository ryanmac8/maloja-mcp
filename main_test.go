package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ryanmac8/maloja-mcp/internal/maloja"
)

func testHandler(t *testing.T, token string) (http.Handler, *httptest.Server) {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"x","versionstring":"3.2.9","db_status":{"healthy":true,"complete":true}}`))
	}))
	t.Cleanup(backend.Close)
	client, err := maloja.New(backend.URL, "", backend.Client())
	if err != nil {
		t.Fatal(err)
	}
	return newHandler(config{AuthToken: token}, client, time.UTC), backend
}

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

func mcpRequest(auth string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initBody))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	return r
}

func TestAuthRequired(t *testing.T) {
	h, _ := testHandler(t, "s3cret")
	for _, auth := range []string{"", "Bearer wrong", "s3cret"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, mcpRequest(auth))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("auth %q: status %d, want 401", auth, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mcpRequest("Bearer s3cret"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"maloja-mcp"`) {
		t.Errorf("authorised initialize: %d %s", rec.Code, rec.Body.String())
	}
}

func TestNoAuthWhenTokenUnset(t *testing.T) {
	h, _ := testHandler(t, "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mcpRequest(""))
	if rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHealthz(t *testing.T) {
	h, _ := testHandler(t, "s3cret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"maloja_version":"3.2.9"`) {
		t.Errorf("healthz: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("MALOJA_URL", "")
	if _, err := loadConfig(); err == nil {
		t.Error("missing MALOJA_URL should fail")
	}
	t.Setenv("MALOJA_URL", "http://m:42010")
	t.Setenv("TZ", "")
	t.Setenv("MALOJA_TIMEZONE", "")
	c, err := loadConfig()
	if err != nil || c.ListenAddr != ":8080" || c.TimeZone != "UTC" {
		t.Errorf("defaults = %+v, %v", c, err)
	}
}
