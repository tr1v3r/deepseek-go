package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"

	"github.com/tr1v3r/deepseek-go/internal/sse"
)

const maxEventBytes = 4 << 20

// Stream reads without a background goroutine. Recv has one caller; Close may
// run concurrently to interrupt it. Metadata returns an independent snapshot.
type Stream struct {
	body      io.ReadCloser
	decoder   *sse.Decoder
	meta      ResponseMetadata
	terminal  error
	closeOnce sync.Once
	closeErr  error
}

func (c *Client) CreateChatCompletionStream(ctx context.Context, req ChatCompletionRequest) (*Stream, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	req.Stream = true
	resp, err := c.request(ctx, http.MethodPost, "/chat/completions", req, true)
	if err != nil {
		return nil, err
	}

	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "text/event-stream" {
		resp.Body.Close()
		return nil, &ProtocolError{
			ResponseMetadata: metadata(resp),
			Message:          "expected text/event-stream response",
			Cause:            err,
		}
	}

	return &Stream{
		body:    resp.Body,
		decoder: sse.New(resp.Body, c.maxEventBytes),
		meta:    metadata(resp),
	}, nil
}

func (s *Stream) Metadata() ResponseMetadata {
	snapshot := s.meta
	snapshot.Header = snapshot.Header.Clone()

	return snapshot
}

func (s *Stream) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.body.Close() })
	return s.closeErr
}

func (s *Stream) finish(err error) (*ChatCompletionChunk, error) {
	s.terminal = err
	_ = s.Close()
	return nil, err
}

// Recv returns io.EOF only after [DONE]; an earlier disconnect returns
// io.ErrUnexpectedEOF. Accumulator can combine text and indexed tool deltas.
func (s *Stream) Recv() (*ChatCompletionChunk, error) {
	if s.terminal != nil {
		return nil, s.terminal
	}

	event, err := s.decoder.Next()
	if errors.Is(err, io.EOF) {
		return s.finish(io.ErrUnexpectedEOF)
	}

	if err != nil {
		return s.finish(&ProtocolError{
			ResponseMetadata: s.Metadata(),
			Message:          "reading SSE event failed",
			Cause:            err,
		})
	}

	if strings.TrimSpace(event.Data) == "[DONE]" {
		return s.finish(io.EOF)
	}

	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Choices json.RawMessage `json:"choices"`
	}

	if err = json.Unmarshal([]byte(event.Data), &envelope); err != nil {
		return s.finish(&ProtocolError{
			ResponseMetadata: s.Metadata(),
			Message:          "invalid SSE JSON",
			Cause:            err,
		})
	}

	if event.Type == "error" || len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		e := decodeAPIError([]byte(event.Data))
		if e.Message == "" {
			e.Message = "stream error"
		}

		e.RequestID = s.meta.RequestID
		e.Header = s.meta.Header.Clone()
		if len(e.Body) > maxErrorBytes {
			e.Body = e.Body[:maxErrorBytes]
			e.BodyTruncated = true
		}

		return s.finish(e)
	}

	if len(envelope.Choices) == 0 || string(envelope.Choices) == "null" {
		return s.finish(&ProtocolError{
			ResponseMetadata: s.Metadata(),
			Message:          "chunk is missing choices",
		})
	}

	var chunk ChatCompletionChunk
	if err = json.Unmarshal([]byte(event.Data), &chunk); err != nil {
		return s.finish(&ProtocolError{
			ResponseMetadata: s.Metadata(),
			Message:          "invalid completion chunk",
			Cause:            err,
		})
	}

	return &chunk, nil
}
