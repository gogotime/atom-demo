package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func readJSON(r *http.Request, dst interface{}) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func userFromCtx(r *http.Request) *User {
	u, _ := r.Context().Value(ctxUserKey{}).(*User)
	return u
}

func handleListProjects(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	ps, err := listProjects(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if ps == nil {
		ps = []Project{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": ps})
}

func handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	_ = readJSON(r, &req)
	u := userFromCtx(r)
	id, err := createProject(r.Context(), u.ID, req.Name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func handleGetProject(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id, err := parseIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	p, err := getProject(r.Context(), id, u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	msgs, err := loadMessagesForDisplay(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project":  p,
		"messages": msgs,
	})
}

func handleGetProjectCode(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id, err := parseIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	p, err := getProject(r.Context(), id, u.ID)
	if err != nil || p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	cs, err := newCodeStorage(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	html, err := cs.Read()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"html": html})
}

func handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id, err := parseIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var req struct {
		Name   string `json:"name"`
		Prompt string `json:"prompt"`
	}
	_ = readJSON(r, &req)
	if err := updateProject(r.Context(), id, u.ID, req.Name, req.Prompt); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	p, err := getProject(r.Context(), id, u.ID)
	if err != nil || p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": p})
}

func handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id, err := parseIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	ok, err := deleteProject(r.Context(), id, u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func handlePublishProject(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id, err := parseIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var req struct {
		Published bool `json:"is_published"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	slug := ""
	if req.Published {
		slug = makeSlug()
	}
	actualSlug, err := setProjectPublished(r.Context(), id, u.ID, slug, req.Published)
	if err != nil {
		log.Printf("setProjectPublished pid=%d: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	resp := map[string]any{"is_published": req.Published}
	if req.Published {
		resp["slug"] = actualSlug
	}
	writeJSON(w, http.StatusOK, resp)
}

func handlePublicPage(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	p, err := getProjectBySlug(r.Context(), slug)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if p == nil {
		http.NotFound(w, r)
		return
	}
	cs, err := newCodeStorage(p.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	html, err := cs.Read()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": p.Name,
		"html": html,
	})
}

func writeStreamEvent(w http.ResponseWriter, flusher http.Flusher) func(idx int64, eventType string, data json.RawMessage) error {
	return func(idx int64, eventType string, data json.RawMessage) error {
		payload, _ := json.Marshal(map[string]interface{}{
			"i": idx,
			"t": eventType,
			"d": json.RawMessage(data),
		})
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
}

func handleStreamInfo(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id, err := parseIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	p, err := getProject(r.Context(), id, u.ID)
	if err != nil || p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"offset":     p.StreamOffset,
		"generating": p.IsGenerating,
	})
}

func handleStream(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id, err := parseIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	p, err := getProject(r.Context(), id, u.ID)
	if err != nil || p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	from := int64(0)
	if v := r.URL.Query().Get("from"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad from"})
			return
		}
		from = n
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no flusher"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	last, _ := readStreamLog(id, from, writeStreamEvent(w, flusher))

	pollInterval := 200 * time.Millisecond
	tailStart := time.Now()
	for {
		if !p.IsGenerating && last >= p.StreamOffset {
			break
		}
		newLast, err := readStreamLog(id, last, writeStreamEvent(w, flusher))
		if err != nil {
			break
		}
		if newLast > last {
			last = newLast
			tailStart = time.Now()
		}
		if time.Since(tailStart) > 60*time.Second {
			break
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

func parseIDParam(r *http.Request, name string) (int64, error) {
	raw := chi.URLParam(r, name)
	if raw == "" {
		return 0, errors.New("missing id")
	}
	var id int64
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, errors.New("bad id")
		}
		id = id*10 + int64(c-'0')
	}
	return id, nil
}

func makeSlug() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	for i := range b {
		b[i] = alphabet[int(nonce[i])%len(alphabet)]
	}
	return string(b)
}

func loadMessagesForDisplay(projectID int64) ([]Message, error) {
	ts, err := newTurnStorage(projectID)
	if err != nil {
		return nil, err
	}
	turns, err := ts.List()
	if err != nil {
		return nil, err
	}
	var msgs []Message
	for _, tn := range turns {
		rec, err := ts.Read(tn)
		if err != nil {
			continue
		}
		ts0 := rec.TS
		if rec.UserMessage != "" {
			msgs = append(msgs, Message{
				ProjectID: projectID,
				Role:      "user",
				Content:   rec.UserMessage,
				CreatedAt: ts0,
			})
		}
		for _, tc := range rec.ToolCalls {
			msgs = append(msgs, Message{
				ProjectID: projectID,
				Role:      "tool",
				ToolName:  tc.Name,
				ToolID:    tc.ID,
				Content:   fmt.Sprintf(`{"is_error":%t,"content":%q}`, tc.IsError, tc.Result),
				CreatedAt: ts0,
			})
		}
		if rec.AssistantText != "" {
			msgs = append(msgs, Message{
				ProjectID: projectID,
				Role:      "assistant",
				Content:   rec.AssistantText,
				CreatedAt: ts0,
			})
		}
	}
	if msgs == nil {
		msgs = []Message{}
	}
	return msgs, nil
}
