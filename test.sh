#!/usr/bin/env bash
set -euo pipefail

# -----------------------
#  Node HTTP Addresses
# -----------------------
NODE1=http://localhost:8081
NODE2=http://localhost:8082
NODE3=http://localhost:8083
NODE4=http://localhost:8084
NODE5=http://localhost:8085

GROUP0="general"
GROUP1="scu"
GROUP2="csen317"

docker compose build --no-cache && docker compose up -d

# -----------------------
#  Start Extra Nodes
# -----------------------
docker compose --profile extra up -d node4 node5
sleep 3


# =========================================================
# 0) Message Ordering Test (general)
# =========================================================
echo "============================================="
echo "0) message ordering (general)"
echo "============================================="

for i in $(seq 1 6); do
  node=$(( (i % 3) + 1 ))
  port=$((8080 + node))
  sender="u$node"
  payload="msg-$i-from-node$node-to-general"

  curl -s -X POST "http://localhost:${port}/api/groups/$GROUP0/message" \
    -H "Content-Type: application/json" \
    -d "{
          \"conv_id\":\"general\",
          \"sender\":\"$sender\",
          \"payload\":\"$payload\"
        }"
done

for P in 8081 8082 8083; do
  echo "---- node on :$P ----"
  curl --max-time 2 -s "http://localhost:$P/api/groups/$GROUP0/stream?from=1" || true
  echo
done



# =========================================================
# 1) Create Group SCU (horizontal scalability test)
# =========================================================
echo "============================================="
echo "1) create group scu"
echo "============================================="

curl -s -X POST "$NODE1/api/groups" \
  -H "Content-Type: application/json" \
  -d "{
        \"group_id\":\"$GROUP1\",
        \"raft_addr\":\"node1:12201\",
        \"bootstrap\":true
      }" || true

echo; echo "✔ scu created"
sleep 3
curl -s "$NODE1/api/groups/$GROUP1/status" | jq
echo

# =========================================================
# 2) Node2 joins SCU
# =========================================================
echo "============================================="
echo "2) node2 joins scu"
echo "============================================="

# local creation
curl -s -X POST "$NODE2/api/groups" \
  -H "Content-Type: application/json" \
  -d "{
        \"group_id\":\"$GROUP1\",
        \"raft_addr\":\"node2:12202\",
        \"bootstrap\":false
      }"

sleep 2
# join to leader
curl -s -X POST "$NODE1/api/groups/$GROUP1/join" \
  -H "Content-Type: application/json" \
  -d "{
        \"ID\":\"node2\",
        \"RaftAddr\":\"node2:12202\"
      }"

echo "✔ node2 joined scu"
echo



# =========================================================
# 3) Node3 joins SCU
# =========================================================
echo "============================================="
echo "3) node3 joins scu"
echo "============================================="

curl -s -X POST "$NODE3/api/groups" \
  -H "Content-Type: application/json" \
  -d "{
        \"group_id\":\"$GROUP1\",
        \"raft_addr\":\"node3:12203\",
        \"bootstrap\":false
      }"
sleep 2

curl -s -X POST "$NODE1/api/groups/$GROUP1/join" \
  -H "Content-Type: application/json" \
  -d "{
        \"ID\":\"node3\",
        \"RaftAddr\":\"node3:12203\"
      }"

echo "✔ node3 joined scu"
echo



# =========================================================
# 4) Node4 joins SCU
# =========================================================
echo "============================================="
echo "4) node4 joins scu"
echo "============================================="

curl -s -X POST "$NODE4/api/groups" \
  -H "Content-Type: application/json" \
  -d "{
        \"group_id\":\"$GROUP1\",
        \"raft_addr\":\"node4:12204\",
        \"bootstrap\":false
      }"
sleep 2
curl -s -X POST "$NODE1/api/groups/$GROUP1/join" \
  -H "Content-Type: application/json" \
  -d "{
        \"ID\":\"node4\",
        \"RaftAddr\":\"node4:12204\"
      }"

echo "✔ node4 joined scu"
echo


# =========================================================
# 5) Send messages to SCU
# =========================================================
echo "============================================="
echo "5) send message to scu"
echo "============================================="

for i in $(seq 1 4); do
  node=$(( (i % 4) + 1 ))
  port=$((8080 + node))
  sender="u$node"
  payload="msg-$i-from-node$node-to-scu"

  curl -s -X POST "http://localhost:${port}/api/groups/$GROUP1/message" \
    -H "Content-Type: application/json" \
    -d "{
          \"conv_id\":\"scu\",
          \"sender\":\"$sender\",
          \"payload\":\"$payload\"
        }"
done

echo "✔ message sent"
echo



# =========================================================
# 6) SCU Stream Replay
# =========================================================
echo "============================================="
echo "6) stream replay SCU"
echo "============================================="

for P in 8081 8082 8083 8084; do
  echo "---- node on :$P ----"
  curl --max-time 2 -s "http://localhost:$P/api/groups/$GROUP1/stream?from=1"|| true
  echo
done



# =========================================================
# 7) Leader Election
# =========================================================
echo "============================================="
echo "7) leader election"
echo "============================================="

echo "node1 (old leader)"
curl -s "$NODE1/api/groups/$GROUP1/status" | jq

echo "docker stop node1"
docker stop distributed_chat_meeting-node1-1
sleep 1
echo "node2 becomes new leader"
curl -s "$NODE2/api/groups/$GROUP1/status" | jq
echo



# =========================================================
# 8) Leave SCU
# =========================================================
echo "============================================="
echo "8) leave scu"
echo "============================================="

curl -s -X POST "$NODE4/api/groups/$GROUP1/leave"
echo
echo "node4 left scu"
echo


# =========================================================
# 9) Message Replication Test (node5)
# =========================================================
echo "============================================="
echo "9) message replication"
echo "============================================="

curl -s -X POST "$NODE5/api/groups" \
  -H "Content-Type: application/json" \
  -d "{
        \"group_id\":\"$GROUP1\",
        \"raft_addr\":\"node5:12205\",
        \"bootstrap\":false
      }"

curl -s -X POST "$NODE2/api/groups/$GROUP1/join" \
  -H "Content-Type: application/json" \
  -d "{
        \"ID\":\"node5\",
        \"RaftAddr\":\"node5:12205\"
      }"

echo "✔ node5 joined scu"
echo
sleep 2

for P in 8082 8085; do
  echo "---- node on :$P ----"
  curl --max-time 2 -s "http://localhost:$P/api/groups/$GROUP1/stream?from=1" ||true
  echo
done



# =========================================================
# 10) Horizontal scalability
# =========================================================
echo "============================================="
echo "10) horizontal scalability"
echo "============================================="
echo "Groups node3 joining"
curl -s "$NODE3/api/groups" | jq
echo

# =========================================================
# SUCCESSFUL SCENARIO – CREATE CSEN317
# =========================================================
echo "*********************************************"
echo "Below is an Example for Successful Scenario"
echo "*********************************************"
docker compose restart node1
sleep 3



echo "============================================="
echo "1) node1 create csen317"
echo "============================================="

curl -s -X POST "$NODE1/api/groups" \
  -H "Content-Type: application/json" \
  -d "{
        \"group_id\":\"$GROUP2\",
        \"raft_addr\":\"node1:12101\",
        \"bootstrap\":true
      }"
sleep 2

echo; echo "✔ csen317 created"
echo


echo "============================================="
echo "2) node4 / node5 create local instances"
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

echo "✔ csen317 local instances created"
echo

sleep 2


echo "============================================="
echo "3) node4 / node5 join csen317"
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

echo "✔ csen317 joined"
echo

sleep 3


echo "============================================="
echo "4) Clients send messages to csen317"
echo "============================================="

curl -s -X POST "$NODE1/api/groups/$GROUP2/message" \
  -H "Content-Type: application/json" \
  -d "{
        \"conv_id\":\"$GROUP2\",
        \"sender\":\"node1\",
        \"payload\":\"hello from node1\"
      }"

curl -s -X POST "$NODE4/api/groups/$GROUP2/message" \
  -H "Content-Type: application/json" \
  -d "{
        \"conv_id\":\"$GROUP2\",
        \"sender\":\"node4\",
        \"payload\":\"hello from node4\"
      }"

curl -s -X POST "$NODE5/api/groups/$GROUP2/message" \
  -H "Content-Type: application/json" \
  -d "{
        \"conv_id\":\"$GROUP2\",
        \"sender\":\"node5\",
        \"payload\":\"hello from node5\"
      }"

echo "✔ csen317 message sent"
echo


echo "============================================="
echo "5) Message replay csen317"
echo "============================================="

for P in 8081 8084 8085; do
  echo "---- node on :$P ----"
  curl --max-time 2 -s "http://localhost:$P/api/groups/$GROUP2/stream?from=1" || true
  echo
done

echo "============================================="
echo "Test Finished"
echo "============================================="

docker compose stop node4 node5
docker compose rm -f node4 node5
docker compose down -v --remove-orphans

