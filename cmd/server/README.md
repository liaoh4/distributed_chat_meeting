本示例实现**最小可行版本 (MVP)**：
- 单会话（conv_id 固定为 `general`），3 个副本形成 Raft 组。
- **一致顺序**展示：消息在所有副本上以 Raft 提交顺序（commit index）投递。
- **Leader 选举**：hashicorp/raft。
- **复制一致**：hashicorp/raft 日志复制；状态机 `FSM.Apply` 提交消息。
- **Vector Clock**：客户端发送时可带 `vector_clock`，服务端随消息保存并转发（排序仍以 commit_index 为准）。
- **接口**：
- `POST /api/v1/message` 发送消息（JSON）
- `GET /api/v1/stream` 订阅提交后的消息（Server-Sent Events, SSE）
- `POST /join` 新节点入群（由 docker-compose 启动时调用）

```bash
# 启动三节点集群
docker compose down -v
rm -rf ./data/node1 ./data/node2 ./data/node3 
# 重新构建（清缓存）
docker compose build --no-cache
# 启动
docker compose up

# 发送消息（任意节点均可；建议打到 node1）
curl -X POST http://localhost:8081/api/v1/message \
-H 'Content-Type: application/json' \
-d '{
"conv_id":"general",
"sender":"alice",
"payload":"hello from alice",
"vector_clock":{"alice":1}
}'


# 打开消息流（另开终端）：
curl http://localhost:8081/api/v1/stream
# 或 8082 / 8083 观察顺序一致
```



## 端口说明
- node1: HTTP 8081, Raft 12001
- node2: HTTP 8082, Raft 12002
- node3: HTTP 8083, Raft 12003


## 设计要点
- **SSE** 下发严格使用 `commit_index` 递增；不同副本收到的顺序一致。
- **去重**：`msg_id`（UUID）由服务器填充（若客户端未提供），重复消息在 `Apply` 中丢弃。
- **持久化**：Raft 日志与快照使用本地 `./data/<node>/`；消息存储用嵌入式 BoltDB（可换 LevelDB/RocksDB）。


---

project structure
```
.
├── README.md
├── docker-compose.yml
├── go.mod
├── cmd/server/main.go
├── internal/raftnode/node.go
├── internal/chat/fsm.go
├── internal/chat/store.go
├── internal/httpapi/http.go
└── internal/types/types.go
```

docker compose up --build

# 发消息
curl -X POST http://localhost:8081/api/v1/message \
  -H 'Content-Type: application/json' \
  -d '{"conv_id":"general","sender":"alice","payload":"hello","vector_clock":{"alice":1}}'

# 订阅（观察顺序）
curl http://localhost:8081/api/v1/stream

# 测试用例
for i in $(seq 1 6); do
  node=$(( (i % 3) + 1 ))                     
  port=$((8080 + node))                       
  payload="msg-$i-from-node$node"
  sender="u$node"
  echo "POST -> $port : $payload"
  curl -s -X POST "http://localhost:${port}/api/v1/message" \
    -H 'Content-Type: application/json' \
    -d '{"conv_id":"general","sender":"'"$sender"'","payload":"'"$payload"'","vector_clock":{"'"$sender"'":'"$i"'}}'
  echo
done

# 查看结果， 比较消息是否显示一致
curl -s 'http://localhost:8081/api/v1/stream?from=1'

curl -s 'http://localhost:8082/api/v1/stream?from=1'

curl -s 'http://localhost:8083/api/v1/stream?from=1'
