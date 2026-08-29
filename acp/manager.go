package acp

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// Manager owns multiple ACP sessions.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	emit     func(Event)
}

func NewManager(emit func(Event)) *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		emit:     emit,
	}
}

type CreateOptions struct {
	Launch AgentLaunch
	Mode   string // local | remote
	Cwd    string
	SSH    *SSHParams
	Title  string
	Files  FileResolver
}

func (m *Manager) Create(opts CreateOptions) (*Session, error) {
	if opts.Mode == "" {
		opts.Mode = "local"
	}
	id := uuid.New().String()[:8]
	title := opts.Title
	if title == "" {
		if opts.Launch.Name != "" {
			title = opts.Launch.Name
		} else {
			title = "ACP"
		}
	}
	ctx := context.Background()
	var tr Transport
	var err error
	if opts.Mode == "remote" {
		if opts.SSH == nil {
			return nil, fmt.Errorf("remote ACP requires SSH params")
		}
		tr, err = startSSHTransport(opts.Launch, opts.Cwd, *opts.SSH)
	} else {
		tr, err = startLocalTransport(ctx, opts.Launch, opts.Cwd)
	}
	if err != nil {
		return nil, err
	}
	sess := newSession(id, title, opts.Mode, opts.Launch.Name, opts.Cwd, tr, m.emit, opts.Files)
	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()
	// Initialize asynchronously so the frontend can subscribe to events first.
	go func() {
		if err := sess.Initialize(opts.Cwd); err != nil {
			m.emit(Event{SessionID: id, Type: "error", Content: err.Error()})
		}
	}()
	return sess, nil
}

func (m *Manager) Get(id string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("acp session %s not found", id)
	}
	return s, nil
}

func (m *Manager) Prompt(id, text string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	return s.Prompt(text)
}

func (m *Manager) Cancel(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	return s.Cancel()
}

func (m *Manager) SetMode(id, modeID string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	return s.SetMode(modeID)
}

func (m *Manager) RespondPermission(id, requestID, optionID string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	return s.RespondPermission(requestID, optionID)
}

func (m *Manager) Close(id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	s.Close()
	return nil
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	list := make([]*Session, 0, len(m.sessions))
	for id, s := range m.sessions {
		list = append(list, s)
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	for _, s := range list {
		s.Close()
	}
}
