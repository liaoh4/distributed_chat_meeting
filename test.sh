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
# Case 1) Testing Message Ordering 
# =========================================================
echo "============================================="
echo "Case 1) Testing Message Ordering"
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
# Case 2) Create a New Group for Horizontal Scalability 
# =========================================================
echo "============================================="
echo "Case 2) Create a New Group for Horizontal Scalability"
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
# Case 3) Adding New Members
# =========================================================


echo "============================================="
echo "Case 3) Adding New Members"
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
# Case 4) Sending Messages  Within the SCU Group
# =========================================================
echo "============================================="
echo "Case 4) Sending Messages  Within the SCU Group"
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


echo "stream replay SCU"
echo "============================================="

for P in 8081 8082 8083 8084; do
  echo "---- node on :$P ----"
  curl --max-time 2 -s "http://localhost:$P/api/groups/$GROUP1/stream?from=1"|| true
  echo
done



# =========================================================
# Case 5) Leader Failure and Automatic Election
# =========================================================
echo "============================================="
echo "Case 5) Leader Failure and Automatic Election"
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
# Case 6) Member Departure and Group Reconfiguration
# =========================================================
echo "============================================="
echo "Case 6) Member Departure and Group Reconfiguration"
echo "============================================="

curl -s -X POST "$NODE4/api/groups/$GROUP1/leave"
echo
echo "node4 left scu"
echo


# =========================================================
# Case 7) Late-Join Replication: Node5 Joining SCU
# =========================================================
echo "============================================="
echo "Case 7) Late-Join Replication: Node5 Joining SCU"
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
# Case 8) Multi-Group Scalability Test: Creating CSEN317
# =========================================================
echo "============================================="
echo "Case 8) Multi-Group Scalability Test: Creating CSEN317"
echo "============================================="
docker compose restart node1
sleep 3



echo 
echo "1) node1 create csen317"
echo

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


echo 
echo "2) node4 / node5 create local instances"
echo 

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


echo 
echo "3) node4 / node5 join csen317"
echo

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


echo 
echo "4) Clients send messages to csen317"
echo 

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


echo 
echo "5) Message replay csen317"
echo 

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

