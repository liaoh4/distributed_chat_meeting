# syntax=docker/dockerfile:1

### ===== 构建阶段 =====
FROM golang:1.22 AS builder
WORKDIR /app

# 先拷贝依赖文件，加速缓存
COPY go.mod go.sum ./
# 可选换代理：如需加速把下一行改为 goproxy.cn
RUN go env -w GOPROXY=https://proxy.golang.org,direct && go mod download

# 再拷贝源码并编译（注意这里的路径！）
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/app ./cmd/server

### ===== 运行阶段 =====
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=builder /out/app /app/app
VOLUME ["/data"]
EXPOSE 8081 8082 8083 12001 12002 12003
USER appuser
ENTRYPOINT ["/app/app"]
