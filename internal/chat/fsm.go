package chat

import (
	"encoding/json"
	"io"
	"log"
	"sync"

	"example.com/dsms-chat/internal/types"
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
)

type FSM struct {
	store *Store
	// Index -> message; only for fast fanout (non-authoritative; persisted in store)
	subMu sync.RWMutex
	subs  map[chan types.Message]struct{}
}

func NewFSM(store *Store) *FSM {
	return &FSM{store: store, subs: make(map[chan types.Message]struct{})}
}

func (f *FSM) Store() *Store { return f.store }

func (f *FSM) Apply(l *raft.Log) any {
	var entry types.RaftLog
	if err := json.Unmarshal(l.Data, &entry); err != nil {
		log.Printf("FSM unmarshal err: %v", err)
		return nil
	}
	if entry.Op == "append" {
		msg := entry.Msg
		if msg.MsgID == "" {
			msg.MsgID = uuid.NewString()
		}
		msg.CommitIndex = l.Index
		// persist
		if err := f.store.Put(l.Index, msg); err != nil {
			log.Printf("store put err: %v", err)
		}
		// notify subscribers (non-blocking)
		f.subMu.RLock()
		for ch := range f.subs {
			select {
			case ch <- msg:
			default:
			}
		}
		f.subMu.RUnlock()
	}
	return nil
}


func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
    raw, err := f.store.Export()
    if err != nil {
		log.Printf("[DEBUG] Snapshot Export error: %v", err)
        return nil, err
    }
	log.Printf("[DEBUG] Snapshot created: size=%d bytes", len(raw))
    return &FSMSnapshot{Data: raw}, nil
}

func (f *FSM) Restore(rc io.ReadCloser) error {
    defer rc.Close()
    data, err := io.ReadAll(rc)
    if err != nil {
        return err
    }
	log.Printf("[DEBUG] Restoring snapshot: size=%d bytes", len(data))
    return f.store.Import(data)
}


// Publish utilities

func (f *FSM) Subscribe() (<-chan types.Message, func()) {
	ch := make(chan types.Message, 256)
	f.subMu.Lock()
	f.subs[ch] = struct{}{}
	f.subMu.Unlock()
	cancel := func() { f.subMu.Lock(); delete(f.subs, ch); close(ch); f.subMu.Unlock() }
	return ch, cancel
}


