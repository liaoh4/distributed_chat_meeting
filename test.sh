#!/usr/bin/env bash
set -euo pipefail

NODE1=http://localhost:8081
NODE2=http://localhost:8082
NODE3=http://localhost:8083
NODE4=http://localhost:8084
NODE5=http://localhost:8085

GROUP="general"
GROUP2="room2"

ts() { date +%s%3N; }

echo "============================================="
echo "1) 创建默认组 general node1 bootstrap"
echo "============================================="

curl -s -X POST "$NODE1/api/groups" \
  -H "Content-Type: application/json" \
  -d "{
        \"group_id\":\"$GROUP\",
        \"raft_addr\":\"node1:12001\",
        \"bootstrap\":true
      }" || true
echo; echo "✔ general 创建(或已存在)"
echo

echo "============================================="
echo "2) node2 加入 general"
echo "============================================="

curl -s -X POST "$NODE2/api/groups/$GROUP/join" \
  -H "Content-Type: application/json" \
  -d "{
        \"ID\":\"node2\",
        \"RaftAddr\":\"node2:12002\"
      }" || true
echo; echo "✔ node2 joined"
echo

echo "============================================="
echo "3) node3 加入 general"
echo "============================================="

curl -s -X POST "$NODE3/api/groups/$GROUP/join" \
  -H "Content-Type: application/json" \
  -d "{
        \"ID\":\"node3\",
        \"RaftAddr\":\"node3:12003\"
      }" || true
echo; echo "✔ node3 joined"
echo


echo "============================================="
echo "4) 发送消息到 general"
echo "============================================="

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

echo; echo "✔ general message sent"
echo


echo "============================================="
echo "5) stream replay (general)"
echo "============================================="

for P in 8081 8082 8083; do
  echo "---- node on :$P ----"
  curl --max-time 2 -s "http://localhost:$P/api/groups/$GROUP/stream?from=1" || true
  echo
done


echo "============================================="
echo "6) 启动 node4 / node5"
echo "============================================="
docker compose --profile extra up -d node4 node5
sleep 3
echo "✔ node4 node5 started"
echo


echo "============================================="
echo "7) node1 创建 room2 (bootstrap=true)"
echo "============================================="

curl -s -X POST "$NODE1/api/groups" \
  -H "Content-Type: application/json" \
  -d "{
        \"group_id\":\"$GROUP2\",
        \"raft_addr\":\"node1:12101\",
        \"bootstrap\":true
      }"
echo; echo "✔ room2 created"
echo


echo "============================================="
echo "8) node4 / node5 创建本地 room2"
echo "============================================="

curl -s -X POST "$NODE4/api/groups" \
  -H "Content-Type: application/json" \
  -d "{
        \"group_id\":\"$GROUP2\",
        \"raft_addr\":\"node4:12104\",
        \"bootstrap\":false
      }"

curl -s -X POST "$NODE5/api/groups" \
  -H "Content-Type: application/json" \
  -d "{
        \"group_id\":\"$GROUP2\",
        \"raft_addr\":\"node5:12105\",
        \"bootstrap\":false
      }"
echo; echo "✔ room2 local instances created"
echo

sleep 2

echo "============================================="
echo "9) node4 / node5 加入 room2"
echo "============================================="

curl -s -X POST "$NODE1/api/groups/$GROUP2/join" \
  -H "Content-Type: application/json" \
  -d "{
        \"ID\":\"node4\",
        \"RaftAddr\":\"node4:12104\"
      }"

curl -s -X POST "$NODE1/api/groups/$GROUP2/join" \
  -H "Content-Type: application/json" \
  -d "{
        \"ID\":\"node5\",
        \"RaftAddr\":\"node5:12105\"
      }"

echo; echo "✔ room2 joined"
echo

sleep 3

echo "============================================="
echo "10) 查看 room2 status"
echo "============================================="

for N in 8081 8084 8085; do
  echo "--- node on :$N ---"
  curl -s "http://localhost:$N/api/groups/$GROUP2/status" | jq
  echo
done


echo "============================================="
echo "11) 发送消息到 room2"
echo "============================================="


curl -s -X POST "$NODE1/api/groups/$GROUP2/message" \
  -H "Content-Type: application/json" \
  -d "{
        \"conv_id\":\"$GROUP2\",
        \"sender\":\"node1\",
        \"payload\":\"hello room2!\"
      }"
echo; echo "✔ room2 message sent"
echo


echo "============================================="
echo "12) 查看 room2 stream"
echo "============================================="

for P in 8081 8084 8085; do
  echo "---- node on :$P ----"
  curl --max-time 2 -s "http://localhost:$P/api/groups/$GROUP2/stream?from=1" || true
  echo
done
