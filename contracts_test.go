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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestContentUnion(t *testing.T) {
	for _, wire := range []string{`null`, `""`, `"hello"`, `[{"type":"text","text":"hi"}]`} {
		var c Content
		if err := json.Unmarshal([]byte(wire), &c); err != nil {
			t.Fatal(err)
		}

		b, err := json.Marshal(c)
		if err != nil || string(b) != wire {
			t.Fatalf("%s => %s, %v", wire, b, err)
		}
	}

	for _, wire := range []string{`1`, `{}`, `true`} {
		var c Content
		if json.Unmarshal([]byte(wire), &c) == nil {
			t.Fatal("invalid content accepted")
		}
	}

	c := Text("old")
	if err := json.Unmarshal([]byte(`null`), &c); err != nil || !c.IsNull() {
		t.Fatal("null did not clear value")
	}

	if Text("").IsNull() || !(Content{}).IsNull() {
		t.Fatal("empty/null distinction")
	}

	b, _ := json.Marshal(Parts())
	if string(b) != "[]" {
		t.Fatal(string(b))
	}
}

func TestToolChoiceWire(t *testing.T) {
	cases := []struct {
		choice *ToolChoice
		wire   string
	}{
		{choice: AutoToolChoice(), wire: `"auto"`},
		{choice: NoToolChoice(), wire: `"none"`},
		{choice: RequiredToolChoice(), wire: `"required"`},
		{
			choice: NamedToolChoice("lookup"),
			wire:   `{"type":"function","function":{"name":"lookup"}}`,
		},
	}

	for _, tc := range cases {
		b, err := json.Marshal(tc.choice)
		if err != nil || string(b) != tc.wire {
			t.Fatalf("%s %v", b, err)
		}

		var decoded ToolChoice
		if err = json.Unmarshal(b, &decoded); err != nil {
			t.Fatal(err)
		}

		again, _ := json.Marshal(decoded)
		if string(again) != string(b) {
			t.Fatal("round trip")
		}
	}

	if _, err := json.Marshal(NamedToolChoice("")); err == nil {
		t.Fatal("empty named choice accepted")
	}
}

func TestLocalValidation(t *testing.T) {
	c := testClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("invalid request reached server")
	})
	requests := []ChatCompletionRequest{
		{},
		{Model: "m"},
		{
			Model:    "m",
			Messages: []Message{{Role: RoleTool, Content: Text("x")}},
		},
		{
			Model:     "m",
			Messages:  []Message{UserMessage("x")},
			MaxTokens: Ptr(0),
		},
	}

	for _, req := range requests {
		_, err := c.CreateChatCompletion(context.Background(), req)
		var e *ValidationError
		if !errors.As(err, &e) {
			t.Fatalf("got %v", err)
		}
	}
}

func TestResponseProtocolAndMetadata(t *testing.T) {
	for _, body := range []string{"null", "{} {}", "[]", `{"data":`, strings.Repeat(" ", 101)} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		c.maxResponseBytes = 100
		_, err := c.ListModels(context.Background())
		var e *ProtocolError
		if !errors.As(err, &e) {
			t.Fatalf("%q: %v", body, err)
		}
	}

	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.UserAgent(), "deepseek-go/") {
			t.Error("missing user agent")
		}

		w.Header().Set("X-Request-Id", "r1")
		fmt.Fprint(w, `{"data":[]}`)
	})
	out, err := c.ListModels(context.Background())
	if err != nil || out.RequestID != "r1" || out.StatusCode != 200 {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer target.Close()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) })
	_, err := c.ListModels(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 307 || calls.Load() != 0 {
		t.Fatalf("redirect %v calls=%d", err, calls.Load())
	}
}

func TestRetryPolicy(t *testing.T) {
	for _, post := range []bool{false, true} {
		var calls atomic.Int32
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(429)
				fmt.Fprint(w, `{"error":{"message":"retry"}}`)
				return
			}

			fmt.Fprint(w, `{"data":[]}`)
		})
		c.maxRetries = 2
		if post {
			_, err := c.CreateChatCompletion(context.Background(), ChatCompletionRequest{
				Model:    "m",
				Messages: []Message{UserMessage("x")},
			})
			if err == nil || calls.Load() != 1 {
				t.Fatalf("POST replayed: %d %v", calls.Load(), err)
			}
		} else {
			_, err := c.ListModels(context.Background())
			if err != nil || calls.Load() != 2 {
				t.Fatalf("GET: %d %v", calls.Load(), err)
			}
		}
	}

	t.Run("exhausted", func(t *testing.T) {
		var calls atomic.Int32
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
		})
		c.maxRetries = 2
		_, err := c.ListModels(context.Background())
		if err == nil || calls.Load() != 3 {
			t.Fatalf("%d %v", calls.Load(), err)
		}
	})
	t.Run("cancel-backoff", testCancelDuringBackoff)
}

func TestRetryAfter(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		header string
		want   time.Duration
		ok     bool
	}{
		{header: "0", want: 0, ok: true},
		{header: "2", want: 2 * time.Second, ok: true},
		{header: "31", want: 0, ok: false},
		{
			header: now.Add(3 * time.Second).Format(http.TimeFormat),
			want:   3 * time.Second,
			ok:     true,
		},
		{
			header: now.Add(time.Minute).Format(http.TimeFormat),
			want:   0,
			ok:     false,
		},
	} {
		d, ok := retryDelay(tc.header, 0, now)
		if d != tc.want || ok != tc.ok {
			t.Fatalf("%q: %v %v", tc.header, d, ok)
		}
	}

	for i := 0; i < 10; i++ {
		d, ok := retryDelay("bad", i, now)
		if !ok || d < 0 || d > 30*time.Second {
			t.Fatal(d, ok)
		}
	}
}

func TestAccumulator(t *testing.T) {
	var a Accumulator
	chunks := []string{
		`{"id":"c","choices":[{"index":1,"delta":{"content":"A","tool_calls":[{"index":2,"id":"t2","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
		`{"id":"c","choices":[{"index":0,"delta":{"reasoning_content":"think"}},{"index":1,"delta":{"content":"B","tool_calls":[{"index":2,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":7}}`,
	}

	for _, wire := range chunks {
		var chunk ChatCompletionChunk
		if err := json.Unmarshal([]byte(wire), &chunk); err != nil {
			t.Fatal(err)
		}

		if err := a.Add(chunk); err != nil {
			t.Fatal(err)
		}
	}

	out := a.Completion()
	if len(out.Choices) != 2 || out.Choices[1].Message.Content.Text() != "AB" || out.Choices[1].Message.ToolCalls[0].Function.Arguments != `{"q":1}` || out.Usage.TotalTokens != 7 {
		t.Fatalf("%+v", out)
	}

	*out.Choices[1].FinishReason = "changed"
	out.Choices[1].Message.ToolCalls[0].ID = "changed"
	out.Usage.TotalTokens = 999
	next := a.Completion()
	if *next.Choices[1].FinishReason != "tool_calls" || next.Choices[1].Message.ToolCalls[0].ID != "t2" || next.Usage.TotalTokens != 7 {
		t.Fatal("snapshot aliases accumulator")
	}

	if a.Add(ChatCompletionChunk{ID: "other"}) == nil {
		t.Fatal("mixed completions accepted")
	}

	if a.Add(ChatCompletionChunk{Choices: []ChunkChoice{{Delta: Delta{ToolCalls: []ToolCall{{}}}}}}) == nil {
		t.Fatal("missing index accepted")
	}
}

func TestStreamProtocolEdges(t *testing.T) {
	for _, body := range []string{"data: null\n\n", "data: {}\n\n", "event: error\ndata: {\"error\":\"failed\"}\n\n"} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, body)
		})
		s, err := c.CreateChatCompletionStream(context.Background(), ChatCompletionRequest{Model: "m", Messages: []Message{UserMessage("hi")}})
		if err != nil {
			t.Fatal(err)
		}

		defer s.Close()
		if _, err = s.Recv(); err == nil {
			t.Fatal("invalid event accepted")
		}
	}
}

// backoffContext observes Done only after the SDK has consumed and closed the
// 429 response. The fake transport does not consult this context, so the signal
// marks evaluation of the retry wait rather than cancellation during HTTP I/O.
type backoffContext struct {
	context.Context
	responseClosed atomic.Bool
	entered        chan struct{}
	once           sync.Once
}

func (ctx *backoffContext) Done() <-chan struct{} {
	if ctx.responseClosed.Load() {
		ctx.once.Do(func() { close(ctx.entered) })
	}

	return ctx.Context.Done()
}

type backoffResponseBody struct {
	io.Reader
	ctx *backoffContext
}

func (body *backoffResponseBody) Close() error {
	body.ctx.responseClosed.Store(true)
	return nil
}

func testCancelDuringBackoff(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx := &backoffContext{
		Context: base,
		entered: make(chan struct{}),
	}
	var calls atomic.Int32

	client, err := NewClient(Config{
		APIKey:     "test",
		MaxRetries: 2,
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)

				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     http.Header{"Retry-After": {"5"}},
					Body: &backoffResponseBody{
						Reader: strings.NewReader(`{"error":{"message":"retry later"}}`),
						ctx:    ctx,
					},
				}, nil
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := client.ListModels(ctx)
		result <- err
	}()

	// This timeout is only a test watchdog, never the request's cancellation
	// trigger. The request stays live until retry waiting has been observed.
	watchdog := time.NewTimer(2 * time.Second)
	defer watchdog.Stop()

	select {
	case <-ctx.entered:
	case err := <-result:
		t.Fatalf("request returned before entering backoff: %v", err)
	case <-watchdog.C:
		t.Fatal("request did not enter backoff")
	}

	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("backoff cancellation returned %v", err)
		}
	case <-watchdog.C:
		t.Fatal("cancellation did not interrupt backoff")
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("canceled backoff sent another request: got %d calls, want 1", got)
	}
}
