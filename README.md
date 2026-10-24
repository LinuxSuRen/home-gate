# home-gate

<p align="center">
  <img src="web/public/logo.svg" width="120" alt="home-gate logo" />
</p>

[![CI](https://github.com/linuxsuren/home-gate/actions/workflows/ci.yml/badge.svg)](https://github.com/linuxsuren/home-gate/actions/workflows/ci.yml)
[![Release](https://github.com/linuxsuren/home-gate/actions/workflows/release.yml/badge.svg)](https://github.com/linuxsuren/home-gate/actions/workflows/release.yml)
[![License](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)

面向**家庭与小型团队**的自托管统一门户与身份提供方（IdP）：基于 **OAuth 2.0 动态客户端注册（DCR, RFC 7591/7592）**，为自托管服务（家庭影音、下载站、相册、NAS 面板，或小团队内部的工具站点）提供**单点登录（SSO）**与统一入口。

一台 NAS、一台迷你主机甚至树莓派即可运行：Go 单二进制内嵌前端，SQLite 存数据，无需外部依赖。

- **后端**：Golang（标准库 net/http + GORM + 纯 Go SQLite，无 CGO，单二进制）
- **前端**：Vue 3 + Vite + Element Plus，构建产物经 `go:embed` 嵌入二进制
- **协议**：OAuth 2.0 授权码 + PKCE、refresh token（轮换）、OIDC 发现、JWKS（Ed25519）、RP-initiated logout、受保护 DCR
- **身份源**：本地账号（初始 admin 兜底运维）+ 飞书上游联邦登录（可选）
- **角色**：门户统一用户组（admin / operate / view 及自定义），经 `groups` 声明下发，各应用按映射换算本地角色

## 快速开始

```bash
cd web && npm install && npm run build && cd ..   # 构建前端
go build -o home-gate .
./home-gate -addr :8080 -data ./data
# 浏览器打开 http://localhost:8080
# 初始账号: admin / admin123 （首次登录必须修改密码）
```

Docker 部署：

```bash
docker compose up -d --build
```

或直接使用发布的容器镜像：

```bash
docker run -d -p 8080:8080 -v ./data:/data ghcr.io/linuxsuren/home-gate:latest
```

生产部署建议通过反向代理暴露 HTTPS，并显式指定外部地址：

```bash
./home-gate -addr :8080 -issuer https://gate.example.com -data ./data
```

## 接入一个系统（应用视角）

1. 管理员在「管理后台 → 注册令牌」签发一个 initial access token。
2. 用它调用 DCR 注册客户端（secret 仅返回一次）：

```bash
curl -X POST https://gate.example.com/oauth2/register \
  -H "Authorization: Bearer <initial-access-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "client_name": "my-media",
    "redirect_uris": ["https://media.example.com/oauth/callback"],
    "token_endpoint_auth_method": "client_secret_basic",
    "scope": "openid profile email offline_access",
    "portal_url": "https://media.example.com",
    "groups_map": {"admin": "admin", "operate": "operate", "view": "view"}
  }'
```

3. 应用侧配置 OAuth 提供方：
   - Provider：custom，Issuer：`https://gate.example.com`（自动发现端点）
   - Client ID / Secret：DCR 返回值；回调地址同注册的 `redirect_uris`
4. 登录体验：用户在门户登录后，进入应用点「使用门户登录」→ gate 检测到已有会话 → 免登录直接放行（SSO）。

### 角色映射

- 用户组在门户统一维护（admin / operate / view 及自定义组）。
- 登录时随 `groups` 声明下发给应用，应用按注册时配置的 `groups_map` 换算本地角色。

## 端点一览

| 端点 | 说明 |
|---|---|
| `GET /.well-known/openid-configuration` | OIDC 发现 |
| `GET /.well-known/jwks.json` | Ed25519 公钥 |
| `GET/POST /oauth2/authorize` | 授权端点（会话存在即静默放行） |
| `POST /oauth2/token` | 授权码 / 刷新令牌 |
| `GET/POST /oauth2/userinfo` | 用户声明（sub/name/email/groups） |
| `GET /oauth2/logout` | RP-initiated logout |
| `POST /oauth2/register`（RFC 7591） | 受保护 DCR 注册 |
| `GET/PUT/DELETE /oauth2/register/{client_id}`（RFC 7592） | 客户端管理 |

## 文档

- [设计方案](docs/design.md)
- [应用接入指南](docs/onboarding.md)

## Contributing

欢迎通过 Issue 与 Pull Request 贡献。提交 PR 前请先阅读 [贡献指南](https://github.com/linuxsuren/open-source-best-practice)。

## License

[Apache-2.0](LICENSE)
