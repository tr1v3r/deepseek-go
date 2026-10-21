package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEmptyTextPartRetainsRequiredField(t *testing.T) {
	part := ContentPart{Type: "text", Text: ""}
	encoded, err := json.Marshal(part)
	if err != nil {
		t.Fatal(err)
	}

	if string(encoded) != `{"type":"text","text":""}` {
		t.Fatalf("empty text must be explicit: %s", encoded)
	}

	image := ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: "https://example.com/image.png"}}
	encoded, err = json.Marshal(image)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(encoded), `"text"`) {
		t.Fatalf("image part unexpectedly contains text: %s", encoded)
	}
}

func TestOversizedRetryAfterDoesNotRetryEarly(t *testing.T) {
	for _, header := range []string{
		"9223372036854775808",
		"18446744073709551616",
		strings.Repeat("9", 100),
	} {
		t.Run(header, func(t *testing.T) {
			if delay, retry := retryDelay(header, 0, time.Now()); retry {
				t.Fatalf("oversized delay triggered retry after %v", delay)
			}
		})
	}
}

func TestAccumulatorRetainsLastNonNullLogprobs(t *testing.T) {
	var accumulator Accumulator
	original := json.RawMessage(`{"content":[{"token":"hi","logprob":-0.1}]}`)

	for _, logprobs := range []json.RawMessage{original, nil, json.RawMessage(" null \n")} {
		chunk := ChatCompletionChunk{
			ID:      "completion",
			Choices: []ChunkChoice{{Index: 0, Logprobs: logprobs}},
		}
		if err := accumulator.Add(chunk); err != nil {
			t.Fatal(err)
		}
	}

	result := accumulator.Completion()
	if string(result.Choices[0].Logprobs) != string(original) {
		t.Fatalf("previous logprobs lost: %s", result.Choices[0].Logprobs)
	}
}

func TestAccumulatorRejectsConflictingIdentityAtomically(t *testing.T) {
	initialTool := ToolCall{
		Index:    Ptr(0),
		ID:       "call-1",
		Type:     "function",
		Function: FunctionCall{Name: "lookup", Arguments: "{"},
	}
	initial := ChatCompletionChunk{
		ID: "completion",
		Choices: []ChunkChoice{{
			Index: 0,
			Delta: Delta{Content: Ptr("original"), ToolCalls: []ToolCall{initialTool}},
		}},
	}

	for _, field := range []string{"id", "type"} {
		t.Run(field, func(t *testing.T) {
			var accumulator Accumulator
			if err := accumulator.Add(initial); err != nil {
				t.Fatal(err)
			}

			before := accumulator.Completion()
			conflicting := initialTool
			if field == "id" {
				conflicting.ID = "call-2"
			} else {
				conflicting.Type = "other"
			}

			chunk := ChatCompletionChunk{
				ID:    "completion",
				Usage: &Usage{TotalTokens: 99},
				Choices: []ChunkChoice{{
					Index: 0,
					Delta: Delta{Content: Ptr("must not append"), ToolCalls: []ToolCall{conflicting}},
				}},
			}
			if err := accumulator.Add(chunk); err == nil {
				t.Fatal("conflicting tool identity accepted")
			}

			if after := accumulator.Completion(); !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected chunk changed state: before=%+v after=%+v", before, after)
			}
		})
	}

	t.Run("within one chunk", func(t *testing.T) {
		var accumulator Accumulator
		secondTool := initialTool
		secondTool.ID = "call-2"
		chunk := ChatCompletionChunk{
			Choices: []ChunkChoice{{
				Delta: Delta{ToolCalls: []ToolCall{initialTool, secondTool}},
			}},
		}

		if err := accumulator.Add(chunk); err == nil {
			t.Fatal("conflicting identities within one chunk accepted")
		}

		if len(accumulator.Completion().Choices) != 0 {
			t.Fatal("rejected chunk created a partial choice")
		}
	})
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (body *trackedBody) Close() error {
	body.closed = true
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (transport roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestMalformedSuccessKeepsMetadataAndClosesBody(t *testing.T) {
	endpoints := []struct {
		name string
		call func(*Client) error
	}{
		{
			name: "chat",
			call: func(client *Client) error {
				_, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{
					Model:    "test",
					Messages: []Message{UserMessage("hi")},
				})
				return err
			},
		},
		{
			name: "models",
			call: func(client *Client) error {
				_, err := client.ListModels(context.Background())
				return err
			},
		},
		{
			name: "balance",
			call: func(client *Client) error {
				_, err := client.GetBalance(context.Background())
				return err
			},
		},
	}

	for _, endpoint := range endpoints {
		for _, wire := range []string{
			`{}`,
			`{"error":{"message":"failed"}}`,
			`{"choices":null,"data":null,"is_available":null}`,
			`{"choices":[],"data":[],"is_available":false,"error":"failed"}`,
			`{"choices":`,
			strings.Repeat("x", 200),
		} {
			t.Run(endpoint.name+"/"+wire, func(t *testing.T) {
				body := &trackedBody{Reader: strings.NewReader(wire)}
				client, err := NewClient(Config{
					APIKey:           "test",
					MaxResponseBytes: 100,
					HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: http.StatusOK,
							Header:     http.Header{"X-Request-Id": {"request-123"}},
							Body:       body,
						}, nil
					})},
				})
				if err != nil {
					t.Fatal(err)
				}

				err = endpoint.call(client)
				var protocolError *ProtocolError
				if !errors.As(err, &protocolError) {
					t.Fatalf("malformed success accepted: %v", err)
				}

				if protocolError.RequestID != "request-123" || protocolError.StatusCode != http.StatusOK {
					t.Fatalf("response metadata lost: %+v", protocolError.ResponseMetadata)
				}

				if !body.closed {
					t.Fatal("response body was not closed")
				}
			})
		}
	}
}

func TestStreamProtocolErrorKeepsMetadata(t *testing.T) {
	for _, contentType := range []string{"application/json", "text/event-stream"} {
		t.Run(contentType, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Request-Id", "stream-123")
				w.Header().Set("Content-Type", contentType)
				_, _ = io.WriteString(w, "data: {}\n\n")
			})

			stream, err := client.CreateChatCompletionStream(context.Background(), ChatCompletionRequest{
				Model:    "test",
				Messages: []Message{UserMessage("hi")},
			})
			if err == nil {
				defer stream.Close()
				_, err = stream.Recv()
			}

			var protocolError *ProtocolError
			if !errors.As(err, &protocolError) || protocolError.RequestID != "stream-123" {
				t.Fatalf("stream protocol metadata lost: %v", err)
			}
		})
	}
}
