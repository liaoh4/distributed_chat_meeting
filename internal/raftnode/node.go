package raftnode

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"time"

	"example.com/dsms-chat/internal/chat"
	"example.com/dsms-chat/internal/types"
	"github.com/hashicorp/raft"
	"github.com/hashicorp/raft-boltdb/v2"
)

type Options struct {
	ID        string
	DataDir   string // ★ 每个组单独的数据目录，例如 /data/<groupID>
	RaftAddr  string // ★ 本节点在该组内的可通告地址，例如 "node1:12101"
	Bootstrap bool
}

type Node struct {
	ID       string
	HTTPAddr string
	RaftAddr string

	raft *raft.Raft
	fsm  *chat.FSM

	// ★ 保留底层资源用于 Close()
	dataDir     string
	logStore    *raftboltdb.BoltStore
	stableStore *raftboltdb.BoltStore
	snaps       *raft.FileSnapshotStore
	transport   *raft.NetworkTransport
	// 如果你的 chat.Store 需要关闭，可从 FSM 里拿出来关闭（见 Close）
}

// ★ 新增：按 Options 构建（多组直接用这个）
func NewWithOpts(opt Options) (*Node, error) {
	dir := opt.DataDir
	if dir == "" {
		dir = "/data" // 兜底：不传就用 /data
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	store, err := chat.NewStore(filepath.Join(dir, "messages.db"))
	if err != nil {
		return nil, err
	}
	fsm := chat.NewFSM(store)

	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(opt.ID)
	config.SnapshotInterval = 20 * time.Second 
	config.SnapshotThreshold = 2
	config.HeartbeatTimeout = 200 * time.Millisecond
	config.ElectionTimeout = 800 * time.Millisecond
	config.LeaderLeaseTimeout = 200 * time.Millisecond // ≤ HeartbeatTimeout
	config.CommitTimeout = 50 * time.Millisecond

	addr, err := net.ResolveTCPAddr("tcp", opt.RaftAddr)
	if err != nil {
		return nil, err
	}
	transport, err := raft.NewTCPTransport(opt.RaftAddr, addr, 3, 5*time.Second, os.Stderr)
	if err != nil {
		return nil, err
	}

	logStore, err := raftboltdb.NewBoltStore(filepath.Join(dir, "raft-log.db"))
	if err != nil {
		return nil, err
	}
	stableStore, err := raftboltdb.NewBoltStore(filepath.Join(dir, "raft-stable.db"))
	if err != nil {
		return nil, err
	}
	snaps, err := raft.NewFileSnapshotStore(dir, 3, os.Stderr)
	if err != nil {
		return nil, err
	}

	r, err := raft.NewRaft(config, fsm, logStore, stableStore, snaps, transport)
	if err != nil {
		return nil, err
	}

	n := &Node{
		ID:         opt.ID,
		RaftAddr:   opt.RaftAddr,
		raft:       r,
		fsm:        fsm,
		dataDir:    dir,
		logStore:   logStore,
		stableStore: stableStore,
		snaps:      snaps,
		transport:  transport,
	}

	// ★ 仅在引导（该组第一个节点）时 BootstrapCluster
	if opt.Bootstrap {
		configuration := raft.Configuration{
			Servers: []raft.Server{{ID: raft.ServerID(opt.ID), Address: raft.ServerAddress(opt.RaftAddr)}},
		}
		r.BootstrapCluster(configuration)
	}
	return n, nil
}

// ★ 原来的构造器保持兼容：复用 NewWithOpts，只是把 HTTPAddr 也存下来
func New(nodeID, httpAddr, raftAddr string, bootstrap bool) (*Node, error) {
	n, err := NewWithOpts(Options{
		ID:        nodeID,
		DataDir:   "/data",
		RaftAddr:  raftAddr,
		Bootstrap: bootstrap,
	})
	if err != nil {
		return nil, err
	}
	n.HTTPAddr = httpAddr
	return n, nil
}

// 优雅关闭：按顺序关 Raft、传输与存储
func (n *Node) Close() error {
	if n.raft != nil {
		f := n.raft.Shutdown()
		_ = f.Error() // 忽略返回错误以继续清理其他资源
	}
	if n.transport != nil {
		_ = n.transport.Close()
	}
	if n.logStore != nil {
		_ = n.logStore.Close()
	}
	if n.stableStore != nil {
		_ = n.stableStore.Close()
	}
	// FileSnapshotStore 没有 Close 方法，跳过
	// 如需关闭 chat.Store，可在 FSM 暴露 Store() 并 Close：
	if n.fsm != nil && n.fsm.Store() != nil {
		_ = n.fsm.Store().Close()
	}
	return nil
}

// Propose append message via Raft
func (n *Node) AppendMessage(msg types.Message) error {
	// 单组 MVP 固定会话；做多组时由上层按组设置
	if msg.ConvID == "" {
		msg.ConvID = "general"
	}
	entry := types.RaftLog{Op: "append", Msg: msg}
	bs, _ := json.Marshal(entry)
	f := n.raft.Apply(bs, 5*time.Second)
	return f.Error()
}

func (n *Node) IsLeader() bool { return n.raft.State() == raft.Leader }

func (n *Node) LeaderAddr() string {
	addr, _ := n.raft.LeaderWithID()
	return string(addr)
}

func (n *Node) AddVoter(id, raftAddr string) error {
	return n.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(raftAddr), 0, 0).Error()
}

func (n *Node) RemoveServer(id string) error {
	return n.raft.RemoveServer(raft.ServerID(id), 0, 0).Error()
}

func (n *Node) FSM() *chat.FSM { return n.fsm }
func (n *Node) GetID() string  { return n.ID }

func (n *Node) Status() map[string]any {
	stats := n.raft.Stats() // map[string]string
	out := make(map[string]any, len(stats))
	for k, v := range stats {
		out[k] = v
	}
	// 也可补充显式数值指标：
	// out["commit_index"] = n.raft.CommitIndex()
	// out["last_applied"] = n.raft.LastApplied()
	return out
}