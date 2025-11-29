# 清空本地数据,整理和更新依赖关系
docker compose down
rm -rf data/*
go mod tidy

# 构建并启动
docker compose build --no-cache && docker compose up -d

# 发消息 e.g.
curl -X POST http://localhost:8081/api/groups/general/message \
  -H 'Content-Type: application/json' \
  -d '{"conv_id":"general","sender":"node1","payload":"test node 1!"}'

# 测试用例
for i in $(seq 1 6); do
  node=$(( (i % 3) + 1 ))                     
  port=$((8080 + node))                       
  payload="msg-$i-from-node$node"
  sender="u$node"
  echo "POST -> $port : $payload"
  curl -s -X POST "http://localhost:${port}/api/groups/general/message" \
    -H 'Content-Type: application/json' \
    -d '{"conv_id":"general","sender":"'"$sender"'","payload":"'"$payload"'","vector_clock":{"'"$sender"'":'"$i"'}}'
  echo
done

# 查看状态/订阅流

curl http://localhost:8083/api/groups/general/status | jq
curl http://localhost:8084/api/groups/general/status | jq

curl http://localhost:8082/api/groups/general/stream


# 启动临时节点
docker compose build --no-cache node5
docker compose --profile extra up -d node5

# 创建本地实例
curl -X POST http://localhost:8082/api/groups -H 'Content-Type: application/json' \
  -d '{"group_id":"room-1","raft_addr":"node2:12102","bootstrap":false}'

# 加入节点
curl -X POST http://localhost:8081/api/groups/room2/join \
  -H 'Content-Type: application/json' \
  -d '{"ID":"node4","RaftAddr":"node4:12104"}'

# 查看日志
docker compose logs node5 --tail=100

# 查看所在组
curl -s http://localhost:8082/api/groups | jq .

# 节点leave
curl -X POST http://localhost:8084/api/groups/general/leave

# leader 执行delete
curl -X DELETE http://localhost:8081/api/groups/room-1/nodes/node4


# 测试完后 停止并删除容器
docker compose stop node4 node5 && docker compose rm -f node4 node5

# 关闭并清除所有数据
docker compose down -v --remove-orphans

# ---------------------------------------------------------------------
# -------------------------multiraft test plan-------------------------

# 创建（node1 bootstrap；node2/node3 只是打开实例）
curl -X POST http://localhost:8081/api/groups -H 'Content-Type: application/json' \
  -d '{"group_id":"room-1","raft_addr":"node1:12101","bootstrap":true}'

curl -X POST http://localhost:8082/api/groups -H 'Content-Type: application/json' \
  -d '{"group_id":"room2","raft_addr":"node2:12102","bootstrap":false}'

curl -X POST http://localhost:8083/api/groups -H 'Content-Type: application/json' \
  -d '{"group_id":"room-1","raft_addr":"node3:12103","bootstrap":false}'
  


# 加入（先创建实例，然后才能加入）
curl -X POST http://localhost:8081/api/groups/general/join -H 'Content-Type: application/json' \
  -d '{"ID":"node4","RaftAddr":"node4:12004"}'
  
curl -X POST http://localhost:8082/api/groups/room-1/join -H 'Content-Type: application/json' \
  -d '{"ID":"node4","RaftAddr":"node4:12104"}'

# 在node4上创建room2的本地raft实例
curl -X POST http://localhost:8084/api/groups \
  -H "Content-Type: application/json" \
  -d '{
    "group_id": "room2",
    "raft_addr": "node4:12104",
    "bootstrap": false
  }'
# node4 加入room2
curl -X POST http://localhost:8081/api/groups/room2/join \
  -H "Content-Type: application/json" \
  -d '{
    "ID": "node4",
    "RaftAddr": "node4:12104"
  }'
