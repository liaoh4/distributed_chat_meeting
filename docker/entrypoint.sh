#!/bin/sh
set -e

# make sure /data exists and writeable
mkdir -p /data

# Correct Volume Permissions
# do not abort if the operation is unsuccessful
chown -R appuser:appuser /data 2>/dev/null || true
chmod 0777 /data 2>/dev/null || true

# print the environment for debugging
echo "Starting app with: NODE_ID=${NODE_ID} HTTP_ADDR=${HTTP_ADDR} RAFT_ADDR=${RAFT_ADDR} BOOTSTRAP=${BOOTSTRAP} JOIN_ADDR=${JOIN_ADDR} GROUP_ID=${GROUP_ID}"

# run using non-root identity
exec su-exec appuser:appuser /app/app
