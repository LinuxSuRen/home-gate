# ---------- 前端构建 ----------
# 网络受限环境可用 --build-arg 指定镜像加速源，如：
#   docker compose build \
#     --build-arg NODE_IMAGE=docker.m.daocloud.io/library/node:20-bookworm \
#     --build-arg GO_IMAGE=docker.m.daocloud.io/library/golang:1.24-bookworm
ARG NODE_IMAGE=node:20-bookworm
ARG GO_IMAGE=golang:1.24-bookworm
ARG RUN_IMAGE=alpine:3.20
FROM ${NODE_IMAGE} AS web
WORKDIR /src/web
# 可用 --build-arg NPM_REGISTRY=https://registry.npmmirror.com 加速
ARG NPM_REGISTRY=https://registry.npmjs.org
COPY web/package.json web/package-lock.json ./
RUN npm config set registry $NPM_REGISTRY && npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- 后端构建（嵌入前端产物） ----------
FROM ${GO_IMAGE} AS build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV CGO_ENABLED=0 GOOS=linux
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN go build -trimpath -ldflags="-s -w" -o /out/home-gate .

# ---------- 运行镜像 ----------
FROM ${RUN_IMAGE}
RUN adduser -D -u 10001 app && apk add --no-cache ca-certificates tzdata \
    && mkdir -p /data && chown app:app /data
WORKDIR /app
COPY --from=build /out/home-gate /usr/local/bin/home-gate
USER app
# 数据目录（SQLite 库 + Ed25519 签名密钥）挂载卷持久化
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["home-gate", "-addr", ":8080", "-data", "/data"]
