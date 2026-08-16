package main

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
	"time"
)

// llmProvider kinds
type llmProvider string

const (
	provClaude llmProvider = "claude"
	provOpenAI llmProvider = "openai"
	provMock   llmProvider = "mock"
)

// detectProvider picks a provider based on env. Priority:
//  1. LLM_PROVIDER if set explicitly
//  2. URL containing "anthropic" → claude
//  3. API key starting with "sk-ant-" → claude
//  4. fallback to openai
func detectProvider() llmProvider {
	explicit := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))
	switch explicit {
	case "claude", "anthropic":
		return provClaude
	case "openai":
		return provOpenAI
	case "mock", "none":
		return provMock
	}
	base := strings.ToLower(apiBaseURL())
	if strings.Contains(base, "anthropic") {
		return provClaude
	}
	key := apiKey()
	if strings.HasPrefix(key, "sk-ant-") {
		return provClaude
	}
	return provOpenAI
}

// apiKey returns the active API key, accepting both new (LLM_API_KEY) and legacy (OPENAI_API_KEY) names
func apiKey() string {
	if k := strings.TrimSpace(os.Getenv("LLM_API_KEY")); k != "" {
		return k
	}
	if k := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); k != "" {
		return k
	}
	return ""
}

func apiBaseURL() string {
	if u := strings.TrimSpace(os.Getenv("LLM_BASE_URL")); u != "" {
		return u
	}
	return ""
}

func apiModel() string {
	if m := strings.TrimSpace(os.Getenv("LLM_MODEL")); m != "" {
		return m
	}
	return ""
}

func defaultModel(p llmProvider) string {
	switch p {
	case provClaude:
		return "claude-3-5-sonnet-20241022"
	case provOpenAI:
		return "gpt-4o-mini"
	default:
		return "mock-model"
	}
}

func defaultStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// ============================================================================
// Streaming protocol (SSE-ish events emitted to the agent loop / SSE writer)
// ============================================================================
//
// StreamEvent kinds:
//   - "text_delta"  : partial assistant text
//   - "tool_start"  : tool call started (id, name)
//   - "tool_input"  : accumulated tool arguments JSON
//   - "tool_done"   : tool call arguments finalized
//   - "stop"        : turn finished with stop reason (end_turn / tool_use / max_tokens / error)
//   - "usage"       : token usage (input, output)
//
// All Anthropic tool use content blocks stream through these events.
// ============================================================================

type StreamEvent struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	ToolID  string          `json:"tool_id,omitempty"`
	ToolInp json.RawMessage `json:"tool_input,omitempty"`
	Stop    string          `json:"stop_reason,omitempty"`
	Usage   *UsageInfo      `json:"usage,omitempty"`
}

type UsageInfo struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// ============================================================================
// System prompt
// ============================================================================

const systemPromptBase = `You are a senior frontend engineer maintaining a small web app. The app is a single self-contained HTML file (HTML + inline CSS + inline JS, no external scripts, no frameworks, no network calls).

You have THREE tools: read_files, patch_files, update_files.

- read_files: see the current HTML. Call this before editing if you don't already know the current contents.
- patch_files: make surgical edits. Pass edits=[{old_text, new_text}, ...]. old_text must match the current file exactly. Empty new_text deletes the matched text. Edits apply in order.
- update_files: replace the entire HTML. Use for new projects or full rewrites.

CRITICAL RULES:

1. When the user asks for ANY change to the app, you MUST call patch_files or update_files. Plain-text responses claiming "done" without a tool call are FORBIDDEN.

2. For small changes (one or a few lines), prefer patch_files — it is faster and cheaper. For new apps or wholesale rewrites, use update_files.

3. Only respond with plain text (no tool call) when the user asks a question or wants an explanation — never as a substitute for doing the work they asked for.

4. Plan briefly in 1-2 sentences, then call a tool. Don't ramble.

5. Keep the app short and readable. Vanilla HTML/CSS/JS only. No external scripts. No network calls.

6. NEVER write a tool call as plain text. Tools are invoked via a structured API channel — not by typing literal text like "[tool_call: name id=xxx] {args...}" in your reply. Past tool calls appear in your conversation history serialized as that bracket form ONLY for context; to actually call a tool, you must use the tool_use mechanism. Writing the bracket form in your reply text does nothing and will confuse the user.`

func buildSystemPrompt(hasPrev bool) string {
	if hasPrev {
		return systemPromptBase + "\n\nThis project already has code. Call read_files if you need to see the current HTML before editing."
	}
	return systemPromptBase + "\n\nThis is a new project — start by calling update_files with the full HTML."
}

type toolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

var readFilesTool = toolDef{
	Name:        "read_files",
	Description: "Return the current contents of the project's HTML file. The project is a single self-contained HTML file with inline CSS and JS. Call this before editing if you don't already know the current contents.",
	InputSchema: map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	},
}

var patchFilesTool = toolDef{
	Name:        "patch_files",
	Description: "Apply one or more exact-string replacements to the project's HTML. Each edit's old_text must match the current file exactly. Empty new_text deletes the matched text. Edits apply in order — later edits see earlier patches within the same call.",
	InputSchema: map[string]interface{}{
		"type":     "object",
		"required": []string{"edits"},
		"properties": map[string]interface{}{
			"edits": map[string]interface{}{
				"type":     "array",
				"minItems": 1,
				"items": map[string]interface{}{
					"type":     "object",
					"required": []string{"old_text", "new_text"},
					"properties": map[string]interface{}{
						"old_text": map[string]interface{}{
							"type": "string",
						},
						"new_text": map[string]interface{}{
							"type": "string",
						},
					},
				},
			},
		},
	},
}

var updateFilesTool = toolDef{
	Name: "update_files",
	Description: "Replace the project's entire HTML file. Pass the full HTML source (with inline CSS and JS). " +
		"This replaces the entire project — the preview will reload with the new code.",
	InputSchema: map[string]interface{}{
		"type":     "object",
		"required": []string{"html"},
		"properties": map[string]interface{}{
			"html": map[string]interface{}{
				"type":        "string",
				"description": "Full HTML source",
			},
		},
	},
}

var projectTools = []toolDef{readFilesTool, patchFilesTool, updateFilesTool}

// ============================================================================
// LLM entry: anthropicStream
// ============================================================================
//
// anthropicStream calls the Anthropic messages API with streaming enabled,
// parsing SSE events and emitting a flat StreamEvent stream. The agent loop
// consumes this stream to drive text + tool execution + looping.

func anthropicStream(
	ctx context.Context,
	system string,
	messages []ChatMessage,
	tools []toolDef,
	sink func(StreamEvent),
) (*UsageInfo, error) {
	baseURL := apiBaseURL()
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	model := defaultStr(apiModel(), defaultModel(provClaude))
	key := apiKey()

	// Build request body
	toolDefs := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		toolDefs = append(toolDefs, map[string]interface{}{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		})
	}

	msgs := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		if m.Role == "" || m.Content == "" {
			continue
		}
		role := m.Role
		if role != "user" && role != "assistant" {
			role = "user"
		}
		msgs = append(msgs, map[string]interface{}{"role": role, "content": m.Content})
	}

	body := map[string]interface{}{
		"model":      model,
		"max_tokens": 8192,
		"system":     system,
		"messages":   msgs,
		"stream":     true,
	}
	if len(toolDefs) > 0 {
		body["tools"] = toolDefs
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(baseURL, "/") + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")

	// For proxies that mimic the Anthropic API, also send Bearer auth
	if !strings.HasPrefix(key, "sk-ant-") {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	httpClient := &http.Client{Timeout: 0}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("llm %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	return parseAnthropicSSE(resp.Body, sink)
}

// ============================================================================
// Anthropic SSE parser
// ============================================================================
//
// Anthropic streaming protocol:
//   event: message_start   data: { ..., usage: {input_tokens, output_tokens} }
//   event: content_block_start  data: { index, content_block: {type: text|tool_use, ...} }
//   event: content_block_delta  data: { index, delta: {type: text_delta|input_json_delta, text|partial_json} }
//   event: content_block_stop   data: { index }
//   event: message_delta        data: { delta: {stop_reason, stop_sequence}, usage: {...} }
//   event: message_stop         data: {}
//
// We flatten these into a single StreamEvent stream and accumulate per-block
// state in a small struct array indexed by content block index.

type anthropicBlock struct {
	Kind     string // "text" | "tool_use"
	ID       string // tool id when Kind=="tool_use"
	Name     string // tool name when Kind=="tool_use"
	InputBuf strings.Builder
	TextBuf  strings.Builder
}

func parseAnthropicSSE(body io.Reader, sink func(StreamEvent)) (*UsageInfo, error) {
	br := bufio.NewReaderSize(body, 64<<10)
	var eventName string
	var dataBuf strings.Builder
	blocks := map[int]*anthropicBlock{}
	var lastUsage *UsageInfo

	flushData := func() {
		if dataBuf.Len() == 0 {
			return
		}
		raw := dataBuf.String()
		dataBuf.Reset()
		switch eventName {
		case "message_start":

			type msgStart struct {
				Message struct {
					Usage struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			var m msgStart
			if err := json.Unmarshal([]byte(raw), &m); err == nil {
				lastUsage = &UsageInfo{Input: m.Message.Usage.InputTokens, Output: m.Message.Usage.OutputTokens}
				sink(StreamEvent{Type: "usage", Usage: lastUsage})
			}

		case "content_block_start":
			type cbStart struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type  string          `json:"type"`
					Text  string          `json:"text,omitempty"`
					ID    string          `json:"id,omitempty"`
					Name  string          `json:"name,omitempty"`
					Input json.RawMessage `json:"input,omitempty"`
				} `json:"content_block"`
			}
			var m cbStart
			if err := json.Unmarshal([]byte(raw), &m); err == nil {
				blk := &anthropicBlock{Kind: m.ContentBlock.Type, ID: m.ContentBlock.ID, Name: m.ContentBlock.Name}
				if m.ContentBlock.Text != "" {
					blk.TextBuf.WriteString(m.ContentBlock.Text)
				}
				// NOTE: do NOT seed blk.InputBuf from content_block_start's
				// "input" field. The Anthropic spec includes an empty `{}`
				// placeholder here; the real JSON arrives as input_json_delta
				// events that follow. Writing the placeholder breaks parsing.
				_ = m.ContentBlock.Input // explicitly ignored
				blocks[m.Index] = blk
				if blk.Kind == "tool_use" {
					sink(StreamEvent{Type: "tool_start", ToolID: blk.ID, Text: blk.Name})
				}
			}

		case "content_block_delta":
			type cbDelta struct {
				Index int `json:"index"`
				Delta struct {
					Type        string          `json:"type"`
					Text        string          `json:"text,omitempty"`
					PartialJSON string          `json:"partial_json,omitempty"`
					Input       json.RawMessage `json:"input,omitempty"`
				} `json:"delta"`
			}
			var m cbDelta
			if err := json.Unmarshal([]byte(raw), &m); err == nil {
				blk, ok := blocks[m.Index]
				if !ok {
					blk = &anthropicBlock{Kind: "text"}
					blocks[m.Index] = blk
				}
				switch m.Delta.Type {
				case "text_delta":
					if m.Delta.Text != "" {
						blk.TextBuf.WriteString(m.Delta.Text)
						sink(StreamEvent{Type: "text_delta", Text: m.Delta.Text})
					}
				case "input_json_delta":
					if m.Delta.PartialJSON != "" {
						blk.InputBuf.WriteString(m.Delta.PartialJSON)
					} else if len(m.Delta.Input) > 0 {
						blk.InputBuf.Write(m.Delta.Input)
					}
				}
			}

		case "content_block_stop":
			type cbStop struct {
				Index int `json:"index"`
			}
			var m cbStop
			if err := json.Unmarshal([]byte(raw), &m); err == nil {
				if blk, ok := blocks[m.Index]; ok && blk.Kind == "tool_use" {
					raw := strings.TrimSpace(blk.InputBuf.String())
					if raw == "" {
						raw = "{}"
					}
					sink(StreamEvent{Type: "tool_done", ToolID: blk.ID, ToolInp: json.RawMessage(raw)})
					delete(blocks, m.Index)
				}
			}

		case "message_delta":
			type msgDelta struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			var m msgDelta
			if err := json.Unmarshal([]byte(raw), &m); err == nil {
				if lastUsage == nil {
					lastUsage = &UsageInfo{}
				}
				lastUsage.Output = m.Usage.OutputTokens
				sink(StreamEvent{Type: "usage", Usage: lastUsage})
				if m.Delta.StopReason != "" {
					sink(StreamEvent{Type: "stop", Stop: m.Delta.StopReason})
				}
			}

		case "message_stop":
			// terminal
		}
		eventName = ""
	}

	for {
		line, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			return lastUsage, err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			flushData()
		case strings.HasPrefix(line, "event: "):
			eventName = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		}
		if err == io.EOF {
			flushData()
			break
		}
	}
	return lastUsage, nil
}

// ============================================================================
// Mock streaming (used when LLM_API_KEY is empty / LLM_PROVIDER=mock)
// ============================================================================

func mockStream(
	ctx context.Context,
	system string,
	messages []ChatMessage,
	sink func(StreamEvent),
) (*UsageInfo, error) {
	_ = system
	// Echo the most recent user message + pretend to write a tiny app.
	var userText string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			userText = messages[i].Content
			break
		}
	}
	if userText == "" {
		userText = "an app"
	}

	greeting := "I'm a local mock. Provide an LLM_API_KEY to get real generations.\n\n"
	greeting += "Here's a placeholder app based on your request:\n"

	// Stream text char-by-char
	for _, ch := range greeting {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		sink(StreamEvent{Type: "text_delta", Text: string(ch)})
		time.Sleep(8 * time.Millisecond)
	}

	full := "<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"UTF-8\" />\n<title>Mock</title>\n<style>\nbody{font-family:system-ui;background:#0f172a;color:#e2e8f0;margin:0;padding:2rem}\nh1{font-size:1.5rem}\n</style>\n</head>\n<body>\n<h1>" + htmlAndJSEscape(userText) + "</h1>\n<script>console.log('mock ready');</script>\n</body>\n</html>"
	sink(StreamEvent{Type: "tool_start", ToolID: "mock_1", Text: "update_files"})
	payload, _ := json.Marshal(map[string]string{"html": full})
	sink(StreamEvent{Type: "tool_done", ToolID: "mock_1", ToolInp: payload})
	sink(StreamEvent{Type: "stop", Stop: "tool_use"})
	return &UsageInfo{Input: 0, Output: 0}, nil
}

// htmlAndJSEscape escapes for safe inclusion as a literal in HTML or JS strings.
func htmlAndJSEscape(s string) string {
	r := strings.NewReplacer(
		`&`, "&amp;",
		`<`, "&lt;",
		`>`, "&gt;",
		`"`, "&quot;",
		`\`, `\\`,
	)
	return r.Replace(s)
}