package multiraft

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"example.com/dsms-chat/internal/raftnode"
)

type GroupOptions struct {
	GroupID   string 
	DataRoot  string 
	RaftAddr  string 
	Bootstrap bool   
	JoinTo    string 
}

type Group struct {
	ID   string
	Node *raftnode.Node
}

type Manager struct {
	NodeID   string
	DataRoot string

	mu     sync.RWMutex
	groups map[string]*Group
}

func NewManager(nodeID, dataRoot string) *Manager {
	return &Manager{
		NodeID:  nodeID,
		DataRoot: dataRoot,
		groups:  make(map[string]*Group),
	}
}

// Get: return the group if exists
func (m *Manager) Get(id string) (*Group, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	g, ok := m.groups[id]
	return g, ok
}

// MustGet if non-exists, then panic
func (m *Manager) MustGet(id string) *Group {
	if g, ok := m.Get(id); ok {
		return g
	}
	panic("group not found: " + id)
}

// ListIDs: return all groups has been in
func (m *Manager) ListIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.groups))
	for k := range m.groups {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Create a group, if Bootstrap=true, become the leaderleader
func (m *Manager) Create(opt GroupOptions) (*Group, error) {
	if opt.GroupID == "" {
		return nil, fmt.Errorf("group_id is required")
	}
	if opt.RaftAddr == "" {
		return nil, fmt.Errorf("raft_addr is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.groups[opt.GroupID]; ok {
		return nil, fmt.Errorf("group %s already exists", opt.GroupID)
	}

	dataDir := filepath.Join(m.DataRoot, opt.GroupID)

	node, err := raftnode.NewWithOpts(raftnode.Options{
		ID:        m.NodeID,   
		DataDir:   dataDir,    
		RaftAddr:  opt.RaftAddr,
		Bootstrap: opt.Bootstrap,
	})
	if err != nil {
		return nil, err
	}

	g := &Group{ID: opt.GroupID, Node: node}
	m.groups[opt.GroupID] = g
	return g, nil
}

// Remove: close and remove a group
func (m *Manager) Remove(groupID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.groups[groupID]
	if !ok {
		return nil
	}
	_ = g.Node.Close()
	delete(m.groups, groupID)
	return nil
}

// Shutdown all groups and graceful exit
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, g := range m.groups {
		_ = g.Node.Close()
		delete(m.groups, id)
	}
}
