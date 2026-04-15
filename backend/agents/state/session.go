package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Session Persistence
// Maps to TypeScript utils/sessionStorage.ts
//
// Provides save/restore of conversation sessions and transcript persistence.
// Sessions are stored as JSON files in a configurable directory.
// ---------------------------------------------------------------------------

// SessionData is the serializable snapshot of a conversation session.
type SessionData struct {
	// ID uniquely identifies this session.
	ID string `json:"id"`
	// CreatedAt is when the session was first created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the session was last saved.
	UpdatedAt time.Time `json:"updated_at"`
	// Messages is the full conversation history.
	Messages []agents.Message `json:"messages"`
	// SystemPrompt is the system prompt at save time.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// Model is the model in use.
	Model string `json:"model,omitempty"`
	// Cwd is the working directory at save time.
	Cwd string `json:"cwd,omitempty"`
	// CompactCount tracks how many times the session was compacted.
	CompactCount int `json:"compact_count,omitempty"`
	// Metadata holds arbitrary session metadata.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// SessionStore manages session persistence to disk.
type SessionStore struct {
	mu       sync.Mutex
	dir      string
	sessions map[string]*SessionData
}

// NewSessionStore creates a store that persists sessions in the given directory.
func NewSessionStore(dir string) *SessionStore {
	return &SessionStore{
		dir:      dir,
		sessions: make(map[string]*SessionData),
	}
}

// Save persists a session to disk.
func (s *SessionStore) Save(session *SessionData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session.UpdatedAt = time.Now()
	s.sessions[session.ID] = session

	if s.dir == "" {
		return nil // in-memory only
	}

	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	path := filepath.Join(s.dir, session.ID+".json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write session: %w", err)
	}

	return nil
}

// Load restores a session from disk or memory cache.
func (s *SessionStore) Load(id string) (*SessionData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check memory cache first
	if cached, ok := s.sessions[id]; ok {
		return cached, nil
	}

	if s.dir == "" {
		return nil, fmt.Errorf("session not found: %s", id)
	}

	path := filepath.Join(s.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}

	var session SessionData
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}

	s.sessions[id] = &session
	return &session, nil
}

// Delete removes a session from memory and disk.
func (s *SessionStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, id)

	if s.dir == "" {
		return nil
	}

	path := filepath.Join(s.dir, id+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// List returns IDs of all known sessions (memory + disk).
func (s *SessionStore) List() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]bool)
	var ids []string

	// Memory sessions
	for id := range s.sessions {
		seen[id] = true
		ids = append(ids, id)
	}

	// Disk sessions
	if s.dir != "" {
		entries, err := os.ReadDir(s.dir)
		if err != nil && !os.IsNotExist(err) {
			return ids, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if ext := filepath.Ext(name); ext == ".json" {
				id := name[:len(name)-len(ext)]
				if !seen[id] {
					ids = append(ids, id)
				}
			}
		}
	}

	return ids, nil
}

// ---------------------------------------------------------------------------
// Transcript persistence
// ---------------------------------------------------------------------------

// TranscriptWriter writes conversation transcripts for debugging/auditing.
type TranscriptWriter struct {
	mu   sync.Mutex
	path string
	file *os.File
}

// NewTranscriptWriter creates a writer that appends to the given file.
func NewTranscriptWriter(path string) (*TranscriptWriter, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	return &TranscriptWriter{path: path, file: f}, nil
}

// Write appends a message to the transcript.
func (tw *TranscriptWriter) Write(msg agents.Message) error {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	if tw.file == nil {
		return fmt.Errorf("transcript writer closed")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = tw.file.Write(append(data, '\n'))
	return err
}

// Close flushes and closes the transcript file.
func (tw *TranscriptWriter) Close() error {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	if tw.file != nil {
		err := tw.file.Close()
		tw.file = nil
		return err
	}
	return nil
}

// Path returns the transcript file path.
func (tw *TranscriptWriter) Path() string {
	return tw.path
}
