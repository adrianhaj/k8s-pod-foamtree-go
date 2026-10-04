package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type seen struct {
	auth string
	body map[string]any
}

type upstream struct {
	*httptest.Server
	mu    sync.Mutex
	calls []seen
}

func (u *upstream) got() []seen {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.calls)
}

func newUpstream(t *testing.T, reply func(w http.ResponseWriter, r *http.Request, call int, body map[string]any)) *upstream {
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		u.mu.Lock()
		u.calls = append(u.calls, seen{r.Header.Get("Authorization"), body})
		n := len(u.calls)
		u.mu.Unlock()
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		reply(w, r, n, body)
	}))
	t.Cleanup(u.Close)
	return u
}

func sse(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, c := range chunks {
		fmt.Fprintf(w, "data: %s\n\n", c)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func answer(text string) func(http.ResponseWriter, *http.Request, int, map[string]any) {
	return func(w http.ResponseWriter, _ *http.Request, _ int, _ map[string]any) {
		sse(w, fmt.Sprintf(`{"choices":[{"delta":{"content":%q},"finish_reason":"stop"}]}`, text))
	}
}

func chat(p *Proxy, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/llm/chat", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("X-LLM-Key", key)
	}
	w := httptest.NewRecorder()
	p.ServeChat(w, r)
	return w
}

func events(t *testing.T, body string) []event {
	t.Helper()
	var out []event
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		data, ok := strings.CutPrefix(block, "data: ")
		if !ok {
			t.Fatalf("not an SSE event: %q", block)
		}
		var e event
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

const q = `"messages":[{"role":"user","content":"q"}]`

func TestServerKeyGoesOnlyToTheServerURL(t *testing.T) {
	server := newUpstream(t, answer("hi"))
	other := newUpstream(t, answer("hi"))
	p := New(Config{URL: server.URL + "/v1", Model: "m", Key: "server-secret", AllowAnyURL: true})

	chat(p, "", `{`+q+`}`)
	if c := server.got(); len(c) != 1 || c[0].auth != "Bearer server-secret" || c[0].body["model"] != "m" {
		t.Fatalf("server connection: %+v", c)
	}
	// A viewer-chosen URL without a key must not borrow the server's key.
	w := chat(p, "", `{"url":"`+other.URL+`/v1","model":"x",`+q+`}`)
	if c := other.got(); w.Code != 200 || len(c) != 1 || c[0].auth != "" {
		t.Fatalf("viewer URL: %d %+v", w.Code, c)
	}
	if len(server.got()) != 1 {
		t.Fatal("the server endpoint was called for a viewer URL")
	}
}

func TestViewerKeyIsForwardedAndNeverLogged(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ map[string]any) {
		http.Error(w, `{"error":{"message":"Incorrect API key provided"}}`, 401)
	})
	w := chat(New(Config{AllowAnyURL: true}), "viewer-secret", `{"url":"`+up.URL+`/v1","model":"m",`+q+`}`)
	if c := up.got(); len(c) != 1 || c[0].auth != "Bearer viewer-secret" {
		t.Fatalf("forwarded %+v", c)
	}
	if w.Code != 502 || !strings.Contains(w.Body.String(), "401") || !strings.Contains(w.Body.String(), "Incorrect API key") {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
	if strings.Contains(logs.String(), "viewer-secret") || strings.Contains(w.Body.String(), "viewer-secret") {
		t.Fatal("the viewer's key leaked into logs or the response")
	}
}

func TestStreamsDeltasAndRealUsage(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ map[string]any) {
		sse(w, `{"choices":[{"delta":{"content":"Hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
	})
	w := chat(New(Config{AllowAnyURL: true}), "k", `{"url":"`+up.URL+`/v1","model":"m","budget":1000,"messages":[{"role":"user","content":"12345678"}]}`)
	want := []event{{Type: "delta", Text: "Hel"}, {Type: "delta", Text: "lo"}, {Type: "done", Usage: &Usage{Prompt: 10, Completion: 2, Total: 12}}}
	if got := events(t, w.Body.String()); !reflect.DeepEqual(got, want) || w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%+v", got)
	}
	body := up.got()[0].body
	in := estimate([]Message{{Role: "user", Content: "12345678"}})
	if body["stream"] != true || body["stream_options"] == nil || body["max_tokens"] != float64(1000-in) {
		t.Fatalf("request %+v", body)
	}
}

func TestRetriesWithoutRejectedFeaturesAndEstimatesUsage(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, call int, _ map[string]any) {
		switch call {
		case 1:
			http.Error(w, `{"error":{"message":"Unrecognized request argument supplied: stream_options"}}`, 400)
		case 2:
			http.Error(w, `{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead."}}`, 400)
		default:
			sse(w, `{"choices":[{"delta":{"content":"12345678"},"finish_reason":"stop"}]}`)
		}
	})
	w := chat(New(Config{AllowAnyURL: true}), "k", `{"url":"`+up.URL+`/v1","model":"m","budget":1000,`+q+`}`)
	c := up.got()
	if len(c) != 3 || c[2].body["stream_options"] != nil || c[2].body["max_tokens"] != nil || c[2].body["max_completion_tokens"] == nil {
		t.Fatalf("retries %+v", c)
	}
	ev := events(t, w.Body.String())
	if last := ev[len(ev)-1]; last.Type != "done" || !last.Usage.Estimated || last.Usage.Completion != 2 || last.Usage.Total != last.Usage.Prompt+2 {
		t.Fatalf("usage %+v", last.Usage)
	}
}

func TestCapRefusesBeforeCallingUpstream(t *testing.T) {
	up := newUpstream(t, answer("never"))
	long := strings.Repeat("x", 4000)
	w := chat(New(Config{AllowAnyURL: true}), "k", `{"url":"`+up.URL+`/v1","model":"m","budget":1000,"messages":[{"role":"user","content":"`+long+`"}]}`)
	if w.Code != 422 || len(up.got()) != 0 || !strings.Contains(w.Body.String(), "cap") {
		t.Fatalf("%d %q, %d upstream calls", w.Code, w.Body, len(up.got()))
	}
}

func TestOperatorCapLimitsTheServerConnection(t *testing.T) {
	up := newUpstream(t, answer("ok"))
	chat(New(Config{URL: up.URL + "/v1", Model: "m", MaxTokens: 2000}), "", `{"budget":90000,`+q+`}`)
	if got := up.got()[0].body["max_tokens"].(float64); got > 2000 {
		t.Fatalf("max_tokens %v over the operator's cap", got)
	}
}

func TestAnswerCutByTheCapSaysSo(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ map[string]any) {
		sse(w, `{"choices":[{"delta":{"content":"partial"},"finish_reason":"length"}]}`)
	})
	ev := events(t, chat(New(Config{AllowAnyURL: true}), "k", `{"url":"`+up.URL+`/v1","model":"m","budget":1000,`+q+`}`).Body.String())
	if ev[1].Type != "notice" || !strings.Contains(ev[1].Text, "token cap") || ev[2].Type != "done" {
		t.Fatalf("%+v", ev)
	}
}

func TestRejectsBadChatRequests(t *testing.T) {
	p := New(Config{AllowedHosts: []string{"api.openai.com"}})
	r := httptest.NewRequest("POST", "/api/llm/chat", strings.NewReader(`{`+q+`}`))
	w := httptest.NewRecorder()
	p.ServeChat(w, r)
	if w.Code != 415 {
		t.Errorf("no JSON content type: %d", w.Code)
	}
	for _, tc := range []struct {
		key, body string
		code      int
		text      string
	}{
		{"k", `{"messages":[{"role":"user","content":"` + strings.Repeat("x", 300<<10) + `"}]}`, 413, "256 KiB"},
		{"k", `{"messages":[]}`, 400, "messages"},
		{"k", `not json`, 400, "JSON"},
		{"k", `{"url":"https://evil.example/v1","model":"m",` + q + `}`, 400, "not allowed"},
		{"k", `{"url":"https://api.openai.com/v1",` + q + `}`, 400, "model is required"},
		{"", `{` + q + `}`, 400, "no server connection"},
	} {
		w := chat(p, tc.key, tc.body)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.text) {
			t.Errorf("%.60s: %d %q", tc.body, w.Code, w.Body)
		}
	}
}

func TestStopCancelsTheUpstreamRequest(t *testing.T) {
	cancelled := make(chan struct{})
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ int, _ map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"a"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	})
	front := httptest.NewServer(http.HandlerFunc(New(Config{AllowAnyURL: true}).ServeChat))
	defer front.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", front.URL, strings.NewReader(`{"url":"`+up.URL+`/v1","model":"m",`+q+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LLM-Key", "k")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream request kept running after the viewer stopped")
	}
}

func TestServerKeyFileIsReread(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api-key")
	os.WriteFile(path, []byte("k1\n"), 0o600)
	up := newUpstream(t, answer("ok"))
	p := New(Config{URL: up.URL + "/v1", Model: "m", KeyFile: path})
	chat(p, "", `{`+q+`}`)
	os.WriteFile(path, []byte("k2"), 0o600)
	chat(p, "", `{`+q+`}`)
	if c := up.got(); c[0].auth != "Bearer k1" || c[1].auth != "Bearer k2" {
		t.Fatalf("%q %q", c[0].auth, c[1].auth)
	}
}

func TestConfigNeverReturnsTheKey(t *testing.T) {
	w := httptest.NewRecorder()
	New(Config{URL: "https://api.openai.com/v1", Model: "m", Key: "server-secret", MaxTokens: 50000}).ServeConfig(w, httptest.NewRequest("GET", "/", nil))
	body := w.Body.String()
	if strings.Contains(body, "server-secret") || !strings.Contains(body, `"server":true`) || !strings.Contains(body, `"maxTokens":50000`) {
		t.Fatalf("%s", body)
	}
}

func TestUpstreamErrorsDoNotLeakTheServerKey(t *testing.T) {
	status := 401
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ map[string]any) {
		http.Error(w, `{"error":{"message":"bad key server-secret"}}`, status)
	})
	p := New(Config{URL: up.URL + "/v1", Model: "m", Key: "server-secret"})
	w := chat(p, "", `{`+q+`}`)
	if w.Code != 502 || strings.Contains(w.Body.String(), "server-secret") || !strings.Contains(w.Body.String(), "API key was rejected") {
		t.Fatalf("401: %d %q", w.Code, w.Body)
	}
	status = 500
	w = chat(p, "", `{`+q+`}`)
	if strings.Contains(w.Body.String(), "server-secret") || !strings.Contains(w.Body.String(), "[key]") {
		t.Fatalf("500: %d %q", w.Code, w.Body)
	}
	w = chat(New(Config{AllowAnyURL: true}), "viewer-secret", `{"url":"`+up.URL+`/v1","model":"m",`+q+`}`)
	if strings.Contains(w.Body.String(), "viewer-secret") {
		t.Fatalf("viewer key echoed: %q", w.Body)
	}
}

func TestRelayedUpstreamTextIsShort(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ map[string]any) {
		http.Error(w, strings.Repeat("y", 3000), 500)
	})
	w := chat(New(Config{AllowAnyURL: true}), "k", `{"url":"`+up.URL+`/v1","model":"m",`+q+`}`)
	if w.Body.Len() > 400 {
		t.Fatalf("relayed %d bytes", w.Body.Len())
	}
}

func TestUnreachableEndpointDoesNotEchoTheAddress(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	addr := strings.TrimPrefix(gone.URL, "http://")
	gone.Close()
	w := chat(New(Config{AllowAnyURL: true}), "k", `{"url":"http://`+addr+`/v1","model":"m",`+q+`}`)
	if w.Code != 502 || strings.Contains(w.Body.String(), "127.0.0.1") || strings.Contains(w.Body.String(), addr) || !strings.Contains(w.Body.String(), "unreachable") {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
}

func TestServerConnectionDoesNotFollowRedirects(t *testing.T) {
	other := newUpstream(t, answer("hi"))
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ int, _ map[string]any) {
		http.Redirect(w, r, other.URL+"/v1/chat/completions", http.StatusTemporaryRedirect)
	})
	w := chat(New(Config{URL: up.URL + "/v1", Model: "m", Key: "server-secret"}), "", `{`+q+`}`)
	if w.Code != 502 || len(other.got()) != 0 {
		t.Fatalf("%d %q, redirect target hit %d times", w.Code, w.Body, len(other.got()))
	}
}

func TestCapHoldsWhenTheEndpointRejectsTheLimit(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ int, body map[string]any) {
		if body["max_tokens"] != nil || body["max_completion_tokens"] != nil {
			http.Error(w, `{"error":{"message":"max_tokens is too large: 998. This model supports at most 4096 completion tokens"}}`, 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 100000 && r.Context().Err() == nil; i++ {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"abcd"}}]}`+"\n\n")
			w.(http.Flusher).Flush()
		}
	})
	w := chat(New(Config{AllowAnyURL: true}), "k", `{"url":"`+up.URL+`/v1","model":"m","budget":1000,`+q+`}`)
	ev := events(t, w.Body.String())
	n := 0
	for _, e := range ev[:len(ev)-2] {
		if e.Type != "delta" {
			t.Fatalf("%+v", e)
		}
		n += len([]rune(e.Text))
	}
	in := estimate([]Message{{Role: "user", Content: "q"}})
	if want := (1000 - in) * charsPerToken; n != want {
		t.Fatalf("streamed %d chars, want %d", n, want)
	}
	if ev[len(ev)-2].Type != "notice" || !strings.Contains(ev[len(ev)-2].Text, "token cap") || ev[len(ev)-1].Type != "done" {
		t.Fatalf("%+v", ev[len(ev)-2:])
	}
	if c := up.got(); len(c) != 2 || c[1].body["max_tokens"] != nil || c[1].body["max_completion_tokens"] != nil {
		t.Fatalf("calls %+v", c)
	}
}

func TestEstimateCountsRunes(t *testing.T) {
	if got, want := estimate([]Message{{Role: "user", Content: "日本語日本語"}}), (4+6)/charsPerToken+1; got != want {
		t.Fatalf("estimate %d, want %d", got, want)
	}
}

func TestSaysWhenTheStreamEndsEarlyOrIsMissing(t *testing.T) {
	for name, tc := range map[string]struct {
		reply func(http.ResponseWriter)
		text  string
	}{
		"no DONE": {func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"half"}}]}`+"\n\n")
		}, "ended the answer early"},
		"not a stream": {func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"content":"whole"}}]}`)
		}, "did not stream"},
	} {
		up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ map[string]any) { tc.reply(w) })
		ev := events(t, chat(New(Config{AllowAnyURL: true}), "k", `{"url":"`+up.URL+`/v1","model":"m",`+q+`}`).Body.String())
		if n := len(ev); n < 2 || ev[n-2].Type != "notice" || !strings.Contains(ev[n-2].Text, tc.text) || ev[n-1].Type != "done" {
			t.Errorf("%s: %+v", name, ev)
		}
	}
}

func TestTurnDeadlineIsReadable(t *testing.T) {
	up := newUpstream(t, answer("x"))
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	tg := target{base: up.URL + "/v1", model: "m", client: &http.Client{}}
	_, err := tg.call(ctx, &compat{}, []Message{{Role: "user", Content: "q"}}, 0, &stream{w: httptest.NewRecorder()})
	if err == nil || !strings.Contains(err.Error(), "longer than 2 minutes") {
		t.Fatalf("%v", err)
	}
}

func TestSecretsAreMaskedBeforeLeaving(t *testing.T) {
	up := newUpstream(t, answer("ok"))
	body := `{"url":"` + up.URL + `/v1","model":"m","messages":[` +
		`{"role":"system","content":"logs:\nDB_PASSWORD=hunter2hunter2\nconnect postgres://app:s3cretpw@db:5432/x"},` +
		`{"role":"user","content":"why does it fail?"}]}`
	w := chat(New(Config{AllowAnyURL: true}), "k", body)
	sent, _ := json.Marshal(up.got()[0].body)
	for _, secret := range []string{"hunter2hunter2", "s3cretpw"} {
		if strings.Contains(string(sent), secret) {
			t.Fatalf("%s reached the model endpoint: %s", secret, sent)
		}
	}
	ev := events(t, w.Body.String())
	if last := ev[len(ev)-1]; last.Type != "done" || last.Masked != 2 {
		t.Fatalf("%+v", last)
	}
}

func TestUnknownRoleIsRejected(t *testing.T) {
	up := newUpstream(t, answer("ok"))
	body := `{"url":"` + up.URL + `/v1","model":"m","messages":[{"role":"tool","content":"x"}]}`
	if w := chat(New(Config{AllowAnyURL: true}), "k", body); w.Code != http.StatusBadRequest || len(up.got()) != 0 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
