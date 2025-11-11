package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"example.com/dsms-chat/internal/httpapi"
	"example.com/dsms-chat/internal/multiraft"
)

func mustGetEnv(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func main() {
	// 节点基本信息
	nodeID   := mustGetEnv("NODE_ID", "node1")
	httpAddr := mustGetEnv("HTTP_ADDR", ":8081")

	// 默认组（可理解为“general 会话”的 raft 组）
	groupID  := mustGetEnv("GROUP_ID", "general")
	raftAddr := mustGetEnv("RAFT_ADDR", "node1:12001") // 本节点在该组内的可通告地址
	bootstrap := os.Getenv("BOOTSTRAP") == "true"      // 该组是否由本节点引导
	joinAddr  := os.Getenv("JOIN_ADDR")                // 若非引导，指向某已在该组的 leader 的 HTTP，如 http://node1:8081

	// 1) 初始化组管理器
	gm := multiraft.NewManager(nodeID, "/data")

	// 2) 在本机创建该默认组（注意：仅创建本机这一份；真正形成集群靠 join）
	_, err := gm.Create(multiraft.GroupOptions{
		GroupID:  groupID,
		DataRoot: "/data",
		RaftAddr: raftAddr,
		Bootstrap: bootstrap,
	})
	if err != nil {
		log.Fatalf("create raft group %s failed: %v", groupID, err)
	}


	// 4) 创建 HTTP API（v2 多组）
	api := httpapi.NewWithManager(gm)

	if g, ok := gm.Get(groupID); ok {
	api.SetSingleNode(g.Node)
}

	// 3) 如果不是 bootstrap 节点，则尝试向现有 leader 发 /api/v2/groups/{gid}/join
	if !bootstrap && joinAddr != "" {
		go func() {
			backoff := 500 * time.Millisecond
			payload := map[string]string{"ID": nodeID, "RaftAddr": raftAddr}
			body, _ := json.Marshal(payload)

			for {
				url := fmt.Sprintf("%s/api/v2/groups/%s/join", joinAddr, groupID)
				req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")

				resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
				if err != nil {
					log.Printf("join group %s via %s failed (retrying): %v", groupID, joinAddr, err)
				} else {
					_ = resp.Body.Close()
					if resp.StatusCode/100 == 2 {
						log.Printf("joined group %s via %s", groupID, joinAddr)
						return
					}
					log.Printf("join group %s via %s got http %d (retrying)", groupID, joinAddr, resp.StatusCode)
				}

				time.Sleep(backoff)
				if backoff < 5*time.Second {
					backoff *= 2
				}
			}
		}()
	} else if !bootstrap && joinAddr == "" {
		log.Printf("[WARN] not bootstrap and no JOIN_ADDR provided; this node will form a single-node group until others join it.")
	} else {
		log.Printf("[BOOTSTRAP] %s: waiting for other nodes to join group %q ...", nodeID, groupID)
	}


	// （可选）如果你还想保留 v1 兼容路由：
	//   - 取回默认组对应的本机节点，作为 v1 的单组节点注入到 API（需要 httpapi 提供一个设置函数）
	//     例如：api.SetSingleNode(gm.MustGet(groupID).Node)
	//   目前的 httpapi.NewWithManager 里没有 setter，v1 路由会返回 “single-raft node not configured”，这是预期的。

	log.Printf("HTTP listen on %s", httpAddr)
	if err := http.ListenAndServe(httpAddr, api.Router()); err != nil {
		log.Fatal(err)
	}
}
