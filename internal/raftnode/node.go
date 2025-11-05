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

type Node struct {
	ID       string
	HTTPAddr string
	RaftAddr string
	raft     *raft.Raft
	fsm      *chat.FSM
}

func New(nodeID, httpAddr, raftAddr string, bootstrap bool) (*Node, error) {
	dir := filepath.Join("/data")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	store, err := chat.NewStore(filepath.Join(dir, "messages.db"))
	if err != nil {
		return nil, err
	}
	fsm := chat.NewFSM(store)

	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(nodeID)
	config.SnapshotThreshold = 8192
	config.HeartbeatTimeout = 200 * time.Millisecond
	config.ElectionTimeout = 800 * time.Millisecond
	config.LeaderLeaseTimeout = 200 * time.Millisecond
	config.CommitTimeout = 50 * time.Millisecond

	addr, err := net.ResolveTCPAddr("tcp", raftAddr)
	if err != nil {
		return nil, err
	}
	transport, err := raft.NewTCPTransport(raftAddr, addr, 3, 5*time.Second, os.Stderr)
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

	n := &Node{ID: nodeID, HTTPAddr: httpAddr, RaftAddr: raftAddr, raft: r, fsm: fsm}

	if bootstrap {
		configuration := raft.Configuration{
			Servers: []raft.Server{{ID: raft.ServerID(nodeID), Address: raft.ServerAddress(raftAddr)}},
		}
		r.BootstrapCluster(configuration)
	}
	return n, nil
}

// Propose append message via Raft
func (n *Node) AppendMessage(msg types.Message) error {
	msg.ConvID = "general" // MVP 固定会话
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

func (n *Node) FSM() *chat.FSM { return n.fsm }
