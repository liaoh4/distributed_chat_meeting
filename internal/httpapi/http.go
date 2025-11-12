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
	"example.com/dsms-chat/internal/raftnode"
	"example.com/dsms-chat/internal/types"
	"github.com/go-chi/chi/v5"
	"github.com/rs/cors"
)

type API struct {
	n  *raftnode.Node     // v1 单组用
	gm *multiraft.Manager // v2 多组用

}

func New(n *raftnode.Node) *API                 { return &API{n: n} }
func NewWithManager(gm *multiraft.Manager) *API { return &API{gm: gm} }

func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(cors.AllowAll().Handler)

	// ---- v2: multi-raft groups ----
	r.Post("/api/v2/groups", a.handleCreateGroup)                        // 本机创建/引导某个组
	r.Post("/api/v2/groups/{gid}/join", a.handleJoinGroup)               // 把某节点加入该组（对组 leader 调用）
	r.Delete("/api/v2/groups/{gid}/nodes/{id}", a.handleRemoveGroupNode) // 从该组移除节点
	r.Post("/api/v2/groups/{gid}/message", a.handlePostMessageV2)        // 发消息到某组
	r.Get("/api/v2/groups/{gid}/stream", a.handleStreamV2)               // 订阅某组
	r.Get("/api/v2/groups/{gid}/status", a.handleStatusV2)               // 该组状态
	r.Get("/api/v2/groups", a.handleListGroups)
	r.Post("/api/v2/groups/{gid}/leave", a.handleLeaveV2) // ✅ 新增

	// ---- v1: 单组兼容接口（默认绑定到 a.n）----
	r.Post("/join", a.handleJoin)
	r.Post("/api/v1/message", a.handlePostMessage)
	r.Get("/api/v1/stream", a.handleStream)

	// 节点管理（单组）
	r.Post("/leave", a.handleLeave)
	r.Delete("/nodes/{id}", a.handleRemoveNode)
	r.Get("/status", a.handleStatus)

	return r
}

// ===== v1 单组 =====

func (a *API) handleJoin(w http.ResponseWriter, r *http.Request) {

	if a.n == nil {
		http.Error(w, "single-raft node not configured", http.StatusNotFound)
		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body error", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	// follower -> 转发
	if !a.n.IsLeader() {
		leaderRaft := a.n.LeaderAddr()
		if leaderRaft == "" {
			http.Error(w, "no leader available", http.StatusServiceUnavailable)
			return
		}

		leaderHTTP, err := raftToHTTP(leaderRaft)
		if err != nil {
			http.Error(w, "cannot map leader addr: "+err.Error(), http.StatusBadGateway)
			return
		}
		
		url := fmt.Sprintf("http://%s/join", leaderHTTP)

		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("forward failed: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	// leader 执行
	var reqBody struct{ ID, RaftAddr string }
	if err := json.Unmarshal(raw, &reqBody); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := a.n.AddVoter(reqBody.ID, reqBody.RaftAddr); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (a *API) handlePostMessage(w http.ResponseWriter, r *http.Request) {

	if a.n == nil {
		http.Error(w, "single-raft node not configured", http.StatusNotFound)
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

	if !a.n.IsLeader() {
		leaderRaft := a.n.LeaderAddr()
		if leaderRaft == "" {
			http.Error(w, "no leader available", http.StatusServiceUnavailable)
			return
		}
		leaderHTTP, err := raftToHTTP(leaderRaft)
		if err != nil {
			http.Error(w, "cannot map leader addr: "+err.Error(), http.StatusBadGateway)
			return
		}
		url := fmt.Sprintf("http://%s/api/v1/message", leaderHTTP)

		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("forward failed: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	if err := a.n.AppendMessage(msg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("accepted"))
}

func (a *API) handleStream(w http.ResponseWriter, r *http.Request) {
	if a.n == nil {
		http.Error(w, "single-raft node not configured", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	from := parseFrom(r.URL.Query().Get("from"))
	a.replay(a.n.FSM().Store(), from, w)

	flusher, _ := w.(http.Flusher)
	ch, cancel := a.n.FSM().Subscribe()
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

func (a *API) handleLeave(w http.ResponseWriter, r *http.Request) {

	if a.n == nil {
		http.Error(w, "single-raft node not configured", http.StatusNotFound)
		return
	}
	id := a.n.GetID()
	if a.n.IsLeader() {
		if err := a.n.RemoveServer(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("left"))
		return
	}
	leaderRaft := a.n.LeaderAddr()
	if leaderRaft == "" {
		http.Error(w, "no leader available", http.StatusServiceUnavailable)
		return
	}
	leaderHTTP, err := raftToHTTP(leaderRaft)
	if err != nil {
		http.Error(w, "cannot map leader addr: "+err.Error(), http.StatusBadGateway)
		return
	}
	url := fmt.Sprintf("http://%s/leave", leaderHTTP)
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("forward failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (a *API) handleRemoveNode(w http.ResponseWriter, r *http.Request) {

	if a.n == nil {
		http.Error(w, "single-raft node not configured", http.StatusNotFound)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if !a.n.IsLeader() {
		leaderRaft := a.n.LeaderAddr()
		if leaderRaft == "" {
			http.Error(w, "no leader available", http.StatusServiceUnavailable)
			return
		}

		leaderHTTP, err := raftToHTTP(leaderRaft)
		if err != nil {
			http.Error(w, "cannot map leader addr: "+err.Error(), http.StatusBadGateway)
			return
		}
		url := fmt.Sprintf("http://%s/nodes/%s", leaderHTTP, id)

		req, _ := http.NewRequest(http.MethodDelete, url, nil)
		resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("forward failed: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}
	if err := a.n.RemoveServer(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("removed"))
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	if a.n == nil {
		http.Error(w, "single-raft node not configured", http.StatusNotFound)
		return
	}
	st := a.n.Status()
	st["id"] = a.n.GetID()
	st["is_leader"] = a.n.IsLeader()
	writeJSON(w, st)
}

// ===== v2 多组 =====

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
		url := fmt.Sprintf("http://%s/api/v2/groups/%s/join", leaderHTTP, gid)

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

func (a *API) handlePostMessageV2(w http.ResponseWriter, r *http.Request) {
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
		url := fmt.Sprintf("http://%s/api/v2/groups/%s/message", leaderHTTP, gid)

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

func (a *API) handleStreamV2(w http.ResponseWriter, r *http.Request) {
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

func (a *API) handleStatusV2(w http.ResponseWriter, r *http.Request) {
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

// 自退：POST /api/v2/groups/{gid}/leave
// 语义：让“当前节点”（在该组的 ID）离开组。
// 规则：必须由组的 leader 执行；若本机不是 leader，则转发给 leader 的 /api/v2/groups/{gid}/nodes/{selfID}
func (a *API) handleLeaveV2(w http.ResponseWriter, r *http.Request) {
	g, gid, err := a.groupOf(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	selfID := g.Node.GetID()

	// 如果本机不是该组的 leader，转发到 leader 删除自己
	if !g.Node.IsLeader() {
		leaderRaft := g.Node.LeaderAddr()
		if leaderRaft == "" {
			http.Error(w, "no leader available", http.StatusServiceUnavailable)
			return
		}
		// 约定映射：nodeX:1200Y -> nodeX:808Y

		leaderHTTP, err := raftToHTTP(leaderRaft)
		if err != nil {
			http.Error(w, "cannot map leader addr: "+err.Error(), http.StatusBadGateway)
			return
		}

		url := fmt.Sprintf("http://%s/api/v2/groups/%s/nodes/%s", leaderHTTP, gid, selfID)

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

	// 本机是 leader：直接移除自己
	if err := g.Node.RemoveServer(selfID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// （可选）等待配置落地一点点再返回，体验更好；也可直接返回 200。
	if ok := waitUntilRemoved(g, selfID, 5*time.Second); !ok {
		log.Printf("[WARN] node %s removal not confirmed in time", selfID)
		// 也可以仅记录日志，仍返回 200；这取决于你的 UX 取舍
	}

	// 这里简单返回：
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("left"))
}

// ===== 公共小工具 =====

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

// 把 "node1:12001" -> "node1:8081"，简化你现有逻辑

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// SetSingleNode 允许在多组模式下绑定一个默认 raftnode.Node
// 这样旧的 v1 接口 (/api/v1/...) 仍然可以正常工作。
func (a *API) SetSingleNode(n *raftnode.Node) {
	a.n = n
}

func waitUntilRemoved(g *multiraft.Group, id string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st := g.Node.Status()
		cfg, _ := st["latest_configuration"].(string)
		if !strings.Contains(cfg, "ID:"+id+" ") { // 粗糙匹配，足够用了
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}

// raftToHTTP 将 "host:12abc" 映射成 "host:808x"，规则：取 Raft 端口最后一位，映射到 808x。
// 例如：12001->8081, 12102->8082, 12203->8083 ...
func raftToHTTP(raftAddr string) (string, error) {
	host, portStr, err := net.SplitHostPort(raftAddr)
	if err != nil {
		return "", fmt.Errorf("bad raft addr %q: %v", raftAddr, err)
	}
	if len(portStr) < 5 || portStr[0:2] != "12" {
		return "", fmt.Errorf("cannot map raft port %s to http", portStr)
	}
	last := portStr[len(portStr)-1:] // 取最后一位
	return net.JoinHostPort(host, "808"+last), nil
}
