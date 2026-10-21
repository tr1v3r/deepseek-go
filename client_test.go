package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewClient(Config{APIKey: "test-key", BaseURL: srv.URL + "/v1/", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func TestEndpoints(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing auth")
		}

		switch r.URL.Path {
		case "/v1/chat/completions":
			if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
				t.Error("wrong request headers/method")
			}

			var p map[string]any
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
			}

			if p["temperature"] != float64(0) || p["stream"] != false || p["logprobs"] != false {
				t.Errorf("zero values lost: %v", p)
			}

			if _, ok := p["stream_options"]; ok {
				t.Error("non-stream request sent stream_options")
			}

			fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"tool1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_cache_hit_tokens":7}}`)
		case "/v1/models":
			if r.Method != "GET" {
				t.Error("wrong method")
			}

			fmt.Fprint(w, `{"object":"list","data":[{"id":"test-model","context_window":123}]}`)
		case "/v1/user/balance":
			if r.Method != "GET" {
				t.Error("wrong method")
			}

			fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"0.10"}]}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	req := ChatCompletionRequest{Model: "test-model", Messages: []Message{{Role: "user", Content: Text("hi")}}, Temperature: Ptr(0.0), Logprobs: Ptr(false), Stream: true, StreamOptions: &StreamOptions{IncludeUsage: true}}
	out, err := c.CreateChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if out.Usage.PromptCacheHitTokens != 7 || out.Choices[0].Message.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("bad result: %+v", out)
	}

	if !req.Stream || req.StreamOptions == nil {
		t.Fatal("mutated caller request")
	}

	models, err := c.ListModels(context.Background())
	if err != nil || models.Data[0].ContextWindow != 123 {
		t.Fatalf("models: %v %v", models, err)
	}

	balance, err := c.GetBalance(context.Background())
	if err != nil || balance.BalanceInfos[0].TotalBalance != "0.10" {
		t.Fatalf("balance: %v %v", balance, err)
	}
}

func TestAPIErrors(t *testing.T) {
	for _, body := range []string{`{"error":{"message":"rate limited","type":"rate_limit","code":429}}`, "<html>gateway failure</html>", strings.Repeat("x", 70<<10)} {
		t.Run(body[:8], func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("X-Request-Id", "req1")
				w.WriteHeader(429)
				fmt.Fprint(w, body)
			})
			_, err := c.ListModels(context.Background())
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 || apiErr.RequestID != "req1" || len(apiErr.Body) > 64<<10 || calls != 1 {
				t.Fatalf("error: %+v calls=%d", err, calls)
			}
		})
	}
}

func TestValidationAndCancellation(t *testing.T) {
	for _, cfg := range []Config{{}, {APIKey: "x", BaseURL: "file:///tmp"}, {APIKey: "x", BaseURL: "https://host/?key=x"}, {APIKey: "x\ny"}} {
		if _, err := NewClient(cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}

	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("canceled request reached server") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListModels(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestStreamEvents(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}

		if !req.Stream || r.Header.Get("Accept") != "text/event-stream" {
			t.Error("missing stream flag")
		}

		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		fmt.Fprint(w, ": keepalive\r\n\r\nid: ignored\r\ndata: {\r\ndata: \"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"思考\"}}]}\r\n\r\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你好\",\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{}\"}}]},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":8}}\n\ndata: [DONE]\n\n")
	})
	s, err := c.CreateChatCompletionStream(context.Background(), ChatCompletionRequest{Model: "test", Messages: []Message{UserMessage("hi")}})
	if err != nil {
		t.Fatal(err)
	}

	defer s.Close()
	first, err := s.Recv()
	if err != nil || *first.Choices[0].Delta.ReasoningContent != "思考" {
		t.Fatalf("first %v %v", first, err)
	}

	second, err := s.Recv()
	if err != nil || *second.Choices[0].Delta.Content != "你好" || second.Usage.TotalTokens != 8 || *second.Choices[0].Delta.ToolCalls[0].Index != 0 {
		t.Fatalf("second %v %v", second, err)
	}

	for i := 0; i < 2; i++ {
		if _, err := s.Recv(); err != io.EOF {
			t.Fatalf("done: %v", err)
		}
	}
}

func TestStreamFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		target     error
	}{
		{"truncated", "data: {\"choices\":[]}\n\n", io.ErrUnexpectedEOF},
		{"partial", "data: {", io.ErrUnexpectedEOF},
		{"bad-json", "data: invalid\n\n", nil},
		{"api-error", "data: {\"error\":{\"message\":\"failed\"}}\n\n", nil},
		{"large-event", "data: " + strings.Repeat("x", maxEventBytes) + "\n\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, tc.body)
			})
			s, err := c.CreateChatCompletionStream(context.Background(), ChatCompletionRequest{Model: "test", Messages: []Message{UserMessage("hi")}})
			if err != nil {
				t.Fatal(err)
			}

			defer s.Close()
			for i := 0; i < 3; i++ {
				_, err = s.Recv()
				if err != nil {
					break
				}
			}

			if err == nil || err == io.EOF {
				t.Fatalf("expected failure: %v", err)
			}

			if tc.target != nil && !errors.Is(err, tc.target) {
				t.Fatalf("got %v want %v", err, tc.target)
			}

			if tc.name == "api-error" {
				var e *APIError
				if !errors.As(err, &e) || e.Message != "failed" {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestStreamCancellation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := c.CreateChatCompletionStream(ctx, ChatCompletionRequest{Model: "test", Messages: []Message{UserMessage("hi")}})
	if err != nil {
		t.Fatal(err)
	}

	defer s.Close()
	done := make(chan error, 1)
	go func() { _, err := s.Recv(); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not unblock Recv")
	}
}

func TestStreamClose(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	s, err := c.CreateChatCompletionStream(context.Background(), ChatCompletionRequest{Model: "test", Messages: []Message{UserMessage("hi")}})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { _, err := s.Recv(); done <- err }()
	_ = s.Close()
	_ = s.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected closed body error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close did not unblock Recv")
	}
}

func TestWrongContentType(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	if _, err := c.CreateChatCompletionStream(context.Background(), ChatCompletionRequest{Model: "test", Messages: []Message{UserMessage("hi")}}); err == nil {
		t.Fatal("accepted non-SSE response")
	}
}
