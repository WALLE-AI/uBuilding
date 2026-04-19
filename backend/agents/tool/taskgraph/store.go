// Package taskgraph implements the TodoV2 task-graph Store plus the five
// associated tools (TaskCreate, TaskGet, TaskUpdate, TaskList, TaskStop).
// Task nodes form a DAG: nodes may declare a parent and explicit dependencies
// on other nodes. Cycles are rejected at Add / Update time.
package taskgraph

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Status values mirror claude-code's TodoV2 task-graph states.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusBlocked    = "blocked"
	StatusCompleted  = "completed"
	StatusCancelled  = "cancelled"
	StatusFailed     = "failed"
)

// ValidStatuses enumerates accepted status values.
var ValidStatuses = map[string]struct{}{
	StatusPending:    {},
	StatusInProgress: {},
	StatusBlocked:    {},
	StatusCompleted:  {},
	StatusCancelled:  {},
	StatusFailed:     {},
}

// Node is a single task in the graph. Fields mirror claude-code-main's
// TaskCreate/TaskUpdate/TaskGet surface:
//
//   - Title       maps to upstream "subject" (imperative task title)
//   - Description maps to upstream "description" (long-form detail)
//   - ActiveForm  maps to upstream "activeForm" (present-continuous label)
//   - Owner       maps to upstream "owner" (agent id or empty = unowned)
//   - DependsOn   maps to upstream "blockedBy" (ids that must resolve first)
//   - Payload     maps to upstream "metadata" (free-form string→string map)
type Node struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	ActiveForm  string            `json:"activeForm,omitempty"`
	Status      string            `json:"status"`
	Owner       string            `json:"owner,omitempty"`
	ParentID    string            `json:"parent_id,omitempty"`
	DependsOn   []string          `json:"depends_on,omitempty"`
	Payload     map[string]string `json:"payload,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// Store is a concurrency-safe task-graph repository.
type Store struct {
	mu    sync.RWMutex
	nodes map[string]*Node
}

// NewStore returns an empty task-graph store.
func NewStore() *Store { return &Store{nodes: map[string]*Node{}} }

// Add inserts a new node. Generates an id when n.ID is empty.
func (s *Store) Add(n Node) (Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n.ID == "" {
		n.ID = "task_" + uuid.NewString()
	}
	if n.Title == "" {
		return Node{}, errors.New("taskgraph: title required")
	}
	if n.Status == "" {
		n.Status = StatusPending
	}
	if _, ok := ValidStatuses[n.Status]; !ok {
		return Node{}, fmt.Errorf("taskgraph: invalid status %q", n.Status)
	}
	if _, exists := s.nodes[n.ID]; exists {
		return Node{}, fmt.Errorf("taskgraph: id %q already exists", n.ID)
	}
	if n.ParentID != "" {
		if _, ok := s.nodes[n.ParentID]; !ok {
			return Node{}, fmt.Errorf("taskgraph: parent %q not found", n.ParentID)
		}
	}
	for _, dep := range n.DependsOn {
		if _, ok := s.nodes[dep]; !ok {
			return Node{}, fmt.Errorf("taskgraph: dependency %q not found", dep)
		}
	}
	now := time.Now()
	n.CreatedAt = now
	n.UpdatedAt = now
	copy := n
	s.nodes[copy.ID] = &copy
	if err := s.detectCycleLocked(copy.ID); err != nil {
		delete(s.nodes, copy.ID)
		return Node{}, err
	}
	return copy, nil
}

// Get returns a node snapshot.
func (s *Store) Get(id string) (Node, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.nodes[id]
	if !ok {
		return Node{}, false
	}
	return *n, true
}

// UpdateFields describes the subset of fields an Update call may change.
// Nil pointers mean "leave unchanged".
type UpdateFields struct {
	Title       *string
	Description *string
	ActiveForm  *string
	Status      *string
	Owner       *string
	ParentID    *string
	DependsOn   *[]string
	Payload     *map[string]string
}

// Update applies patches to a node. Returns the updated snapshot.
func (s *Store) Update(id string, patch UpdateFields) (Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return Node{}, fmt.Errorf("taskgraph: id %q not found", id)
	}
	prev := *n
	if patch.Title != nil {
		if *patch.Title == "" {
			return Node{}, errors.New("taskgraph: title must not be empty")
		}
		n.Title = *patch.Title
	}
	if patch.Description != nil {
		n.Description = *patch.Description
	}
	if patch.ActiveForm != nil {
		n.ActiveForm = *patch.ActiveForm
	}
	if patch.Owner != nil {
		n.Owner = *patch.Owner
	}
	if patch.Status != nil {
		if _, ok := ValidStatuses[*patch.Status]; !ok {
			return Node{}, fmt.Errorf("taskgraph: invalid status %q", *patch.Status)
		}
		n.Status = *patch.Status
	}
	if patch.ParentID != nil {
		if *patch.ParentID == id {
			return Node{}, errors.New("taskgraph: node cannot be its own parent")
		}
		if *patch.ParentID != "" {
			if _, ok := s.nodes[*patch.ParentID]; !ok {
				return Node{}, fmt.Errorf("taskgraph: parent %q not found", *patch.ParentID)
			}
		}
		n.ParentID = *patch.ParentID
	}
	if patch.DependsOn != nil {
		for _, dep := range *patch.DependsOn {
			if dep == id {
				return Node{}, errors.New("taskgraph: node cannot depend on itself")
			}
			if _, ok := s.nodes[dep]; !ok {
				return Node{}, fmt.Errorf("taskgraph: dependency %q not found", dep)
			}
		}
		n.DependsOn = append([]string(nil), (*patch.DependsOn)...)
	}
	if patch.Payload != nil {
		n.Payload = *patch.Payload
	}
	n.UpdatedAt = time.Now()
	if err := s.detectCycleLocked(id); err != nil {
		*n = prev
		return Node{}, err
	}
	return *n, nil
}

// List returns all nodes filtered by status (empty = no filter), sorted by
// creation time ascending.
func (s *Store) List(status string) []Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		if status != "" && n.Status != status {
			continue
		}
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Remove deletes a node. Returns true when removed.
func (s *Store) Remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[id]; !ok {
		return false
	}
	delete(s.nodes, id)
	return true
}

// Stop transitions a node to cancelled. Returns (status, ok, err) matching
// the bg.GraphStopper interface contract.
func (s *Store) Stop(id string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return "", false, nil
	}
	if n.Status == StatusCompleted || n.Status == StatusCancelled || n.Status == StatusFailed {
		return n.Status, true, nil
	}
	n.Status = StatusCancelled
	n.UpdatedAt = time.Now()
	return n.Status, true, nil
}

// Children returns the direct children of parentID.
func (s *Store) Children(parentID string) []Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Node
	for _, n := range s.nodes {
		if n.ParentID == parentID {
			out = append(out, *n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// detectCycleLocked walks ParentID + DependsOn edges from start looking for
// start again. Caller must hold s.mu.
func (s *Store) detectCycleLocked(start string) error {
	type frame struct {
		id   string
		path []string
	}
	stack := []frame{{id: start, path: []string{start}}}
	visited := map[string]bool{}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n, ok := s.nodes[cur.id]
		if !ok {
			continue
		}
		next := make([]string, 0, len(n.DependsOn)+1)
		next = append(next, n.DependsOn...)
		if n.ParentID != "" {
			next = append(next, n.ParentID)
		}
		for _, nid := range next {
			if nid == start && len(cur.path) > 1 {
				return fmt.Errorf("taskgraph: cycle detected via %v", append(cur.path, nid))
			}
			if visited[nid] {
				continue
			}
			visited[nid] = true
			stack = append(stack, frame{id: nid, path: append(cur.path, nid)})
		}
	}
	return nil
}
