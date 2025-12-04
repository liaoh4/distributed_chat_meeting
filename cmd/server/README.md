# Build and run (run docker first)
docker compose build --no-cache && docker compose up -d

# view log (example)
docker compose logs node5 --tail=100

# view groups (example)
curl -s http://localhost:8081/api/groups | jq .

# view status (example)
curl -s "http://localhost:8081/api/groups/csen317/status" | jq

# node leave (example)
curl -X POST http://localhost:8084/api/groups/scu/leave

# leader perform delete (example)
curl -X DELETE http://localhost:8081/api/groups/room-1/nodes/node4


# view containers
 docker ps --format "table {{.Names}}\t{{.ID}}"

# stop container (example)
docker stop distributed_chat_meeting-node1-1

# remove data and update dependencies
docker compose down
rm -rf data/*
go mod tidy

# test 
./test.sh
