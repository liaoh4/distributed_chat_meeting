# syntax=docker/dockerfile:1

########## 构建阶段 ##########
FROM golang:1.22 AS builder
WORKDIR /app

# 先缓存依赖
COPY go.mod go.sum ./
# 如需国内代理，换成 goproxy.cn
RUN go env -w GOPROXY=https://proxy.golang.org,direct && go mod download

# 拷贝源码并编译（确保你的主程序在 ./cmd/server）
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/server

########## 运行阶段 ##########
FROM alpine:3.20

# su-exec 用于降权，curl 用于 HEALTHCHECK（可选）
RUN apk add --no-cache ca-certificates su-exec curl

# 非 root 账户
RUN adduser -D -u 10001 appuser

WORKDIR /app
COPY --from=builder /out/app /app/app

# 入口脚本：启动时修正 /data 权限，然后降权运行
COPY docker/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# 暴露端口仅作文档提示；compose 会映射具体端口
EXPOSE 8081 8082 8083 12001 12002 12003

# 建议加健康检查（HTTP /status 返回 200 即视为健康）
HEALTHCHECK --interval=10s --timeout=3s --retries=6 CMD \
  curl -fsS http://127.0.0.1:8081/status >/dev/null || \
  curl -fsS http://127.0.0.1:8082/status >/dev/null || \
  curl -fsS http://127.0.0.1:8083/status >/dev/null || exit 1

# 重要：不要在这里切换 USER，入口脚本需要 root 来 chown 挂载卷
# USER appuser   # ← 不要这样；在入口脚本里用 su-exec 降权

VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
