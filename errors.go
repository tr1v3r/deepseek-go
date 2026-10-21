package deepseek

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ValidationError reports a local request/configuration error before any HTTP call.
type ValidationError struct{ Field, Message string }

func (e *ValidationError) Error() string {
	return "deepseek: " + e.Field + ": " + e.Message
}

// ProtocolError reports malformed or oversized server data. Unwrap retains the cause.
// ResponseMetadata is populated when an HTTP response has been received.
type ProtocolError struct {
	ResponseMetadata

	Message string
	Cause   error
}

func (e *ProtocolError) Error() string {
	return "deepseek: protocol: " + e.Message
}

func (e *ProtocolError) Unwrap() error {
	return e.Cause
}

// ResponseMetadata contains a snapshot of HTTP response details. Headers may contain
// sensitive values. The SDK never logs them. Mutations do not affect the transport.
type ResponseMetadata struct {
	StatusCode int
	RequestID  string
	Header     http.Header
}

func metadata(r *http.Response) ResponseMetadata {
	return ResponseMetadata{
		StatusCode: r.StatusCode,
		RequestID:  r.Header.Get("X-Request-Id"),
		Header:     r.Header.Clone(),
	}
}

// APIError describes a non-2xx response or a stream error (StatusCode=0).
// Body is limited to 64 KiB; BodyTruncated indicates discarded bytes.
type APIError struct {
	StatusCode    int             `json:"-"`
	RequestID     string          `json:"-"`
	Header        http.Header     `json:"-"`
	Message       string          `json:"message"`
	Type          string          `json:"type"`
	Code          json.RawMessage `json:"code"`
	Param         json.RawMessage `json:"param"`
	Body          string          `json:"-"`
	BodyTruncated bool            `json:"-"`
}

func (e *APIError) Error() string {
	if e.StatusCode == 0 {
		return "deepseek: stream error: " + e.Message
	}

	return fmt.Sprintf("deepseek: HTTP %d: %s", e.StatusCode, e.Message)
}

// Retryable describes transient status, not whether replaying a request is safe.
func (e *APIError) Retryable() bool {
	return retryStatus(e.StatusCode)
}

func decodeAPIError(b []byte) *APIError {
	e := &APIError{}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}

	if json.Unmarshal(b, &envelope) == nil && len(envelope.Error) > 0 {
		if json.Unmarshal(envelope.Error, e) != nil {
			_ = json.Unmarshal(envelope.Error, &e.Message)
		}
	}

	e.Body = string(b)
	return e
}
