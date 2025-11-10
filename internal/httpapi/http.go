package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.com/dsms-chat/internal/raftnode"
	"example.com/dsms-chat/internal/types"
	"github.com/go-chi/chi/v5"
	"github.com/rs/cors"
)

type API struct { n *raftnode.Node }

func New(n *raftnode.Node) *API { return &API{n: n} }

func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(cors.AllowAll().Handler)

	r.Post("/join", a.handleJoin)
	r.Post("/api/v1/message", a.handlePostMessage)
	r.Get("/api/v1/stream", a.handleStream)
	//v2
	r.Post("/leave", a.handleLeave)
	r.Delete("/nodes/{id}", a.handleRemoveNode)
	r.Get("/status", a.handleStatus)

	return r
}

func (a *API) handleJoin(w http.ResponseWriter, r *http.Request) {
    raw, err := io.ReadAll(r.Body)
    if err != nil { http.Error(w, "read body error", http.StatusBadRequest); return }
    _ = r.Body.Close()

    if !a.n.IsLeader() {
        leaderRaft := a.n.LeaderAddr()
        if leaderRaft == "" { http.Error(w, "no leader available", http.StatusServiceUnavailable); return }

        parts := strings.Split(leaderRaft, ":")
        leaderHTTP := leaderRaft
        if len(parts) == 2 && strings.HasPrefix(parts[1], "1200") {
            leaderHTTP = parts[0] + ":" + strings.Replace(parts[1], "1200", "808", 1)
        }
        url := "http://" + leaderHTTP + "/join"

        req, _ := http.NewRequest("POST", url, bytes.NewReader(raw))
        req.Header.Set("Content-Type", "application/json")
        resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
        if err != nil { http.Error(w, fmt.Sprintf("forward failed: %v", err), http.StatusBadGateway); return }
        defer resp.Body.Close()
        w.WriteHeader(resp.StatusCode)
        io.Copy(w, resp.Body)
        return
    }

    // 真正由 leader 执行添加
    var reqBody struct{ ID, RaftAddr string }
    if err := json.Unmarshal(raw, &reqBody); err != nil {
        http.Error(w, "invalid json", http.StatusBadRequest); return
    }
    if err := a.n.AddVoter(reqBody.ID, reqBody.RaftAddr); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError); return
    }
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))
}


func (a *API) handlePostMessage(w http.ResponseWriter, r *http.Request) {
    // 先把原始 body 读入内存，避免被提前消费
    raw, err := io.ReadAll(r.Body)
    if err != nil {
        http.Error(w, "read body error", http.StatusBadRequest)
        return
    }
    _ = r.Body.Close()

    // 解析一份副本用于本地提交校验
    var msg types.Message
    if err := json.Unmarshal(raw, &msg); err != nil {
        http.Error(w, "invalid json", http.StatusBadRequest)
        return
    }

    // 非 leader：转发到 leader
    if !a.n.IsLeader() {
        leaderRaft := a.n.LeaderAddr() // 例如 node1:12001
        if leaderRaft == "" {
            http.Error(w, "no leader available", http.StatusServiceUnavailable)
            return
        }
        // 约定映射：12001 -> 8081, 12002 -> 8082 ...
        parts := strings.Split(leaderRaft, ":")
        leaderHTTP := leaderRaft
        if len(parts) == 2 && strings.HasPrefix(parts[1], "1200") {
            leaderHTTP = parts[0] + ":" + strings.Replace(parts[1], "1200", "808", 1)
        }
        url := fmt.Sprintf("http://%s/api/v1/message", leaderHTTP)

        req, err := http.NewRequest("POST", url, bytes.NewReader(raw))
        if err != nil { http.Error(w, err.Error(), 500); return }
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

    // leader：本地提交
    if err := a.n.AppendMessage(msg); err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    w.WriteHeader(202)
    w.Write([]byte("accepted"))
}


func (a *API) handleStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	fromStr := r.URL.Query().Get("from")
	from := uint64(1)
	if fromStr != "" {
		if n, err := strconv.ParseUint(fromStr, 10, 64); err == nil {
			from = n
		}
	}
	a.replay(from, w)

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

func (a *API) replay(from uint64, w http.ResponseWriter) {
	flusher, _ := w.(http.Flusher)
	_ = a.n.FSM().Store().Range(from, func(idx uint64, raw []byte) error {
		w.Write([]byte("data: "))
		w.Write(raw)
		w.Write([]byte("\n\n"))
		flusher.Flush()
		return nil
	})
}

func (a *API) JoinCluster(leaderHTTP, id, raftAddr string) error {
	body := fmt.Sprintf(`{"ID":"%s","RaftAddr":"%s"}`, id, raftAddr)
	resp, err := http.Post(leaderHTTP+"/join", "application/json", io.NopCloser(strings.NewReader(body)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		bs, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("join failed: %s", string(bs))
	}
	return nil
}

// 1) 自退：POST /leave  （在本节点调用即可）
//    - 如果本节点是 leader：直接 Remove 自己，再返回 200（随后由编排回收容器）
//    - 如果本节点是 follower：转发给 leader 的 /nodes/{id}，移除自己
func (a *API) handleLeave(w http.ResponseWriter, r *http.Request) {
    id := a.n.GetID() // 需要在 raftnode.Node 暴露 ID()
    if a.n.IsLeader() {
        if err := a.n.RemoveServer(id); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }
        w.WriteHeader(200); w.Write([]byte("left"))
        // 可选：优雅关闭 Raft（不在此处直接退出进程，交给编排）
        return
    }
    // 转发给 leader：DELETE /nodes/{id}
    leaderRaft := a.n.LeaderAddr()
    if leaderRaft == "" { http.Error(w, "no leader available", http.StatusServiceUnavailable); return }
    parts := strings.Split(leaderRaft, ":")
    leaderHTTP := leaderRaft
    if len(parts) == 2 && strings.HasPrefix(parts[1], "1200") {
        leaderHTTP = parts[0] + ":" + strings.Replace(parts[1], "1200", "808", 1)
    }
    url := fmt.Sprintf("http://%s/nodes/%s", leaderHTTP, id)
    req, _ := http.NewRequest("DELETE", url, nil)
    resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
    if err != nil { http.Error(w, fmt.Sprintf("forward failed: %v", err), http.StatusBadGateway); return }
    defer resp.Body.Close()
    w.WriteHeader(resp.StatusCode)
    io.Copy(w, resp.Body)
}

// 2) 管理移除：DELETE /nodes/{id}（仅 leader 允许调用；follower 自动转发）
func (a *API) handleRemoveNode(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    if id == "" { http.Error(w, "missing id", 400); return }

    if !a.n.IsLeader() {
        leaderRaft := a.n.LeaderAddr()
        if leaderRaft == "" { http.Error(w, "no leader available", http.StatusServiceUnavailable); return }
        parts := strings.Split(leaderRaft, ":")
        leaderHTTP := leaderRaft
        if len(parts) == 2 && strings.HasPrefix(parts[1], "1200") {
            leaderHTTP = parts[0] + ":" + strings.Replace(parts[1], "1200", "808", 1)
        }
        url := fmt.Sprintf("http://%s/nodes/%s", leaderHTTP, id)
        req, _ := http.NewRequest("DELETE", url, nil)
        resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
        if err != nil { http.Error(w, fmt.Sprintf("forward failed: %v", err), http.StatusBadGateway); return }
        defer resp.Body.Close()
        w.WriteHeader(resp.StatusCode)
        io.Copy(w, resp.Body)
        return
    }

    if err := a.n.RemoveServer(id); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }
    w.WriteHeader(200); w.Write([]byte("removed"))
}

// 3) 状态：GET /status
func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
    st := a.n.Status()
    st["id"] = a.n.GetID()
    st["is_leader"] = a.n.IsLeader()
    bs, _ := json.Marshal(st)
    w.Header().Set("Content-Type", "application/json")
    w.Write(bs)
}
