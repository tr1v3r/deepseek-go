package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultBaseURL       = "https://api.deepseek.com"
	defaultResponseBytes = 16 << 20
	maxErrorBytes        = 64 << 10
)

// Config configures a reusable Client. The HTTPClient is shallow-copied; its
// Transport is shared and must be concurrency-safe. Redirects are rejected.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client

	// MaxRetries opts GET requests into retries (0 disables, maximum 10).
	// Generation POST requests and active streams are never replayed by the SDK.
	MaxRetries int

	// MaxResponseBytes limits a JSON response (default 16 MiB).
	MaxResponseBytes int64

	// MaxEventBytes limits both an SSE line and the accumulated event (default 4 MiB).
	MaxEventBytes int
}

// Client can be shared by goroutines. Treat request slices and maps as immutable
// while a request is running. Create clients once to reuse HTTP connections.
type Client struct {
	key, base        string
	http             *http.Client
	maxRetries       int
	maxResponseBytes int64
	maxEventBytes    int
}

// NewClient validates configuration and creates a reusable HTTP client.
func NewClient(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" || strings.ContainsAny(cfg.APIKey, "\r\n") {
		return nil, &ValidationError{"api_key", "must be nonempty without newlines"}
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}

	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" ||
		(u.Scheme != "https" && u.Scheme != "http") ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.User != nil {
		return nil, &ValidationError{"base_url", "requires an HTTP(S) URL without credentials, query, or fragment"}
	}

	if cfg.MaxRetries < 0 || cfg.MaxRetries > 10 {
		return nil, &ValidationError{"max_retries", "must be between 0 and 10"}
	}

	if cfg.MaxResponseBytes < 0 || cfg.MaxEventBytes < 0 {
		return nil, &ValidationError{"limits", "must not be negative"}
	}

	if cfg.MaxResponseBytes == 0 {
		cfg.MaxResponseBytes = defaultResponseBytes
	}

	if cfg.MaxResponseBytes > 1<<30 || cfg.MaxEventBytes > 1<<30 {
		return nil, &ValidationError{"limits", "must not exceed 1 GiB"}
	}

	if cfg.MaxEventBytes == 0 {
		cfg.MaxEventBytes = maxEventBytes
	}

	hc := http.Client{Timeout: 10 * time.Minute}
	if cfg.HTTPClient != nil {
		hc = *cfg.HTTPClient
	}

	// No redirected POST replay or credential forwarding. Do not mutate caller's client.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		key:              cfg.APIKey,
		base:             strings.TrimRight(cfg.BaseURL, "/"),
		http:             &hc,
		maxRetries:       cfg.MaxRetries,
		maxResponseBytes: cfg.MaxResponseBytes,
		maxEventBytes:    cfg.MaxEventBytes,
	}, nil
}

func (c *Client) request(ctx context.Context, method, path string, payload any, stream bool) (*http.Response, error) {
	if ctx == nil {
		return nil, &ValidationError{"context", "must not be nil"}
	}

	var encoded []byte
	if payload != nil {
		var err error
		encoded, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("deepseek: encode request: %w", err)
		}
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(encoded))
		if err != nil {
			return nil, err
		}

		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "deepseek-go/0.1.0-dev")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		if stream {
			req.Header.Set("Accept", "text/event-stream")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("deepseek: request: %w", err)
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		b, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes+1))
		_ = resp.Body.Close()
		truncated := len(b) > maxErrorBytes
		if truncated {
			b = b[:maxErrorBytes]
		}

		e := decodeAPIError(b)
		e.StatusCode = resp.StatusCode
		e.RequestID = resp.Header.Get("X-Request-Id")
		e.Header = resp.Header.Clone()
		e.BodyTruncated = truncated
		if e.Message == "" {
			e.Message = http.StatusText(resp.StatusCode)
		}

		if readErr != nil {
			return nil, errors.Join(e, readErr)
		}

		if method != http.MethodGet || attempt >= c.maxRetries || !e.Retryable() {
			return nil, e
		}

		delay, ok := retryDelay(resp.Header.Get("Retry-After"), attempt, time.Now())
		if !ok {
			return nil, e
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) json(ctx context.Context, method, path string, payload, out any, requiredField string) (ResponseMetadata, error) {
	resp, err := c.request(ctx, method, path, payload, false)
	if err != nil {
		return ResponseMetadata{}, err
	}

	defer resp.Body.Close()
	meta := metadata(resp)
	b, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
	if err != nil {
		return meta, &ProtocolError{
			ResponseMetadata: meta,
			Message:          "reading JSON response failed",
			Cause:            err,
		}
	}

	if int64(len(b)) > c.maxResponseBytes {
		return meta, &ProtocolError{
			ResponseMetadata: meta,
			Message:          "JSON response exceeds configured limit",
		}
	}

	// Require an object: JSON null/arrays and trailing garbage are not valid API results.
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return meta, &ProtocolError{
			ResponseMetadata: meta,
			Message:          "expected a JSON object",
		}
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(b, &envelope); err != nil {
		return meta, &ProtocolError{
			ResponseMetadata: meta,
			Message:          "invalid JSON response",
			Cause:            err,
		}
	}

	if raw := bytes.TrimSpace(envelope["error"]); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		return meta, &ProtocolError{
			ResponseMetadata: meta,
			Message:          "unexpected error object in successful HTTP response",
		}
	}

	required := bytes.TrimSpace(envelope[requiredField])
	if len(required) == 0 || bytes.Equal(required, []byte("null")) {
		return meta, &ProtocolError{
			ResponseMetadata: meta,
			Message:          "response is missing required field " + requiredField,
		}
	}

	if err = json.Unmarshal(b, out); err != nil {
		return meta, &ProtocolError{
			ResponseMetadata: meta,
			Message:          "invalid JSON response",
			Cause:            err,
		}
	}

	return meta, nil
}

// CreateChatCompletion performs exactly one generation attempt at SDK level.
func (c *Client) CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletion, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	req.Stream, req.StreamOptions = false, nil
	var out ChatCompletion
	meta, err := c.json(ctx, http.MethodPost, "/chat/completions", req, &out, "choices")
	if err != nil {
		return nil, err
	}

	out.ResponseMetadata = meta
	return &out, nil
}

// ListModels retrieves model capabilities from the server.
func (c *Client) ListModels(ctx context.Context) (*ModelList, error) {
	var out ModelList
	meta, err := c.json(ctx, http.MethodGet, "/models", nil, &out, "data")
	if err != nil {
		return nil, err
	}

	out.ResponseMetadata = meta
	return &out, nil
}

// GetBalance retrieves current account availability and balances.
func (c *Client) GetBalance(ctx context.Context) (*Balance, error) {
	var out Balance
	meta, err := c.json(ctx, http.MethodGet, "/user/balance", nil, &out, "is_available")
	if err != nil {
		return nil, err
	}

	out.ResponseMetadata = meta
	return &out, nil
}
