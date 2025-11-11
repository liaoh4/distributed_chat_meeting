package multiraft

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"example.com/dsms-chat/internal/raftnode"
)

type GroupOptions struct {
	GroupID   string // 例如 "general"、"room-123"
	DataRoot  string // 例如 "/data"
	RaftAddr  string // 本节点在该组的可通告地址，如 "node1:12101"
	Bootstrap bool   // 是否由本节点引导该组（仅该组第一个节点为 true）
	JoinTo    string // 可选：HTTP 地址（http://nodeX:808X），留空时由调用方自行 join
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

// Get 返回组以及是否存在
func (m *Manager) Get(id string) (*Group, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	g, ok := m.groups[id]
	return g, ok
}

// MustGet 不存在则 panic（可选）
func (m *Manager) MustGet(id string) *Group {
	if g, ok := m.Get(id); ok {
		return g
	}
	panic("group not found: " + id)
}

// ListIDs 返回所有组 ID，按字母序排序
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

// Create 在本机创建/引导一个组；如果 Bootstrap=true 则自举（形成单节点集群）
// 注意：加入其它已有组的操作（join）建议在上层通过 HTTP /join 完成。
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
		ID:        m.NodeID,   // 本机节点 ID（在所有组内一致）
		DataDir:   dataDir,    // 形如 /data/<groupID>
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

// Remove 关闭并移除本机上的某个组
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

// Shutdown 关闭本机上管理的所有组（优雅退出）
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, g := range m.groups {
		_ = g.Node.Close()
		delete(m.groups, id)
	}
}
