package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"example.com/dsms-chat/internal/httpapi"
	"example.com/dsms-chat/internal/raftnode"
)

func mustGetEnv(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func main() {
	nodeID := mustGetEnv("NODE_ID", "node1")
	httpAddr := mustGetEnv("HTTP_ADDR", ":8081")
	raftAddr := mustGetEnv("RAFT_ADDR", ":12001")
	bootstrap := os.Getenv("BOOTSTRAP") == "true"
	joinAddr := os.Getenv("JOIN_ADDR") // e.g., http://node1:8081

	n, err := raftnode.New(nodeID, httpAddr, raftAddr, bootstrap)
	if err != nil {
		log.Fatalf("raftnode: %v", err)
	}
	api := httpapi.New(n)

	go func() {
    if os.Getenv("BOOTSTRAP") == "true" {
        // node1：自举为 leader，等待其它节点来 join
        log.Println("[BOOTSTRAP] waiting for followers to join...")
        return
    }
    if joinAddr == "" {
        return
    }

    backoff := 500 * time.Millisecond
    for {
        if err := api.JoinCluster(joinAddr, nodeID, raftAddr); err != nil {
            // 409 not leader / 5xx / 连接错误 等，都重试
            log.Printf("join cluster failed (will retry): %v", err)
            time.Sleep(backoff)
            if backoff < 5*time.Second {
                backoff *= 2
            }
            continue
        }
        log.Printf("joined cluster via %s", joinAddr)
        return
    }
}()


	log.Printf("HTTP listen on %s", httpAddr)
	if err := http.ListenAndServe(httpAddr, api.Router()); err != nil {
		log.Fatal(err)
	}
}
