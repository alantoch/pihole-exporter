package pihole

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSessionAuthenticatesAndCachesToken(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		assertAuthRequest(t, r)
		writeAuthResponse(t, w, "sid-1", "csrf-1", 300)
	}))
	defer server.Close()

	client, err := NewAuthClient(server.URL, "app-password")
	if err != nil {
		t.Fatalf("NewAuthClient() error = %v", err)
	}
	client.now = fixedClock(time.Unix(1000, 0))

	first, err := client.Session(context.Background())
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	second, err := client.Session(context.Background())
	if err != nil {
		t.Fatalf("Session() second call error = %v", err)
	}

	if first.SID != "sid-1" || second.SID != "sid-1" {
		t.Fatalf("unexpected SID values: first=%q second=%q", first.SID, second.SID)
	}
	if requests != 1 {
		t.Fatalf("expected one auth request, got %d", requests)
	}
}

func TestSessionRefreshesWhenTokenIsNearExpiry(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		assertAuthRequest(t, r)
		writeAuthResponse(t, w, "sid-"+string(rune('0'+requests)), "csrf", 60)
	}))
	defer server.Close()

	now := time.Unix(1000, 0)
	client, err := NewAuthClient(server.URL, "app-password", WithRefreshSkew(30*time.Second))
	if err != nil {
		t.Fatalf("NewAuthClient() error = %v", err)
	}
	client.now = func() time.Time { return now }

	first, err := client.Session(context.Background())
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}

	now = now.Add(31 * time.Second)

	second, err := client.Session(context.Background())
	if err != nil {
		t.Fatalf("Session() refresh error = %v", err)
	}

	if first.SID == second.SID {
		t.Fatalf("expected refreshed SID, got %q twice", first.SID)
	}
	if requests != 2 {
		t.Fatalf("expected two auth requests, got %d", requests)
	}
}

func TestAddAuthHeaders(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAuthResponse(t, w, "sid-1", "csrf-1", 300)
	}))
	defer server.Close()

	client, err := NewAuthClient(server.URL, "app-password")
	if err != nil {
		t.Fatalf("NewAuthClient() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/stats/summary", nil)
	if err := client.AddAuthHeaders(context.Background(), req); err != nil {
		t.Fatalf("AddAuthHeaders() error = %v", err)
	}

	if got := req.Header.Get("X-FTL-SID"); got != "sid-1" {
		t.Fatalf("X-FTL-SID = %q, want sid-1", got)
	}
	if got := req.Header.Get("X-FTL-CSRF"); got != "csrf-1" {
		t.Fatalf("X-FTL-CSRF = %q, want csrf-1", got)
	}
}

func TestNewAuthClientFromEnv(t *testing.T) {
	t.Setenv(DefaultAppPasswordEnv, "from-env")

	client, err := NewAuthClientFromEnv("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("NewAuthClientFromEnv() error = %v", err)
	}
	if client.password != "from-env" {
		t.Fatalf("password = %q, want from-env", client.password)
	}
}

func TestNewAuthClientRequiresPassword(t *testing.T) {
	t.Parallel()

	_, err := NewAuthClient("http://127.0.0.1:8080", "")
	if err != ErrMissingPassword {
		t.Fatalf("NewAuthClient() error = %v, want ErrMissingPassword", err)
	}
}

func TestSessionReturnsAPIErrorMessage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"key":     "unauthorized",
				"message": "Unauthorized",
				"hint":    nil,
			},
		})
	}))
	defer server.Close()

	client, err := NewAuthClient(server.URL, "bad-password")
	if err != nil {
		t.Fatalf("NewAuthClient() error = %v", err)
	}

	_, err = client.Session(context.Background())
	if err == nil {
		t.Fatal("Session() error = nil, want error")
	}
}

func TestLiveAuth(t *testing.T) {
	if os.Getenv("PIHOLE_LIVE_TEST") != "1" {
		t.Skip("set PIHOLE_LIVE_TEST=1 to run against a real Pi-hole")
	}

	client, err := NewAuthClientFromEnv(envOrDefault("PIHOLE_BASE_URL", "http://192.168.0.2:9000"))
	if err != nil {
		t.Fatalf("NewAuthClientFromEnv() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := client.Session(ctx)
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	if session.SID == "" {
		t.Fatal("Session().SID is empty")
	}
}

func assertAuthRequest(t *testing.T, r *http.Request) {
	t.Helper()

	if r.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", r.Method)
	}
	if r.URL.Path != "/api/auth" {
		t.Fatalf("path = %s, want /api/auth", r.URL.Path)
	}

	var body authRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode auth request body: %v", err)
	}
	if body.Password != "app-password" {
		t.Fatalf("password = %q, want app-password", body.Password)
	}
}

func writeAuthResponse(t *testing.T, w http.ResponseWriter, sid, csrf string, validity int) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(map[string]any{
		"session": map[string]any{
			"valid":    true,
			"totp":     false,
			"sid":      sid,
			"csrf":     csrf,
			"validity": validity,
		},
		"took": 0.0002,
	})
	if err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time {
		return t
	}
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
