# 第三方系统 OIDC 接入指南

面向第三方系统的开发者/管理员：把你的系统接入 home-gate 统一门户，实现单点登录（SSO）与角色同步。

## 接入前提

- 系统支持配置自定义 OIDC / OAuth2 提供方（标准授权码流程，推荐带 PKCE）。
- 用户浏览器能同时访问门户与该系统（同一内网或公网可达）。

## 第一步：申请注册令牌

向门户管理员申请一个 initial access token（注册令牌）：

1. 说明系统名称、回调地址、需要的用户组。
2. 管理员在「管理后台 → 注册令牌」签发，有效期默认 7 天，仅可使用一次流程。
3. 令牌泄露或过期请联系管理员重新签发。

## 第二步：DCR 注册客户端

```bash
curl -X POST https://gate.example.com/oauth2/register \
  -H "Authorization: Bearer <initial-access-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "client_name": "your-system",
    "redirect_uris": ["https://your-system.example.com/oauth/callback"],
    "token_endpoint_auth_method": "client_secret_basic",
    "scope": "openid profile email offline_access",
    "portal_url": "https://your-system.example.com",
    "description": "系统用途一句话说明",
    "logo_url": "https://your-system.example.com/logo.png"
  }'
```

- `token_endpoint_auth_method` 建议 `client_secret_basic`（服务端可保密 secret 时）；纯前端/无法保密时用 `none` 并强制 PKCE。
- `portal_url` / `description` / `logo_url` 用于门户应用中心卡片展示。
- 响应中的 `client_secret` 仅返回一次，请妥善保存。

## 第三步：系统侧配置

- **Issuer**：填门户对外地址，如 `https://gate.example.com`，系统会自动发现 `/.well-known/openid-configuration` 与 JWKS。
- **Client ID / Client Secret**：DCR 返回值。
- **回调地址（redirect URI）**：必须与注册的 `redirect_uris` 完全一致。
- **groups 声明**：登录后 ID Token / userinfo 中含 `groups` 数组，取值为门户用户组（`admin` / `operate` / `view` 及自定义组），系统侧按注册时的 `groups_map` 换算本地角色。

## 接入验证清单

- [ ] 系统登录页出现「使用门户登录」入口。
- [ ] 门户已有会话时，点击后免密直达系统（SSO 静默放行）。
- [ ] 门户「应用中心」出现该系统卡片（配置了 `portal_url`）。
- [ ] 登录后系统内角色与门户用户组映射一致。

## 常见问题

**回调地址不匹配（invalid_redirect_uri）**
回调地址做精确匹配白名单校验，须与注册的 `redirect_uris` 逐字符一致（含协议、端口、路径；不支持通配符）。地址变更需通过 RFC 7592 管理接口更新注册信息。

**登录流程出现 HTTP 302**
授权端点校验通过后以 302 重定向回传授权码，这是协议正常行为，不是错误。

**不想做认证聚合**
若系统无需接入 SSO，只希望出现在门户卡片中，可请管理员在「管理后台 → 链接管理」直接添加纯链接入口（仅跳转，不传递身份）。
