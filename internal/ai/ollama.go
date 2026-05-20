// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// DefaultOllamaHost is the local-daemon URL Ollama listens on by default.
// Honour OLLAMA_HOST to override (matches the upstream CLI's convention).
const DefaultOllamaHost = "http://localhost:11434"

// OllamaProvider streams from Ollama's /api/chat endpoint. Pure net/http,
// no SDK dep — Ollama's wire format is simple newline-delimited JSON and
// adding an SDK for it would be more weight than the protocol deserves.
type OllamaProvider struct {
	host   string
	client *http.Client
}

func NewOllama() (*OllamaProvider, error) {
	host := strings.TrimRight(os.Getenv("OLLAMA_HOST"), "/")
	if host == "" {
		host = DefaultOllamaHost
	}
	return &OllamaProvider{
		host:   host,
		client: &http.Client{}, // no timeout — ctx drives cancellation
	}, nil
}

func (p *OllamaProvider) Name() string { return "ollama" }

// Wire types kept private — Ollama's chat API is the only consumer.
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  *ollamaOptions  `json:"options,omitempty"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	// NumPredict is Ollama's MaxTokens equivalent; -1 = until done.
	NumPredict int `json:"num_predict,omitempty"`
}

type ollamaChatResponse struct {
	Message    ollamaMessage `json:"message"`
	Done       bool          `json:"done"`
	DoneReason string        `json:"done_reason,omitempty"`
}

func (p *OllamaProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	body := ollamaChatRequest{
		Model:    req.Model,
		Messages: buildOllamaMessages(req.System, req.Messages),
		Stream:   true,
	}
	if req.MaxTokens > 0 {
		body.Options = &ollamaOptions{NumPredict: req.MaxTokens}
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal ollama request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.host+"/api/chat", bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("build ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	events := make(chan Event, 8)
	go p.runStream(ctx, httpReq, events)
	return events, nil
}

func (p *OllamaProvider) runStream(ctx context.Context, httpReq *http.Request, out chan<- Event) {
	defer close(out)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		sendEvent(ctx, out, ErrorEvent{Err: fmt.Errorf("ollama dial: %w", err)})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		sendEvent(ctx, out, ErrorEvent{Err: fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))})
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	// NDJSON lines can be large when the model emits a long token in one
	// chunk; allow up to 1 MiB per line to match the log streamer.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var stopReason string
	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk ollamaChatResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			// Skip malformed lines instead of aborting the stream; in
			// practice Ollama is well-behaved here, but a single bad
			// line shouldn't lose the rest of the response.
			continue
		}
		if chunk.Message.Content != "" {
			if !sendEvent(ctx, out, TextDeltaEvent{Delta: chunk.Message.Content}) {
				return
			}
		}
		if chunk.Done {
			stopReason = chunk.DoneReason
			break
		}
	}

	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		sendEvent(ctx, out, ErrorEvent{Err: fmt.Errorf("ollama read: %w", err)})
		return
	}
	sendEvent(ctx, out, DoneEvent{StopReason: stopReason})
}

func buildOllamaMessages(system string, in []Message) []ollamaMessage {
	out := make([]ollamaMessage, 0, len(in)+1)
	if system != "" {
		out = append(out, ollamaMessage{Role: "system", Content: system})
	}
	for _, m := range in {
		switch m.Role {
		case RoleUser:
			out = append(out, ollamaMessage{Role: "user", Content: m.Content})
		case RoleAssistant:
			out = append(out, ollamaMessage{Role: "assistant", Content: m.Content})
		}
	}
	return out
}
