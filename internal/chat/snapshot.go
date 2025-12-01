package chat

import (
	"log"

	"github.com/hashicorp/raft"
)

type FSMSnapshot struct {
    Data []byte
}

func (s *FSMSnapshot) Persist(sink raft.SnapshotSink) error {
	 log.Printf("[DEBUG] Persisting snapshot size=%d bytes", len(s.Data))
    _, err := sink.Write(s.Data)
    if err != nil {
        sink.Cancel()
        return err
    }
    return sink.Close()
}

func (s *FSMSnapshot) Release() {}
