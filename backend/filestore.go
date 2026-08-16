package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

func projectDir(projectID int64) string {
	base := os.Getenv("DATA_DIR")
	if base == "" {
		base = "./data"
	}
	return filepath.Join(base, "projects", strconv.FormatInt(projectID, 10))
}

func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

func atomicWriteFile(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type codeStorage struct {
	dir string
}

func newCodeStorage(projectID int64) (*codeStorage, error) {
	dir := projectDir(projectID)
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	return &codeStorage{dir: dir}, nil
}

func (s *codeStorage) Write(html string) error {
	return atomicWriteFile(filepath.Join(s.dir, "code.html"), html)
}

func (s *codeStorage) Read() (string, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, "code.html"))
	if err == nil {
		return string(b), nil
	}
	if os.IsNotExist(err) {
		return "", nil
	}
	return "", err
}

func (s *codeStorage) Exists() bool {
	_, err := os.Stat(filepath.Join(s.dir, "code.html"))
	return err == nil
}

type turnRecord struct {
	Turn          int                    `json:"turn"`
	TS            string                 `json:"ts"`
	UserMessage   string                 `json:"user_message"`
	AssistantText string                 `json:"assistant_text"`
	ToolCalls     []turnToolCall         `json:"tool_calls"`
	CodeAfter     string                 `json:"code_after"`
	Usage         *UsageInfo             `json:"usage,omitempty"`
	StopReason    string                 `json:"stop_reason"`
	Truncated     bool                   `json:"truncated,omitempty"`
}

type turnToolCall struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Args    string `json:"args"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

type turnStorage struct {
	dir string
}

func newTurnStorage(projectID int64) (*turnStorage, error) {
	dir := filepath.Join(projectDir(projectID), "turns")
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	return &turnStorage{dir: dir}, nil
}

func (s *turnStorage) Path(turn int) string {
	return filepath.Join(s.dir, fmt.Sprintf("%04d.json", turn))
}

func (s *turnStorage) Write(rec *turnRecord) error {
	if rec.Turn <= 0 {
		return fmt.Errorf("turn must be >= 1, got %d", rec.Turn)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.Path(rec.Turn), string(b))
}

func (s *turnStorage) Read(turn int) (*turnRecord, error) {
	b, err := os.ReadFile(s.Path(turn))
	if err != nil {
		return nil, err
	}
	var rec turnRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *turnStorage) List() ([]int, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var turns []int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		n, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		turns = append(turns, n)
	}
	sort.Ints(turns)
	return turns, nil
}

func (s *turnStorage) Next() (int, error) {
	turns, err := s.List()
	if err != nil {
		return 0, err
	}
	if len(turns) == 0 {
		return 1, nil
	}
	return turns[len(turns)-1] + 1, nil
}

type compactionStorage struct {
	dir string
}

func newCompactionStorage(projectID int64) (*compactionStorage, error) {
	dir := projectDir(projectID)
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	return &compactionStorage{dir: dir}, nil
}

type compactionRecord struct {
	TokensBefore  int    `json:"tokens_before"`
	Summary       string `json:"summary"`
	ReadFiles     string `json:"read_files"`
	ModifiedFiles string `json:"modified_files"`
	CreatedAt     string `json:"created_at"`
}

func (s *compactionStorage) Read() (*compactionRecord, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, "compaction.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var c compactionRecord
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *compactionStorage) Write(rec *compactionRecord) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(s.dir, "compaction.json"), string(b))
}

type streamLog struct {
	mu     sync.Mutex
	file   *os.File
	writer *bufio.Writer
	index  int64
}

func newStreamLog(projectID int64) (*streamLog, error) {
	path := filepath.Join(projectDir(projectID), "stream.ndjson")
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &streamLog{file: f, writer: bufio.NewWriter(f)}, nil
}

func (s *streamLog) Append(eventType string, data interface{}) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.index++
	payload, err := json.Marshal(map[string]interface{}{
		"i": s.index,
		"t": eventType,
		"d": data,
	})
	if err != nil {
		return 0, err
	}
	if _, err := s.writer.Write(payload); err != nil {
		return 0, err
	}
	if err := s.writer.WriteByte('\n'); err != nil {
		return 0, err
	}
	if err := s.writer.Flush(); err != nil {
		return 0, err
	}
	return s.index, nil
}

func (s *streamLog) Offset() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index
}

func (s *streamLog) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writer.Flush(); err != nil {
		return err
	}
	return s.file.Close()
}

func streamLogPath(projectID int64) string {
	return filepath.Join(projectDir(projectID), "stream.ndjson")
}

func readStreamLog(projectID int64, from int64, emit func(idx int64, eventType string, data json.RawMessage) error) (int64, error) {
	path := streamLogPath(projectID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 4<<20)
	var last int64
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env struct {
			I int64           `json:"i"`
			T string          `json:"t"`
			D json.RawMessage `json:"d"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		last = env.I
		if env.I <= from {
			continue
		}
		if err := emit(env.I, env.T, env.D); err != nil {
			return last, err
		}
	}
	return last, scanner.Err()
}
