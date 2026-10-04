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
)

const (
	maxBody     = 256 << 10
	turnTimeout = 2 * time.Minute
	// The browser estimates the same way, so both sides agree on the cap.
	charsPerToken   = 4
	minAnswerTokens = 256
)

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
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Name  string `json:"name,omitempty"`
	Args  string `json:"args,omitempty"`
	Usage *Usage `json:"usage,omitempty"`
}

type Proxy struct {
	cfg             Config
	trusted, viewer *http.Client
}

func New(cfg Config) *Proxy {
	p := &Proxy{cfg: cfg, trusted: &http.Client{}, viewer: viewerClient()}
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
	if in := estimate(req.Messages); t.budget > 0 && in+minAnswerTokens > t.budget {
		http.Error(w, fmt.Sprintf("this question needs about %d tokens before the answer, over the cap of %d: untick some context or raise the cap", in, t.budget), http.StatusUnprocessableEntity)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), turnTimeout)
	defer cancel()
	p.converse(ctx, &stream{w: w, rc: http.NewResponseController(w)}, t, req)
}

func (p *Proxy) converse(ctx context.Context, s *stream, t target, req chatRequest) {
	var c compat
	rd, err := t.call(ctx, &c, req.Messages, 0, s)
	if err != nil {
		s.fail(err)
		return
	}
	if rd.Finish == "length" {
		s.send(event{Type: "notice", Text: "The answer stopped at the token cap."})
	}
	s.send(event{Type: "done", Usage: &rd.Usage})
}

type target struct {
	base, model, key string
	budget           int
	client           *http.Client
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
		return target{p.cfg.URL, p.cfg.Model, key, budget, p.trusted}, nil
	}
	base, err := p.cfg.checkURL(req.URL)
	if err != nil {
		return target{}, err
	}
	if req.Model == "" {
		return target{}, errors.New("model is required")
	}
	return target{base, req.Model, viewerKey, budget, p.viewer}, nil
}

func estimate(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Role) + len(m.Content)
	}
	return n/charsPerToken + 1
}

// compat remembers what an endpoint rejected, so a retry leaves it out.
type compat struct{ noUsage, completionTokens bool }

func (c *compat) relax(msg string) bool {
	switch {
	case !c.noUsage && strings.Contains(msg, "stream_options"):
		c.noUsage = true
	case !c.completionTokens && strings.Contains(msg, "max_tokens"):
		c.completionTokens = true
	default:
		return false
	}
	return true
}

type round struct {
	Finish string
	Usage  Usage
	chars  int
}

// call runs one upstream completion and streams its text. used is what
// earlier rounds of this question spent, so max_tokens keeps the total under the budget.
func (t target) call(ctx context.Context, c *compat, msgs []Message, used int, s *stream) (round, error) {
	in := estimate(msgs)
	for attempt := 0; ; attempt++ {
		body := map[string]any{"model": t.model, "messages": msgs, "stream": true}
		if !c.noUsage {
			body["stream_options"] = map[string]bool{"include_usage": true}
		}
		if t.budget > 0 {
			k := "max_tokens"
			if c.completionTokens {
				k = "max_completion_tokens"
			}
			body[k] = t.budget - used - in
		}
		resp, err := t.post(ctx, body)
		if err != nil {
			return round{}, fmt.Errorf("assistant endpoint unreachable: %w", err)
		}
		if resp.StatusCode/100 == 2 {
			defer resp.Body.Close()
			rd, err := readStream(resp.Body, func(text string) { s.send(event{Type: "delta", Text: text}) })
			if rd.Usage.Total == 0 {
				out := rd.chars / charsPerToken
				rd.Usage = Usage{Prompt: in, Completion: out, Total: in + out, Estimated: true}
			}
			return rd, err
		}
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if attempt < 3 && resp.StatusCode == http.StatusBadRequest && c.relax(string(msg)) {
			continue
		}
		return round{}, fmt.Errorf("assistant endpoint returned %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
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
		if data == "[DONE]" {
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
				rd.chars += len(ch.Delta.Content)
				onText(ch.Delta.Content)
			}
			if ch.FinishReason != "" {
				rd.Finish = ch.FinishReason
			}
		}
	}
	return rd, sc.Err()
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
