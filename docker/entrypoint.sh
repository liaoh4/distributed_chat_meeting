#!/bin/sh
set -e

# 确保 /data 存在并可写
mkdir -p /data

# 修正卷权限（卷是运行时挂载的，必须此时 chown）
# 如果失败（例如只读），也不要中断
chown -R appuser:appuser /data 2>/dev/null || true
chmod 0777 /data 2>/dev/null || true

# 打印环境便于排错（可选）
echo "Starting app with: NODE_ID=${NODE_ID} HTTP_ADDR=${HTTP_ADDR} RAFT_ADDR=${RAFT_ADDR} BOOTSTRAP=${BOOTSTRAP} JOIN_ADDR=${JOIN_ADDR} GROUP_ID=${GROUP_ID}"

# 以非 root 身份运行应用
exec su-exec appuser:appuser /app/app
