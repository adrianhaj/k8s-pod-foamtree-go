package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxBody     = 256 << 10
	turnTimeout = 2 * time.Minute
	// The browser estimates the same way, so both sides agree on the cap.
	charsPerToken   = 4
	minAnswerTokens = 256
	maxRelayed      = 300
)

var errTimeout = errors.New("the answer took longer than 2 minutes")

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	URL      string    `json:"url"`
	Model    string    `json:"model"`
	Context  string    `json:"context"`
	Budget   int       `json:"budget"`
	Messages []Message `json:"messages"`
}

type Usage struct {
	Prompt     int  `json:"prompt"`
	Completion int  `json:"completion"`
	Total      int  `json:"total"`
	Estimated  bool `json:"estimated"`
}

func (u Usage) add(v Usage) Usage {
	return Usage{u.Prompt + v.Prompt, u.Completion + v.Completion, u.Total + v.Total, u.Estimated || v.Estimated}
}

type event struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Name   string `json:"name,omitempty"`
	Args   string `json:"args,omitempty"`
	Usage  *Usage `json:"usage,omitempty"`
	Masked int    `json:"masked,omitempty"`
}

type Proxy struct {
	cfg             Config
	trusted, viewer *http.Client
}

func New(cfg Config) *Proxy {
	p := &Proxy{cfg: cfg, trusted: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, viewer: viewerClient()}
	if cfg.AllowAnyURL {
		p.viewer = p.trusted
	}
	return p
}

func (p *Proxy) ServeConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"server": p.cfg.URL != "", "url": p.cfg.URL, "model": p.cfg.Model, "maxTokens": p.cfg.MaxTokens})
}

func (p *Proxy) ServeChat(w http.ResponseWriter, r *http.Request) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "request over 256 KiB: untick some context", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "request body is not valid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Messages) == 0 {
		http.Error(w, "messages are required", http.StatusBadRequest)
		return
	}
	t, err := p.target(req, r.Header.Get("X-LLM-Key"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var hidden int
	req.Messages, hidden = redactMessages(req.Messages)
	if in := estimate(req.Messages); t.budget > 0 && in+minAnswerTokens > t.budget {
		http.Error(w, fmt.Sprintf("this question needs about %d tokens before the answer, over the cap of %d: untick some context or raise the cap", in, t.budget), http.StatusUnprocessableEntity)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), turnTimeout)
	defer cancel()
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Now().Add(turnTimeout + 10*time.Second))
	p.converse(ctx, &stream{w: w, rc: rc}, t, req, hidden)
}

func (p *Proxy) converse(ctx context.Context, s *stream, t target, req chatRequest, hidden int) {
	var c compat
	rd, err := t.call(ctx, &c, req.Messages, 0, s)
	if err != nil {
		s.fail(err)
		return
	}
	if rd.Finish == "length" {
		s.send(event{Type: "notice", Text: "The answer stopped at the token cap."})
	}
	s.send(event{Type: "done", Usage: &rd.Usage, Masked: hidden})
}

type target struct {
	base, model, key string
	budget           int
	client           *http.Client
	server           bool
}

// target picks the connection. A body without url is the server connection;
// the server key goes there and nowhere else.
func (p *Proxy) target(req chatRequest, viewerKey string) (target, error) {
	budget := req.Budget
	if req.URL == "" {
		if p.cfg.URL == "" {
			return target{}, errors.New("no server connection: add your own key in Settings, Assistant, Connection")
		}
		key, err := p.cfg.serverKey()
		if err != nil {
			slog.Warn("llm api key file", "err", err)
			return target{}, errors.New("the server's API key could not be read")
		}
		if m := p.cfg.MaxTokens; m > 0 && (budget <= 0 || budget > m) {
			budget = m
		}
		return target{p.cfg.URL, p.cfg.Model, key, budget, p.trusted, true}, nil
	}
	base, err := p.cfg.checkURL(req.URL)
	if err != nil {
		return target{}, err
	}
	if req.Model == "" {
		return target{}, errors.New("model is required")
	}
	return target{base, req.Model, viewerKey, budget, p.viewer, false}, nil
}

func estimate(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += utf8.RuneCountInString(m.Role) + utf8.RuneCountInString(m.Content)
	}
	return n/charsPerToken + 1
}

// compat remembers what an endpoint rejected, so a retry leaves it out.
// noLimit leaves the limit field out and the proxy counts the answer itself.
type compat struct{ noUsage, completionTokens, noLimit bool }

// limitTooLarge matches OpenAI and vLLM wording conservatively: a bare mention
// of the field means "rename it", a number or a size complaint means "drop it".
func limitTooLarge(msg string) bool {
	msg = strings.ToLower(msg)
	if strings.Contains(msg, "too large") || strings.Contains(msg, "maximum context length") || strings.Contains(msg, "at most") {
		return true
	}
	return (strings.Contains(msg, "max_tokens") || strings.Contains(msg, "max_completion_tokens")) && strings.ContainsAny(msg, "0123456789")
}

func (c *compat) relax(msg string) bool {
	switch {
	case !c.noUsage && strings.Contains(msg, "stream_options"):
		c.noUsage = true
	case !c.noLimit && limitTooLarge(msg):
		c.noLimit = true
	case !c.completionTokens && strings.Contains(msg, "max_tokens"):
		c.completionTokens = true
	case !c.noLimit && c.completionTokens && (strings.Contains(msg, "max_tokens") || strings.Contains(msg, "max_completion_tokens")):
		c.noLimit = true
	default:
		return false
	}
	return true
}

type round struct {
	Finish string
	Usage  Usage
	chars  int
	events int
	done   bool
}

// scrub keeps what an upstream said short and free of the key it was called with.
func (t target) scrub(s string) string {
	if t.key != "" {
		s = strings.ReplaceAll(s, t.key, "[key]")
	}
	if r := []rune(s); len(r) > maxRelayed {
		s = string(r[:maxRelayed]) + "…"
	}
	return s
}

// call runs one upstream completion and streams its text. used is what
// earlier rounds of this question spent, so max_tokens keeps the total under the budget.
func (t target) call(parent context.Context, c *compat, msgs []Message, used int, s *stream) (round, error) {
	ctx, stop := context.WithCancel(parent)
	defer stop()
	in := estimate(msgs)
	for attempt := 0; ; attempt++ {
		body := map[string]any{"model": t.model, "messages": msgs, "stream": true}
		if !c.noUsage {
			body["stream_options"] = map[string]bool{"include_usage": true}
		}
		if t.budget > 0 && !c.noLimit {
			k := "max_tokens"
			if c.completionTokens {
				k = "max_completion_tokens"
			}
			body[k] = t.budget - used - in
		}
		resp, err := t.post(ctx, body)
		if err != nil {
			slog.Warn("llm upstream unreachable", "err", err)
			if errors.Is(parent.Err(), context.DeadlineExceeded) {
				return round{}, errTimeout
			}
			return round{}, errors.New("assistant endpoint unreachable")
		}
		if resp.StatusCode/100 == 2 {
			defer resp.Body.Close()
			return t.read(parent, stop, c, resp.Body, in, used, s)
		}
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if attempt < 3 && resp.StatusCode == http.StatusBadRequest && c.relax(string(msg)) {
			continue
		}
		if t.server && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			slog.Warn("llm server key rejected", "status", resp.StatusCode)
			return round{}, errors.New("the server's API key was rejected by the assistant endpoint: ask the operator to check it")
		}
		return round{}, fmt.Errorf("assistant endpoint returned %d: %s", resp.StatusCode, t.scrub(string(bytes.TrimSpace(msg))))
	}
}

// read relays one streamed completion. Without a limit field the cap is kept
// here: past the remaining budget the upstream request is cancelled.
func (t target) read(parent context.Context, stop context.CancelFunc, c *compat, body io.Reader, in, used int, s *stream) (round, error) {
	limit, chars, cut := -1, 0, false
	if c.noLimit && t.budget > 0 {
		limit = max(t.budget-used-in, 0) * charsPerToken
	}
	rd, err := readStream(body, func(text string) {
		if cut {
			return
		}
		n := utf8.RuneCountInString(text)
		if limit >= 0 && chars+n >= limit {
			text = string([]rune(text)[:limit-chars])
			n, cut = len([]rune(text)), true
		}
		chars += n
		if text != "" {
			s.send(event{Type: "delta", Text: text})
		}
		if cut {
			stop()
		}
	})
	if cut {
		rd.Finish, rd.chars, err = "length", chars, nil
	}
	if err != nil {
		if errors.Is(parent.Err(), context.DeadlineExceeded) {
			return rd, errTimeout
		}
		return rd, errors.New(t.scrub(err.Error()))
	}
	if rd.Usage.Total == 0 {
		out := rd.chars / charsPerToken
		rd.Usage = Usage{Prompt: in, Completion: out, Total: in + out, Estimated: true}
	}
	switch {
	case cut || parent.Err() != nil:
	case rd.events == 0:
		s.send(event{Type: "notice", Text: "The endpoint did not stream an answer."})
	case !rd.done && rd.Finish == "":
		s.send(event{Type: "notice", Text: "The endpoint ended the answer early."})
	}
	return rd, nil
}

func (t target) post(ctx context.Context, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(t.base, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if t.key != "" {
		req.Header.Set("Authorization", "Bearer "+t.key)
	}
	return t.client.Do(req)
}

type chunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		Prompt     int `json:"prompt_tokens"`
		Completion int `json:"completion_tokens"`
		Total      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func readStream(r io.Reader, onText func(string)) (round, error) {
	var rd round
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		rd.events++
		if data == "[DONE]" {
			rd.done = true
			break
		}
		var c chunk
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			return rd, fmt.Errorf("unreadable reply from the assistant endpoint: %w", err)
		}
		if c.Error != nil {
			return rd, errors.New(c.Error.Message)
		}
		if c.Usage != nil {
			rd.Usage = Usage{Prompt: c.Usage.Prompt, Completion: c.Usage.Completion, Total: c.Usage.Total}
		}
		for _, ch := range c.Choices {
			if ch.Delta.Content != "" {
				rd.chars += utf8.RuneCountInString(ch.Delta.Content)
				onText(ch.Delta.Content)
			}
			if ch.FinishReason != "" {
				rd.Finish = ch.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		slog.Warn("llm upstream stream", "err", err)
		return rd, errors.New("the connection to the assistant endpoint broke during the answer")
	}
	return rd, nil
}

type stream struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	started bool
}

func (s *stream) send(e event) {
	if !s.started {
		h := s.w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Accel-Buffering", "no") // nginx ingress would otherwise hold the stream
		s.started = true
	}
	b, _ := json.Marshal(e)
	fmt.Fprintf(s.w, "data: %s\n\n", b)
	s.rc.Flush()
}

// fail is a plain 502 before the stream starts, and an error event after.
func (s *stream) fail(err error) {
	if !s.started {
		http.Error(s.w, err.Error(), http.StatusBadGateway)
		return
	}
	s.send(event{Type: "error", Text: err.Error()})
}
