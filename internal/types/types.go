package types

type VectorClock map[string]int64

type Message struct {
	MsgID       string      `json:"msg_id"`
	ConvID      string      `json:"conv_id"`
	Sender      string      `json:"sender"`
	Payload     string      `json:"payload"`
	ClientTS    int64       `json:"client_ts"`
	Lamport     uint64      `json:"lamport"`
	VectorClock VectorClock `json:"vector_clock"`
	CommitIndex uint64      `json:"commit_index"`
}

// RaftLog wraps a message for FSM.Apply

type RaftLog struct {
	Op  string  `json:"op"` // "append"
	Msg Message `json:"msg"`
}
