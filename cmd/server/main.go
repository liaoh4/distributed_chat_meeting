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

func getenv(k string) string {
	return os.Getenv(k)
}

func main() {
	nodeID := getenv("NODE_ID")
	httpAddr := getenv("HTTP_ADDR")
	groupID := getenv("GROUP_ID") 
	raftAddr := getenv("RAFT_ADDR")
	bootstrap := getenv("BOOTSTRAP") == "true"
	joinAddr := getenv("JOIN_ADDR")

	gm := multiraft.NewManager(nodeID, "/data")
	api := httpapi.NewWithManager(gm)

	// if groupID is empty, run multi-group mode
	if groupID == "" {
		log.Printf("[INFO] Node %s running in MULTI-GROUP MODE", nodeID)
		startServer(httpAddr, api)
		return
	}

	// create default group
	_, err := gm.Create(multiraft.GroupOptions{
		GroupID:   groupID,
		DataRoot:  "/data",
		RaftAddr:  raftAddr,
		Bootstrap: bootstrap,
	})
	if err != nil {
		log.Fatalf("create group %s failed: %v", groupID, err)
	}

	// ★  automatically join（ bootstrap = faulse）
	if !bootstrap && joinAddr != "" {
		go autoJoin(nodeID, groupID, raftAddr, joinAddr)
	}

	log.Printf("HTTP listen on %s", httpAddr)
	startServer(httpAddr, api)
}

func startServer(addr string, api *httpapi.API) {
	if err := http.ListenAndServe(addr, api.Router()); err != nil {
		log.Fatal(err)
	}
}

func autoJoin(nodeID, gid, raftAddr, joinAddr string) {
	payload := map[string]string{
		"ID":       nodeID,
		"RaftAddr": raftAddr,
	}
	body, _ := json.Marshal(payload)

	for {
		url := fmt.Sprintf("%s/api/groups/%s/join", joinAddr, gid)
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
		if err == nil && resp.StatusCode/100 == 2 {
			log.Printf("[JOIN] %s joined group %s", nodeID, gid)
			return
		}

		time.Sleep(time.Second)
	}
}
