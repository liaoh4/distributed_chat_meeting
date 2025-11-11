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
docker compose up -d

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


# 清空本地数据
docker compose down
rm -rf data/*


# 整理和更新依赖关系
 go mod tidy


# 构建
docker compose build
docker compose up -d

# 临时启动 node 并用join加入 raft ,可以通过非leader节点join，因为follower可以转发join请求给leader
docker compose --profile extra up -d node5

curl -X POST http://localhost:8082/join \
  -H 'Content-Type: application/json' \
  -d '{"ID":"node5","RaftAddr":"node5:12005"}'


# 查看状态
curl -s http://localhost:8081/status | jq
curl -s http://localhost:8082/status | jq
curl -s http://localhost:8083/status | jq


# 节点自行退出
curl -X POST http://localhost:8085/leave


# leader移除节点 node2
curl -X DELETE http://localhost:8081/nodes/node5

docker rm -f distributed_chat_meeting-node5-1

rm -rf ./data/node5


# docker临时停止节点
docker compose stop node3
rm -rf ./data/node3              


# docker 启动节点
docker compose up -d node3


# 将被删除的节点重新加入raft
curl -X POST http://localhost:8081/join \
  -H 'Content-Type: application/json' \
  -d '{"ID":"node2","RaftAddr":"node2:12002"}'


# 启动删除节点
docker compose rm -sf node4    # 如存在，先删除
rm -rf ./data/node4              # 可选：清空它的旧数据，避免脏配置


# 发消息
curl -X POST http://localhost:8083/api/v1/message \
  -H 'Content-Type: application/json' \
  -d '{"conv_id":"general","sender":"node1","payload":"Node 5 leaves","vector_clock":{"Cici":1}}'

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

# 查看节点日志
docker compose logs node3 --tail=80

# 测试完后清理临时节点
curl -X DELETE http://localhost:8081/nodes/node4  # 先从raft配置删除，leader节点才能执行该操作
docker compose rm -sf node4 node5  #停止并移除容器

# 关闭并清除所有数据
docker compose down -v --remove-orphans



## ***********以下是V2内容**************

# 构建并启动
docker compose build --no-cache && docker compose up -d

# 发消息
curl -X POST http://localhost:8081/api/v2/groups/general/message \
  -H 'Content-Type: application/json' \
  -d '{"conv_id":"general","sender":"node1","payload":"Hello, world!"}'

  # 测试用例
for i in $(seq 1 6); do
  node=$(( (i % 3) + 1 ))                     
  port=$((8080 + node))                       
  payload="msg-$i-from-node$node"
  sender="u$node"
  echo "POST -> $port : $payload"
  curl -s -X POST "http://localhost:${port}/api/v2/groups/general/message" \
    -H 'Content-Type: application/json' \
    -d '{"conv_id":"general","sender":"'"$sender"'","payload":"'"$payload"'","vector_clock":{"'"$sender"'":'"$i"'}}'
  echo
done


# 查看状态/订阅流

curl http://localhost:8083/api/v2/groups/general/status | jq
curl http://localhost:8085/api/v2/groups/general/stream


# 启动 node4（带 extra profile）
docker compose build --no-cache node4
docker compose --profile extra up -d node4

# 在leader上执行join请求 （也可以不在leader上执行，因为follower会转发给leader）
curl -X POST http://localhost:8081/api/v2/groups/general/join \
  -H 'Content-Type: application/json' \
  -d '{"ID":"node5","RaftAddr":"node5:12005"}'

# 查看日志
docker compose logs node5 --tail=100

# leader 执行delete
curl -X DELETE http://localhost:8083/api/v2/groups/general/nodes/node4

# 停止并删除容器
docker compose stop node4 && docker compose rm -f node4


# 测试完后清理临时节点
curl -X DELETE http://localhost:8083/api/v2/groups/general/nodes/node4
docker compose rm -sf node4 node5  #停止并移除容器

# 关闭并清除所有数据
docker compose down -v --remove-orphans
