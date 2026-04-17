package pihole

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAppPasswordEnv = "PIHOLE_APP_PASSWORD"
	defaultRefreshSkew    = 30 * time.Second
)

var (
	ErrMissingPassword = errors.New("pihole app password is required")
	ErrUnauthenticated = errors.New("pihole session is not authenticated")
)

type AuthClient struct {
	baseURL     *url.URL
	password    string
	httpClient  *http.Client
	refreshSkew time.Duration
	now         func() time.Time

	mu      sync.Mutex
	session Session
}

type Session struct {
	SID       string
	CSRF      string
	Valid     bool
	TOTP      bool
	ExpiresAt time.Time
}

type AuthClientOption func(*AuthClient)

func WithHTTPClient(client *http.Client) AuthClientOption {
	return func(c *AuthClient) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func WithRefreshSkew(skew time.Duration) AuthClientOption {
	return func(c *AuthClient) {
		if skew >= 0 {
			c.refreshSkew = skew
		}
	}
}

func NewAuthClient(baseURL, password string, opts ...AuthClientOption) (*AuthClient, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, errors.New("pihole base URL is required")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse pihole base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("pihole base URL must include scheme and host: %q", baseURL)
	}

	password = strings.TrimSpace(password)
	if password == "" {
		return nil, ErrMissingPassword
	}

	client := &AuthClient{
		baseURL:     parsed,
		password:    password,
		httpClient:  http.DefaultClient,
		refreshSkew: defaultRefreshSkew,
		now:         time.Now,
	}
	for _, opt := range opts {
		opt(client)
	}

	return client, nil
}

func NewAuthClientFromEnv(baseURL string, opts ...AuthClientOption) (*AuthClient, error) {
	return NewAuthClient(baseURL, os.Getenv(DefaultAppPasswordEnv), opts...)
}

func (c *AuthClient) Session(ctx context.Context) (Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session.Valid && c.now().Add(c.refreshSkew).Before(c.session.ExpiresAt) {
		return c.session, nil
	}

	session, err := c.authenticate(ctx)
	if err != nil {
		return Session{}, err
	}
	c.session = session

	return c.session, nil
}

func (c *AuthClient) AddAuthHeaders(ctx context.Context, req *http.Request) error {
	session, err := c.Session(ctx)
	if err != nil {
		return err
	}
	if !session.Valid || session.SID == "" {
		return ErrUnauthenticated
	}

	req.Header.Set("X-FTL-SID", session.SID)
	if session.CSRF != "" {
		req.Header.Set("X-FTL-CSRF", session.CSRF)
	}

	return nil
}

func (c *AuthClient) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = Session{}
}

func (c *AuthClient) authenticate(ctx context.Context) (Session, error) {
	endpoint := c.resolve("/api/auth")

	body, err := json.Marshal(authRequest{Password: c.password})
	if err != nil {
		return Session{}, fmt.Errorf("encode auth request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Session{}, fmt.Errorf("create auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Session{}, fmt.Errorf("authenticate with pihole: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var errResp apiErrorResponse
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errResp); decodeErr == nil && errResp.Error.Message != "" {
			return Session{}, fmt.Errorf("authenticate with pihole: %s: %s", resp.Status, errResp.Error.Message)
		}
		return Session{}, fmt.Errorf("authenticate with pihole: %s", resp.Status)
	}

	var authResp authResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return Session{}, fmt.Errorf("decode auth response: %w", err)
	}

	if !authResp.Session.Valid || authResp.Session.SID == "" {
		return Session{}, ErrUnauthenticated
	}

	return Session{
		SID:       authResp.Session.SID,
		CSRF:      authResp.Session.CSRF,
		Valid:     authResp.Session.Valid,
		TOTP:      authResp.Session.TOTP,
		ExpiresAt: c.now().Add(time.Duration(authResp.Session.Validity) * time.Second),
	}, nil
}

func (c *AuthClient) resolve(path string) string {
	next := *c.baseURL
	next.Path = strings.TrimRight(next.Path, "/") + path
	next.RawQuery = ""
	next.Fragment = ""
	return next.String()
}

type authRequest struct {
	Password string `json:"password"`
}

type authResponse struct {
	Session struct {
		Valid    bool   `json:"valid"`
		TOTP     bool   `json:"totp"`
		SID      string `json:"sid"`
		CSRF     string `json:"csrf"`
		Validity int    `json:"validity"`
	} `json:"session"`
}

type apiErrorResponse struct {
	Error struct {
		Key     string `json:"key"`
		Message string `json:"message"`
		Hint    any    `json:"hint"`
	} `json:"error"`
}
