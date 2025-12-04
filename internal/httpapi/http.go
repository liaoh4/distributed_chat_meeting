package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.com/dsms-chat/internal/multiraft"
	"example.com/dsms-chat/internal/types"
	"github.com/go-chi/chi/v5"
	"github.com/rs/cors"
)

type API struct {
	
	gm *multiraft.Manager 

}


func NewWithManager(gm *multiraft.Manager) *API { return &API{gm: gm} }

func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(cors.AllowAll().Handler)

	// ----  multi-raft groups ----
	r.Post("/api/groups", a.handleCreateGroup)                        // create group
	r.Post("/api/groups/{gid}/join", a.handleJoinGroup)               // add a node
	r.Delete("/api/groups/{gid}/nodes/{id}", a.handleRemoveGroupNode) // leader delete a node
	r.Post("/api/groups/{gid}/message", a.handlePostMessage)        // send message
	r.Get("/api/groups/{gid}/stream", a.handleStream)               // subscribe a group
	r.Get("/api/groups/{gid}/status", a.handleStatus)               // check status of a group
	r.Get("/api/groups", a.handleListGroups)
	r.Post("/api/groups/{gid}/leave", a.handleLeave) // node leaves

	return r
}

func (a *API) groupOf(r *http.Request) (*multiraft.Group, string, error) {
	if a.gm == nil {
		return nil, "", fmt.Errorf("multi-raft manager not configured")
	}
	gid := chi.URLParam(r, "gid")
	if gid == "" {
		return nil, "", fmt.Errorf("missing group_id")
	}
	g, ok := a.gm.Get(gid)
	if !ok {
		return nil, gid, fmt.Errorf("group %s not found", gid)
	}
	return g, gid, nil
}

func (a *API) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID   string `json:"group_id"`
		RaftAddr  string `json:"raft_addr"`
		Bootstrap bool   `json:"bootstrap"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, err := a.gm.Create(multiraft.GroupOptions{
		GroupID: req.GroupID, DataRoot: "/data", RaftAddr: req.RaftAddr, Bootstrap: req.Bootstrap,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("ok"))
}

func (a *API) handleJoinGroup(w http.ResponseWriter, r *http.Request) {
	g, gid, err := a.groupOf(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body error", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	var reqBody struct{ ID, RaftAddr string }
	if err := json.Unmarshal(raw, &reqBody); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if !g.Node.IsLeader() {
		leaderRaft := g.Node.LeaderAddr()
		if leaderRaft == "" {
			http.Error(w, "no leader", http.StatusServiceUnavailable)
			return
		}

		leaderHTTP, err := raftToHTTP(leaderRaft)
		if err != nil {
			http.Error(w, "cannot map leader addr: "+err.Error(), http.StatusBadGateway)
			return
		}
		url := fmt.Sprintf("http://%s/api/groups/%s/join", leaderHTTP, gid)

		resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
		if err != nil {
			http.Error(w, "forward failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}
	if err := g.Node.AddVoter(reqBody.ID, reqBody.RaftAddr); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (a *API) handleRemoveGroupNode(w http.ResponseWriter, r *http.Request) {
	g, _, err := a.groupOf(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if !g.Node.IsLeader() {
		http.Error(w, "not leader", http.StatusConflict)
		return
	}
	if err := g.Node.RemoveServer(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write([]byte("ok"))
}

func (a *API) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	g, gid, err := a.groupOf(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body error", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	var msg types.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if !g.Node.IsLeader() {
		

		leaderRaft := g.Node.LeaderAddr()
		if leaderRaft == "" {
			http.Error(w, "no leader available", http.StatusServiceUnavailable)
			return
		}

		leaderHTTP, err := raftToHTTP(leaderRaft)
		if err != nil {
			http.Error(w, "cannot map leader addr: "+err.Error(), http.StatusBadGateway)
			return
		}
		url := fmt.Sprintf("http://%s/api/groups/%s/message", leaderHTTP, gid)

		resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
		if err != nil {
			http.Error(w, "forward to leader failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	ch := make(chan error, 1)
	go func() { ch <- g.Node.AppendMessage(msg) }()
	select {
	case err := <-ch:
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("accepted"))
	case <-ctx.Done():
		http.Error(w, "timeout", http.StatusGatewayTimeout)
	}
}

func (a *API) handleStream(w http.ResponseWriter, r *http.Request) {
	g, _, err := a.groupOf(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	from := parseFrom(r.URL.Query().Get("from"))
	a.replay(g.Node.FSM().Store(), from, w)

	flusher, _ := w.(http.Flusher)
	ch, cancel := g.Node.FSM().Subscribe()
	defer cancel()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			bs, _ := json.Marshal(msg)
			fmt.Fprintf(w, "data: %s\n\n", string(bs))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	g, _, err := a.groupOf(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, g.Node.Status())
}

func (a *API) handleListGroups(w http.ResponseWriter, r *http.Request) {
	if a.gm == nil {
		writeJSON(w, map[string]any{"groups": []string{}})
		return
	}
	writeJSON(w, map[string]any{"groups": a.gm.ListIDs()})
}


func (a *API) handleLeave(w http.ResponseWriter, r *http.Request) {
	g, gid, err := a.groupOf(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	selfID := g.Node.GetID()

	// if current node is not the leader， forward the command to leader 
	if !g.Node.IsLeader() {
		leaderRaft := g.Node.LeaderAddr()
		if leaderRaft == "" {
			http.Error(w, "no leader available", http.StatusServiceUnavailable)
			return
		}
		// port mapping：nodeX:1200Y -> nodeX:808Y

		leaderHTTP, err := raftToHTTP(leaderRaft)
		if err != nil {
			http.Error(w, "cannot map leader addr: "+err.Error(), http.StatusBadGateway)
			return
		}

		url := fmt.Sprintf("http://%s/api/groups/%s/nodes/%s", leaderHTTP, gid, selfID)

		req, _ := http.NewRequest("DELETE", url, nil)
		resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
		if err != nil {
			http.Error(w, "forward failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	// current node is the leader：remove itself
	if err := g.Node.RemoveServer(selfID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 。
	if ok := waitUntilRemoved(g, selfID, 5*time.Second); !ok {
		log.Printf("[WARN] node %s removal not confirmed in time", selfID)
		
	}

	
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("left"))
}

// ===== public widget =====

func parseFrom(s string) uint64 {
	if s == "" {
		return 1
	}
	if n, err := strconv.ParseUint(s, 10, 64); err == nil {
		return n
	}
	return 1
}

func (a *API) replay(store interface {
	Range(from uint64, fn func(idx uint64, raw []byte) error) error
}, from uint64, w http.ResponseWriter) {
	flusher, _ := w.(http.Flusher)
	_ = store.Range(from, func(idx uint64, raw []byte) error {
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(raw)
		_, _ = w.Write([]byte("\n\n"))
		flusher.Flush()
		return nil
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}


func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func waitUntilRemoved(g *multiraft.Group, id string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st := g.Node.Status()
		cfg, _ := st["latest_configuration"].(string)
		if !strings.Contains(cfg, "ID:"+id+" ") { 
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}

// raftToHTTP maps "host:12abc" to "host:808x"

func raftToHTTP(raftAddr string) (string, error) {
	host, portStr, err := net.SplitHostPort(raftAddr)
	if err != nil {
		return "", fmt.Errorf("bad raft addr %q: %v", raftAddr, err)
	}
	if len(portStr) < 5 || portStr[0:2] != "12" {
		return "", fmt.Errorf("cannot map raft port %s to http", portStr)
	}
	last := portStr[len(portStr)-1:] 
	return net.JoinHostPort(host, "808"+last), nil
}
