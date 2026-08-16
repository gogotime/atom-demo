package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func newSSEWriter(w http.ResponseWriter) (*sseWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("response writer does not support flushing")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return &sseWriter{w: w, flusher: flusher}, nil
}

func (s *sseWriter) emit(event string, data interface{}) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *sseWriter) emitEnvelope(idx int64, event string, data interface{}) error {
	envelope := map[string]interface{}{"i": idx, "t": event, "d": data}
	b, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

var (
	locksMu sync.Mutex
	locks   = map[int64]*sync.Mutex{}
)

func lockForProject(id int64) *sync.Mutex {
	locksMu.Lock()
	defer locksMu.Unlock()
	m, ok := locks[id]
	if !ok {
		m = &sync.Mutex{}
		locks[id] = m
	}
	return m
}

func sanitizeAgentError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "context deadline exceeded"):
		return errors.New("AI request timed out. Please retry.")
	case strings.Contains(msg, "llm http:"):
		return errors.New("AI service is unreachable. Please retry.")
	case strings.HasPrefix(msg, "llm "):
		return errors.New("AI service returned an error. Please retry.")
	}
	return errors.New("The AI run failed. Please retry.")
}

func isRetryableLLMError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "llm 429") {
		return true
	}
	if strings.Contains(msg, "llm 5") {
		return true
	}
	if strings.Contains(msg, "llm http:") {
		return true
	}
	return false
}

func withRetry(ctx context.Context, maxAttempts int, fn func() error) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	backoff := 250 * time.Millisecond
	var last error
	for i := 0; i < maxAttempts; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := fn()
		if err == nil {
			return nil
		}
		last = err
		if !isRetryableLLMError(err) {
			return err
		}
		if i < maxAttempts-1 {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			backoff *= 2
		}
	}
	return last
}

const (
	maxTurns         = 4
	compactThreshold = 25000
	keepRecentChars  = 8000
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	ToolID  string `json:"tool_id,omitempty"`
}

type pendingToolCall struct {
	Name string
	ID   string
	Args map[string]interface{}
}

type toolOutcome struct {
	tc           pendingToolCall
	isError      bool
	content      string
	modifiesCode bool
}

func shortPreview(s string) string {
	const max = 120
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

func truncatePreview(s string, max int) string {
	if max < 4 {
		max = 4
	}
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

type turnRecorder struct {
	rec        *turnRecord
	codeAfter  string
}

func newTurnRecorder(turn int, userMsg string) *turnRecorder {
	return &turnRecorder{
		rec: &turnRecord{
			Turn:        turn,
			TS:          time.Now().UTC().Format(time.RFC3339),
			UserMessage: userMsg,
			ToolCalls:   []turnToolCall{},
		},
	}
}

func (r *turnRecorder) setAssistantText(s string) {
	r.rec.AssistantText = s
}

func (r *turnRecorder) addToolCall(tc pendingToolCall, result string, isError bool) {
	argsJSON, _ := json.Marshal(tc.Args)
	r.rec.ToolCalls = append(r.rec.ToolCalls, turnToolCall{
		ID:      tc.ID,
		Name:    tc.Name,
		Args:    string(argsJSON),
		Result:  result,
		IsError: isError,
	})
}

func (r *turnRecorder) setCode(html string) {
	r.rec.CodeAfter = html
	r.codeAfter = html
}

func (r *turnRecorder) setUsage(u *UsageInfo) {
	r.rec.Usage = u
}

func (r *turnRecorder) setStop(reason string, truncated bool) {
	r.rec.StopReason = reason
	r.rec.Truncated = truncated
}

func (r *turnRecorder) commit(ts *turnStorage) error {
	return ts.Write(r.rec)
}

type streamTap struct {
	sink *sseWriter
	log  *streamLog
}

func (t *streamTap) emit(event string, data interface{}) error {
	var idx int64
	if t.log != nil {
		var err error
		idx, err = t.log.Append(event, data)
		if err != nil {
			log.Printf("stream log append: %v", err)
		}
	}
	if t.sink != nil {
		return t.sink.emitEnvelope(idx, event, data)
	}
	return nil
}

func (t *streamTap) close() {
	if t.log != nil {
		_ = t.log.Close()
	}
}

func executeToolBatch(ctx context.Context, tools []pendingToolCall, code string, id, uID int64) ([]toolOutcome, string) {
	outcomes := make([]toolOutcome, len(tools))
	if len(tools) == 0 {
		return outcomes, code
	}
	curCode := code
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range tools {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, newCode := executeSingleTool(ctx, &tools[i], curCode, id, uID)
			outcomes[i] = out
			if !out.isError && out.modifiesCode {
				mu.Lock()
				curCode = newCode
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	return outcomes, curCode
}

func executeSingleTool(ctx context.Context, tc *pendingToolCall, code string, id, uID int64) (toolOutcome, string) {
	switch tc.Name {
	case "read_files":
		return executeReadFiles(tc, code), code
	case "patch_files":
		return executePatchFiles(tc, code, id, uID)
	case "update_files":
		return executeUpdateFiles(tc, code, id, uID)
	default:
		return toolOutcome{
			tc:      *tc,
			isError: true,
			content: "unknown tool: " + tc.Name,
		}, code
	}
}

func executeReadFiles(tc *pendingToolCall, code string) toolOutcome {
	if code == "" {
		return toolOutcome{
			tc:      *tc,
			content: "Project is empty. Use update_files to create the initial HTML.",
		}
	}
	return toolOutcome{
		tc:      *tc,
		content: code,
	}
}

func executePatchFiles(tc *pendingToolCall, code string, id, uID int64) (toolOutcome, string) {
	editsRaw, _ := tc.Args["edits"].([]interface{})
	if len(editsRaw) == 0 {
		return toolOutcome{
			tc:      *tc,
			isError: true,
			content: "patch_files called with no edits",
		}, code
	}
	cur := code
	applied := 0
	for _, e := range editsRaw {
		em, _ := e.(map[string]interface{})
		oldT, _ := em["old_text"].(string)
		newT, _ := em["new_text"].(string)
		if oldT == "" {
			return toolOutcome{
				tc:      *tc,
				isError: true,
				content: fmt.Sprintf("edit #%d: old_text is empty", applied+1),
			}, code
		}
		i := strings.Index(cur, oldT)
		if i < 0 {
			return toolOutcome{
				tc:      *tc,
				isError: true,
				content: fmt.Sprintf("edit #%d: old_text not found: %q", applied+1, truncatePreview(oldT, 80)),
			}, code
		}
		cur = cur[:i] + newT + cur[i+len(oldT):]
		applied++
	}
	cs, err := newCodeStorage(id)
	if err != nil {
		return toolOutcome{tc: *tc, isError: true, content: "save error: " + err.Error()}, code
	}
	if err := cs.Write(cur); err != nil {
		return toolOutcome{tc: *tc, isError: true, content: "save error: " + err.Error()}, code
	}
	return toolOutcome{
		tc:           *tc,
		content:      fmt.Sprintf("patched %d edit(s)", applied),
		modifiesCode: true,
	}, cur
}

func executeUpdateFiles(tc *pendingToolCall, code string, id, uID int64) (toolOutcome, string) {
	html, _ := tc.Args["html"].(string)
	if strings.TrimSpace(html) == "" {
		return toolOutcome{
			tc:      *tc,
			isError: true,
			content: "update_files called with empty html",
		}, code
	}
	cs, err := newCodeStorage(id)
	if err != nil {
		return toolOutcome{tc: *tc, isError: true, content: "save error: " + err.Error()}, code
	}
	if err := cs.Write(html); err != nil {
		return toolOutcome{tc: *tc, isError: true, content: "save error: " + err.Error()}, code
	}
	return toolOutcome{
		tc:           *tc,
		content:      "code updated",
		modifiesCode: true,
	}, html
}

func loadCode(ctx context.Context, projectID int64) (string, error) {
	cs, err := newCodeStorage(projectID)
	if err != nil {
		return "", err
	}
	return cs.Read()
}

func loadHistoryForLLM(ctx context.Context, projectID int64) ([]ChatMessage, error) {
	var out []ChatMessage

	cs, err := newCompactionStorage(projectID)
	if err == nil {
		if prev, err := cs.Read(); err == nil && prev != nil && prev.Summary != "" {
			out = append(out, ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("Context summary (before %d tokens of history):\n<summary>\n%s\n</summary>", prev.TokensBefore, prev.Summary),
			})
		}
	}

	ts, err := newTurnStorage(projectID)
	if err != nil {
		return out, err
	}
	turns, err := ts.List()
	if err != nil {
		return out, err
	}
	for _, tn := range turns {
		rec, err := ts.Read(tn)
		if err != nil {
			log.Printf("read turn %d: %v", tn, err)
			continue
		}
		if rec.UserMessage != "" {
			out = append(out, ChatMessage{Role: "user", Content: rec.UserMessage})
		}
		for _, tc := range rec.ToolCalls {
			argsJSON := tc.Args
			if argsJSON == "" {
				argsJSON = "{}"
			}
			out = append(out, ChatMessage{
				Role:    "assistant",
				Content: fmt.Sprintf(`[tool_call: %s id=%s] %s`, tc.Name, tc.ID, argsJSON),
			})
			out = append(out, ChatMessage{
				Role:    "tool",
				ToolID:  tc.ID,
				Content: fmt.Sprintf(`{"is_error":%t,"content":%q}`, tc.IsError, tc.Result),
			})
		}
		if rec.AssistantText != "" {
			out = append(out, ChatMessage{Role: "assistant", Content: rec.AssistantText})
		}
	}

	return out, nil
}

func handleGenerate(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id, err := parseIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Message) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message required"})
		return
	}

	sse, err := newSSEWriter(w)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	mu := lockForProject(id)
	if !mu.TryLock() {
		_ = sse.emit("error", map[string]string{"error": "Another generation is in progress for this project."})
		return
	}
	defer mu.Unlock()

	ctx := r.Context()

	p, err := getProject(ctx, id, u.ID)
	if err != nil {
		_ = sse.emit("error", map[string]string{"error": "internal error"})
		return
	}
	if p == nil {
		_ = sse.emit("error", map[string]string{"error": "project not found"})
		return
	}

	if err := setProjectGenerating(ctx, id, true); err != nil {
		log.Printf("set generating pid=%d: %v", id, err)
	}
	defer setProjectGenerating(ctx, id, false)

	sl, err := newStreamLog(id)
	if err != nil {
		log.Printf("stream log open pid=%d: %v", id, err)
	}
	defer func() {
		if sl != nil {
			_ = updateStreamOffset(ctx, id, sl.Offset())
			sl.Close()
		}
	}()

	tap := &streamTap{sink: sse, log: sl}

	code, err := loadCode(ctx, id)
	if err != nil {
		_ = tap.emit("error", map[string]string{"error": "internal error"})
		return
	}
	_ = code

	history, err := loadHistoryForLLM(ctx, id)
	if err != nil {
		log.Printf("load history pid=%d: %v", id, err)
		_ = tap.emit("error", map[string]string{"error": "internal error"})
		return
	}

	hasPrev := code != ""
	sysPrompt := buildSystemPrompt(hasPrev)

	if historyChars(history) > compactThreshold {
		log.Printf("pid=%d: pre-loop compaction (history=%d chars)", id, historyChars(history))
		if err := compactHistory(ctx, id, history); err != nil {
			log.Printf("pre-compact pid=%d: %v", id, err)
		}
		history, err = loadHistoryForLLM(ctx, id)
		if err != nil {
			log.Printf("reload history pid=%d: %v", id, err)
		}
	}

	ts, err := newTurnStorage(id)
	if err != nil {
		log.Printf("turn storage pid=%d: %v", id, err)
		_ = tap.emit("error", map[string]string{"error": "internal error"})
		return
	}
	nextTurn, err := ts.Next()
	if err != nil {
		log.Printf("turn next pid=%d: %v", id, err)
		_ = tap.emit("error", map[string]string{"error": "internal error"})
		return
	}

	rec := newTurnRecorder(nextTurn, req.Message)
	originalUserMsg := req.Message
	history = append(history, ChatMessage{Role: "user", Content: req.Message})
	retryUsed := false
	codeApplied := false

	for turn := 0; turn < maxTurns; turn++ {
		turnNum := turn + 1

		prov := detectProvider()
		var (
			usage *UsageInfo
			err   error
		)

		var (
			assistantText     strings.Builder
			suppressedBuf     strings.Builder // pseudo tool-call-as-text (dropped from UI/persistence)
			textTailBuf       strings.Builder // last ~12 chars of legitimate text, to bridge chunk-boundary splits
			pseudoToolAttempt bool
			tools             []pendingToolCall
			stopReason        string
			curTool           *pendingToolCall
		)

		const artifactMarker = "[tool_call:"
		const maxTailKeep = 16

		perTurnSink := func(evt StreamEvent) {
			switch evt.Type {
			case "text_delta":
				s := evt.Text
				if pseudoToolAttempt {
					// Already suppressing for this turn; just accumulate.
					suppressedBuf.WriteString(s)
					return
				}
				// Combine current chunk with the rolling tail so a split like
				// "[tool_" / "call: ..." is still detected.
				combined := textTailBuf.String() + s
				if idx := strings.Index(combined, artifactMarker); idx >= 0 {
					pseudoToolAttempt = true
					// Emit any prose that arrived before the artifact (only the part
					// that belongs to THIS chunk, not the bridged tail).
					tailLen := textTailBuf.Len()
					prefixLen := idx - tailLen
					if prefixLen > 0 {
						prefix := s[:prefixLen]
						assistantText.WriteString(prefix)
						_ = tap.emit("text", map[string]string{"text": prefix})
					}
					// Everything from the artifact start onward is suppressed.
					suppressedBuf.WriteString(combined[idx:])
					textTailBuf.Reset()
					log.Printf("pid=%d turn=%d: detected pseudo tool-call-as-text mid-stream; suppressing remainder of turn", id, turnNum)
					return
				}
				// No artifact in this chunk. Emit it, but remember the tail in
				// case the marker straddles the next chunk boundary.
				assistantText.WriteString(s)
				_ = tap.emit("text", map[string]string{"text": s})
				combinedFull := combined
				if len(combinedFull) > maxTailKeep {
					tail := combinedFull[len(combinedFull)-maxTailKeep:]
					textTailBuf.Reset()
					textTailBuf.WriteString(tail)
				} else {
					textTailBuf.Reset()
					textTailBuf.WriteString(combinedFull)
				}
			case "tool_start":
				_ = tap.emit("tool_call", map[string]string{"id": evt.ToolID, "name": evt.Text})
				curTool = &pendingToolCall{ID: evt.ToolID, Name: evt.Text}
			case "tool_done":
				if curTool != nil && curTool.ID == evt.ToolID {
					var args map[string]interface{}
					if len(evt.ToolInp) > 0 {
						_ = json.Unmarshal(evt.ToolInp, &args)
					}
					curTool.Args = args
					tools = append(tools, *curTool)
					curTool = nil
				}
			case "stop":
				stopReason = evt.Stop
			case "usage":
				usage = evt.Usage
			}
		}

		switch prov {
		case provClaude:
			usage, err = anthropicStream(ctx, sysPrompt, history, projectTools, perTurnSink)
		case provMock:
			usage, err = mockStream(ctx, sysPrompt, history, perTurnSink)
		default:
			err = errors.New("openai provider not yet implemented")
		}

		var inTok, outTok int
		if usage != nil {
			inTok, outTok = usage.Input, usage.Output
		}
		log.Printf("pid=%d turn=%d text=%dB tools=%d stop=%q in=%d out=%d history=%d chars",
			id, turnNum, assistantText.Len(), len(tools), stopReason, inTok, outTok, historyChars(history))

		if err != nil {
			log.Printf("pid=%d turn=%d llm error: %v", id, turnNum, err)
			_ = tap.emit("error", map[string]any{
				"error":  sanitizeAgentError(err).Error(),
				"detail": err.Error(),
			})
			return
		}

		if stopReason == "length" && len(tools) > 0 {
			log.Printf("pid=%d turn=%d: stop_reason=length with %d tool calls — treating as truncation", id, turnNum, len(tools))
			for _, tc := range tools {
				_ = tap.emit("tool_result", map[string]any{
					"id":       tc.ID,
					"is_error": true,
					"content":  "Tool call truncated due to output token limit. Re-issue with complete arguments.",
				})
				rec.addToolCall(tc, "truncated; please retry", true)
				history = append(history,
					ChatMessage{Role: "assistant", Content: fmt.Sprintf(`[tool_call: %s id=%s] {"truncated":true}`, tc.Name, tc.ID)},
					ChatMessage{Role: "tool", ToolID: tc.ID, Content: `{"is_error":true,"content":"truncated; please retry"}`},
				)
			}
			continue
		}

		text := assistantText.String()
		const maxPersist = 2000
		if len(text) > maxPersist {
			text = text[:maxPersist] + "...[truncated]"
		}
		rec.setAssistantText(text)

		// If the model emitted a tool call as plain text instead of via the
		// structured tool_use channel, don't surface it to the user. Instead,
		// feed the suppressed artifact back to the model along with an
		// explicit reminder, so the next iteration retries through the proper
		// channel. We cap this at one retry per turn.
		if pseudoToolAttempt {
			artifact := suppressedBuf.String()
			log.Printf("pid=%d turn=%d: pseudo-tool-call-as-text (%d bytes); feeding back as retry", id, turnNum, len(artifact))
			if retryUsed {
				_ = tap.emit("error", map[string]string{"error": "The AI couldn't invoke a tool correctly (wrote it as plain text). Please rephrase your request and try again."})
				_ = tap.emit("done", map[string]any{"ok": false})
				return
			}
			retryUsed = true
			_ = tap.emit("text", map[string]string{"text": "\n\n[retrying — previous reply misused the tool API] "})
			history = append(history, ChatMessage{Role: "assistant", Content: artifact})
			history = append(history, ChatMessage{Role: "user", Content: "Your previous reply attempted to invoke a tool by writing it as plain text starting with `[tool_call: NAME id=XXX] {...}`. That format is just how past tool calls are serialized in your conversation history — it is NOT how you actually call a tool. You must use the structured tool_use API channel. Please retry by invoking the tool properly through that channel."})
			continue
		}

		if len(tools) == 0 && assistantText.Len() > 0 {
			if codeApplied {
				_ = tap.emit("text", map[string]string{"text": "\n\n"})
				break
			}
			if !retryUsed {
				retryUsed = true
				if historyChars(history) > compactThreshold {
					log.Printf("pid=%d: pre-retry compaction (history=%d chars)", id, historyChars(history))
					_ = compactHistory(ctx, id, history)
					history, _ = loadHistoryForLLM(ctx, id)
				}
				_ = tap.emit("text", map[string]string{"text": "\n\n[retrying…] "})
				reminder := "Your last reply did not call update_files or patch_files, so no changes were applied. You MUST call patch_files or update_files now to apply the user's request: " + originalUserMsg
				history = append(history, ChatMessage{Role: "user", Content: reminder})
				continue
			}
			_ = tap.emit("error", map[string]string{"error": "The AI didn't apply any changes. Please rephrase your request and try again."})
			_ = tap.emit("done", map[string]any{"ok": false})
			return
		}

		if len(tools) == 0 {
			rec.setUsage(usage)
			rec.setStop(stopReason, false)
			rec.setCode(code)
			if err := rec.commit(ts); err != nil {
				log.Printf("commit turn pid=%d: %v", id, err)
			}
			break
		}

		outcomes, newCode := executeToolBatch(ctx, tools, code, id, u.ID)
		for _, o := range outcomes {
			_ = tap.emit("tool_result", map[string]any{
				"id":       o.tc.ID,
				"is_error": o.isError,
				"content":  o.content,
			})
			rec.addToolCall(o.tc, o.content, o.isError)
			argsJSON, _ := json.Marshal(map[string]any{"tool": o.tc.Name, "args": o.tc.Args})
			history = append(history,
				ChatMessage{Role: "assistant", Content: fmt.Sprintf("[tool_call: %s id=%s] %s", o.tc.Name, o.tc.ID, string(argsJSON))},
				ChatMessage{Role: "tool", ToolID: o.tc.ID, Content: fmt.Sprintf(`{"is_error":%t,"content":%q}`, o.isError, o.content)},
			)
			if !o.isError && o.modifiesCode {
				codeApplied = true
			}
		}
		code = newCode
		rec.setCode(code)
		rec.setUsage(usage)
		rec.setStop(stopReason, false)

		if historyChars(history) > compactThreshold {
			log.Printf("pid=%d: mid-loop compaction (history=%d chars)", id, historyChars(history))
			if err := compactHistory(ctx, id, history); err != nil {
				log.Printf("compact pid=%d: %v", id, err)
			} else {
				history, err = loadHistoryForLLM(ctx, id)
				if err != nil {
					log.Printf("reload history pid=%d: %v", id, err)
				}
			}
		}
	}

	if err := rec.commit(ts); err != nil {
		log.Printf("commit turn pid=%d: %v", id, err)
	}
	_ = tap.emit("done", map[string]any{"ok": true})
}

func historyChars(h []ChatMessage) int {
	n := 0
	for _, m := range h {
		n += len(m.Content)
	}
	return n
}

const summarizationSystemPrompt = `You are a context-summarization assistant. Produce a structured summary another LLM will use to continue a project. Do NOT continue the conversation; only output the structured summary. Be concise. Preserve exact filenames, function names, and user-stated constraints.`

const summarizationPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const updateSummarizationPrompt = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

func compactHistory(ctx context.Context, projectID int64, history []ChatMessage) error {
	var cut int
	acc := 0
	for i := len(history) - 1; i >= 0; i-- {
		acc += len(history[i].Content)
		if acc >= keepRecentChars {
			cut = i
			break
		}
	}
	if cut <= 1 {
		log.Printf("compact pid=%d: skip (cut=%d, total=%d)", projectID, cut, len(history))
		return nil
	}
	toSummarize := history[:cut]
	log.Printf("compact pid=%d: summarizing %d older messages (%d chars), keeping %d recent",
		projectID, cut, historyChars(toSummarize), len(history)-cut)

	cs, err := newCompactionStorage(projectID)
	if err != nil {
		return err
	}
	prev, _ := cs.Read()

	var convSB strings.Builder
	for _, m := range toSummarize {
		fmt.Fprintf(&convSB, "[%s] %s\n", m.Role, m.Content)
	}
	conversation := convSB.String()

	var prompt string
	if prev != nil && prev.Summary != "" {
		prompt = "<conversation>\n" + conversation + "\n</conversation>\n\n<previous-summary>\n" + prev.Summary + "\n</previous-summary>\n\n" + updateSummarizationPrompt
	} else {
		prompt = "<conversation>\n" + conversation + "\n</conversation>\n\n" + summarizationPrompt
	}

	prov := detectProvider()
	var summary string
	switch prov {
	case provClaude:
		summary, err = claudeCompleteWithRetry(ctx, summarizationSystemPrompt, []ChatMessage{
			{Role: "user", Content: prompt},
		})
	case provMock:
		summary = "## Goal\n(none)\n## Progress\n### Done\n- (mock)\n## Next Steps\n1. (mock)"
		err = nil
	default:
		err = errors.New("compaction: provider unsupported")
	}
	if err != nil {
		log.Printf("compact pid=%d: claudeComplete error: %v", projectID, err)
		return err
	}

	tokensBefore := historyChars(toSummarize)
	rec := &compactionRecord{
		TokensBefore: tokensBefore,
		Summary:      summary,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if err := cs.Write(rec); err != nil {
		log.Printf("compact pid=%d: write compaction: %v", projectID, err)
		return err
	}
	log.Printf("compact pid=%d: stored summary (%d chars)", projectID, len(summary))
	return nil
}

func claudeCompleteWithRetry(ctx context.Context, system string, msgs []ChatMessage) (string, error) {
	var out string
	err := withRetry(ctx, 3, func() error {
		s, e := claudeComplete(ctx, system, msgs)
		if e != nil {
			return e
		}
		out = s
		return nil
	})
	return out, err
}

func claudeComplete(ctx context.Context, system string, msgs []ChatMessage) (string, error) {
	baseURL := apiBaseURL()
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	model := defaultStr(apiModel(), defaultModel(provClaude))
	key := apiKey()

	bodyMsgs := make([]map[string]interface{}, 0, len(msgs))
	for _, m := range msgs {
		bodyMsgs = append(bodyMsgs, map[string]interface{}{"role": m.Role, "content": m.Content})
	}
	body := map[string]interface{}{
		"model":      model,
		"max_tokens": 1500,
		"system":     system,
		"messages":   bodyMsgs,
	}
	bodyBytes, _ := json.Marshal(body)

	url := strings.TrimRight(baseURL, "/") + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	if !strings.HasPrefix(key, "sk-ant-") {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", fmt.Errorf("llm %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	for _, c := range parsed.Content {
		if c.Type == "text" {
			return c.Text, nil
		}
	}
	return "", errors.New("no text in completion response")
}
