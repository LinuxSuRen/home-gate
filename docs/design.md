# home-gate 设计方案

面向家庭与小型团队的自托管统一门户与身份提供方（IdP）：基于 OAuth 2.0 动态客户端注册（DCR, RFC 7591/7592）的门户系统，为自托管应用提供单点登录（SSO）。

## 1. 架构

```
                    ┌────────────────────────────────────┐
                    │        home-gate（门户）          │
  浏览器 ──────────▶│  门户 UI（应用入口、用户中心、管理台）  │
                    │  OAuth2 授权服务器 + OIDC Provider   │
                    │  DCR 注册端点（RFC 7591/7592，受保护）│
                    │  用户库（本地账号 + 飞书联邦）/ 审计   │
                    └──────┬──────────────┬──────────────┘
                     授权码+PKCE      JWKS 验签（EdDSA JWT）
                    ┌──────▼──────┐  ┌────▼──────────┐
                    │  家庭影音   │  │   下载站       │
                    │ (OIDC RP)   │  │ (新增 OIDC 登录)│
                    └─────────────┘  └───────────────┘
```

- 门户是唯一登录点：登录 gate 后访问任一子系统，子系统重定向到 `/oauth2/authorize`，gate 检测到已有会话即免登录发授权码（SSO）。
- 登出：RP-initiated logout，销毁 gate 会话并回跳 `post_logout_redirect_uri`。
- 身份源：本地账号（admin 兜底运维）+ 飞书 OAuth 上游。

## 2. 关键决策（已与研发确认）

| 决策点 | 结论 |
|---|---|
| 身份源 | 本地账号 + 飞书上游联邦 |
| 子系统账号 | SSO 首次登录自动开户，赋默认角色，管理员事后调整 |
| 角色映射 | gate 统一用户组，经 id_token/userinfo 的 `groups` 声明下发；子系统按 client 级映射表换算本地角色 |
| HTTP 框架 | 标准库 `net/http`（零额外部依赖） |
| DCR 模式 | RFC 7591 受保护注册：需 initial access token（管理员在门户签发/吊销） |

## 3. 协议端点

| 端点 | 说明 |
|---|---|
| `GET /.well-known/openid-configuration` | OIDC 发现：授权码 + PKCE + refresh_token |
| `GET /.well-known/jwks.json` | Ed25519 公钥（OKP/Ed25519，kid 区分，支持轮换） |
| `GET/POST /oauth2/authorize` | 授权端点；已有会话静默放行；支持 `prompt=login/none`；内部第一方系统免同意页 |
| `POST /oauth2/token` | grant_type = authorization_code / refresh_token |
| `GET/POST /oauth2/userinfo` | Bearer access token → sub/name/email/groups |
| `GET /oauth2/logout` | RP-initiated logout |
| `POST /oauth2/register` | RFC 7591 受保护 DCR |
| `GET/PUT/DELETE /oauth2/register/{client_id}` | RFC 7592 客户端管理（initial token 或 client 凭据） |

## 4. 数据模型（GORM + 纯 Go SQLite）

- **User**：id, username, password_hash(空=仅联邦), name, email, feishu_union_id(唯一), must_change_password, active
- **Group**：id, name(唯一)；**UserGroup** 关联。内置组：admin / operate / view（与应用角色对齐）
- **Session**：id(随机 token, cookie `gate_session`), user_id, expires_at, created_ip；滑动续期
- **Client**：client_id, client_secret_hash, name, redirect_uris(JSON), scopes, token_endpoint_auth_method(confidential/public), portal_url, groups_map(JSON: gate组→子系统角色), active, reg_token(用于 RFC 7592 管理凭据)
- **InitialToken**：token_hash, remark, expires_at, revoked
- **SigningKey**：kid, ed25519 私钥（创建即持久化，多 key 共存实现轮换）
- **AuthCode**：code, client_id, user_id, redirect_uri, scope, nonce, code_challenge, method, expires_at（一次性）
- **AuditLog**：time, actor, action(login/logout/authorize/token/dcr/…), detail, ip
- **AppLink**：name, description, logo_url, url, sort, public, active（纯链接入口；public 控制免登录是否可见，缺省隐私）

## 5. 令牌

- **access_token**：JWT（EdDSA），claims：iss, sub, aud=client_id, exp(1h), iat, scope, groups。子系统用 JWKS 本地验签，无需回调 gate。
- **id_token**：JWT（EdDSA），claims：iss, sub, aud, exp, iat, nonce, name, email, preferred_username, groups。
- **refresh_token**：随机 opaque token，DB 存储（client_id+user_id 绑定），有效期 30 天，可吊销；轮换：使用即换新。

## 6. 门户 API（session 鉴权）

- `POST /api/auth/login`、`POST /api/auth/logout`、`GET /api/auth/me`、`POST /api/auth/change-password`
- `GET /api/auth/feishu/start`、`GET /api/auth/feishu/callback`（state 防 CSRF，自动 provision：按 union_id 命中或建新账号）
- `GET /api/apps`：应用入口卡片。**免登录可访问**：未登录仅返回公开（`public`）链接；登录后返回全部链接与 OAuth 应用卡片（来自 Client.portal_url）
- 管理端（admin 组）：`/api/admin/users`、`/api/admin/groups`、`/api/admin/clients`（吊销/重置 secret）、`/api/admin/initial-tokens`、`/api/admin/audit`、`/api/admin/settings`（飞书 app 凭据）

## 7. 应用接入改造

接入方（以家庭影音、下载站为例）：

| 应用 | 改造 |
|---|---|
| 家庭影音 | 新增 custom OIDC provider（按发现端点动态取端点），登录页加"使用门户登录"，角色按 groups 映射 admin/operate/view |
| 下载站 | 新增 OIDC 登录（授权码+PKCE），按 sub/email 关联本地账号，未命中自动开户，groups 映射本地角色 |

应用可保留本地账号密码登录（应急兜底）。

## 8. 部署形态

Go 单二进制 + go:embed 前端 + SQLite + docker-compose；可选自签 HTTPS 双协议开关。默认端口 `:8080`。

## 9. 技术栈

- 后端：Go 1.24+，标准库 net/http，GORM + glebarez/sqlite，golang-jwt/jwt/v5（EdDSA），golang.org/x/crypto/bcrypt。协议层自研，不引 OAuth 框架。
- 前端：Vue 3 + Vite + Element Plus，fetch 封装 API 层。

## 10. 验证

- `go build ./... && go test ./...`：授权码+PKCE 全流程、token 换发/刷新/验签、DCR 注册/管理、SSO 会话复用、审计写入。
- `vite build`；联调验证交由部署环境。
