package state

import (
	"sync"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// Store is a thread-safe in-memory state container with change notification.
// It replaces TypeScript's EventEmitter-based AppStateStore pattern.
type Store struct {
	mu        sync.RWMutex
	state     agents.AppState
	listeners []func(agents.AppState)
}

// NewStore creates a new Store with the given initial state.
func NewStore(initial agents.AppState) *Store {
	return &Store{
		state: initial,
	}
}

// Get returns a snapshot of the current state (read-locked).
func (s *Store) Get() agents.AppState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Update atomically modifies the state and notifies all listeners.
// The update function receives a pointer to the state for in-place mutation.
func (s *Store) Update(fn func(state *agents.AppState)) {
	s.mu.Lock()
	fn(&s.state)
	snapshot := s.state
	s.mu.Unlock()

	// Notify listeners outside the lock to prevent deadlocks
	for _, l := range s.listeners {
		l(snapshot)
	}
}

// SetState replaces the entire state and notifies listeners.
func (s *Store) SetState(fn func(prev *agents.AppState) *agents.AppState) {
	s.mu.Lock()
	newState := fn(&s.state)
	if newState != nil {
		s.state = *newState
	}
	snapshot := s.state
	s.mu.Unlock()

	for _, l := range s.listeners {
		l(snapshot)
	}
}

// Subscribe adds a listener that is called whenever the state changes.
// Returns an unsubscribe function.
func (s *Store) Subscribe(listener func(agents.AppState)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, listener)

	idx := len(s.listeners) - 1
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// Replace with last element and shrink
		if idx < len(s.listeners) {
			s.listeners[idx] = s.listeners[len(s.listeners)-1]
			s.listeners = s.listeners[:len(s.listeners)-1]
		}
	}
}
