package httpapi

import (
	"bytes"
	"context"
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
	return r
}

func (a *API) handleJoin(w http.ResponseWriter, r *http.Request) {
	var req struct{ ID, RaftAddr string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, err.Error(), 400); return }
	if !a.n.IsLeader() { http.Error(w, "not leader", 409); return }
	if err := a.n.AddVoter(req.ID, req.RaftAddr); err != nil { http.Error(w, err.Error(), 500); return }
	w.WriteHeader(200)
	w.Write([]byte("ok"))
}

func (a *API) handlePostMessage(w http.ResponseWriter, r *http.Request) {
    // 先把原始 body 读到内存，避免转发时 body 已被消费
    raw, err := io.ReadAll(r.Body)
    if err != nil { http.Error(w, "read body error", 400); return }
    _ = r.Body.Close()

    // 为了校验/本地提交，解析一份副本
    var msg types.Message
    if err := json.Unmarshal(raw, &msg); err != nil {
        http.Error(w, "invalid json", 400)
        return
    }

    // 如果不是 leader，转发到 leader（保持原始 body）
    if !a.n.IsLeader() {
        leaderRaft := a.n.LeaderAddr() // e.g., "node1:12001"
        if leaderRaft == "" {
            http.Error(w, "no leader available", 503)
            return
        }
        // 规则：12001 -> 8081（MVP 约定：1200x ↔ 808x）
        parts := strings.Split(leaderRaft, ":")
        leaderHTTP := leaderRaft
        if len(parts) == 2 && strings.HasPrefix(parts[1], "1200") {
            leaderHTTP = parts[0] + ":" + strings.Replace(parts[1], "1200", "808", 1)
        }
        url := fmt.Sprintf("http://%s/api/v1/message", leaderHTTP)

        req, err := http.NewRequest("POST", url, bytes.NewReader(raw))
        if err != nil { http.Error(w, err.Error(), 500); return }
        req.Header.Set("Content-Type", "application/json")

        client := &http.Client{ Timeout: 6 * time.Second }
        resp, err := client.Do(req)
        if err != nil { http.Error(w, fmt.Sprintf("forward failed: %v", err), 502); return }
        defer resp.Body.Close()
        w.WriteHeader(resp.StatusCode)
        io.Copy(w, resp.Body)
        return
    }

    // 本节点是 leader：走本地提交
    ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
    defer cancel()
    ch := make(chan error, 1)
    go func() { ch <- a.n.AppendMessage(msg) }()
    select {
    case err := <-ch:
        if err != nil { http.Error(w, err.Error(), 500); return }
        w.WriteHeader(202)
        w.Write([]byte("accepted"))
    case <-ctx.Done():
        http.Error(w, "timeout", 504)
    }
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
