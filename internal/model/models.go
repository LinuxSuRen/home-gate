package model

import (
	"time"
)

// User 门户用户：本地账号（password_hash 非空）或飞书联邦账号（feishu_union_id 非空）
type User struct {
	ID                uint      `gorm:"primarykey" json:"id"`
	Username          string    `gorm:"uniqueIndex;size:64" json:"username"`
	PasswordHash      string    `json:"-"` // 空 = 仅联邦登录
	Name              string    `gorm:"size:128" json:"name"`
	Email             string    `gorm:"size:128" json:"email"`
	FeishuUnionID     string    `gorm:"index;size:64" json:"-"` // 空 = 非联邦；唯一性由联邦登录查重保证
	MustChangePasswd  bool      `json:"must_change_password"`
	Active            bool      `json:"active"`
	Groups            []Group   `gorm:"many2many:user_groups;" json:"groups"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (u *User) GroupNames() []string {
	out := make([]string, 0, len(u.Groups))
	for _, g := range u.Groups {
		out = append(out, g.Name)
	}
	return out
}

func (u *User) HasGroup(name string) bool {
	for _, g := range u.Groups {
		if g.Name == name {
			return true
		}
	}
	return false
}

// Group 用户组：角色经 groups 声明下发，子系统按 client 级映射表换算本地角色。
// 内置组：admin / operate / view
type Group struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Name      string    `gorm:"uniqueIndex;size:64" json:"name"`
	Remark    string    `gorm:"size:256" json:"remark"`
	CreatedAt time.Time `json:"created_at"`
}

// Session 门户会话（cookie gate_session → ID）
type Session struct {
	ID        string    `gorm:"primarykey;size:64" json:"-"`
	UserID    uint      `json:"user_id"`
	ExpiresAt time.Time `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	CreatedIP string    `json:"created_ip"`
}

// Client OAuth2 客户端（经 DCR 注册）
type Client struct {
	ID                      uint      `gorm:"primarykey" json:"id"`
	ClientID                string    `gorm:"uniqueIndex;size:64" json:"client_id"`
	ClientSecretHash        string    `json:"-"` // bcrypt
	Name                    string    `gorm:"size:128" json:"name"`
	RedirectURIs            string    `gorm:"type:text" json:"-"` // JSON 数组
	Scopes                  string    `gorm:"size:512" json:"scopes"`
	TokenEndpointAuthMethod string    `gorm:"size:32" json:"token_endpoint_auth_method"` // client_secret_basic / client_secret_post / none
	PortalURL               string    `gorm:"size:512" json:"portal_url"`                // 门户入口页跳转地址（DCR 扩展字段）
	Description             string    `gorm:"size:256" json:"description"`               // 应用描述（门户卡片展示）
	LogoURL                 string    `gorm:"size:512" json:"logo_url"`                  // 应用 logo 地址（门户卡片展示）
	GroupsMap               string    `gorm:"type:text" json:"-"`                        // JSON: gate 组名 -> 子系统角色
	RegTokenHash            string    `json:"-"` // RFC 7592 registration access token
	Active                  bool      `json:"active"`
	IssuedAt                time.Time `json:"issued_at"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

// AppLink 纯链接应用入口（不走认证聚合，仅门户卡片跳转）
type AppLink struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	Name        string    `gorm:"size:128" json:"name"`
	Description string    `gorm:"size:256" json:"description"`
	LogoURL     string    `gorm:"size:512" json:"logo_url"`
	URL         string    `gorm:"size:512" json:"url"` // 跳转地址
	Sort        int       `json:"sort"`                // 越小越靠前
	Public      bool      `gorm:"default:false" json:"public"` // 公开链接免登录可见；隐私链接需登录
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Favorite 用户关注的门户入口：kind 为 link（ref=链接 id）或 app（ref=client_id）
type Favorite struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"index;uniqueIndex:uk_fav" json:"-"`
	Kind      string    `gorm:"size:16;uniqueIndex:uk_fav" json:"kind"`
	Ref       string    `gorm:"size:64;uniqueIndex:uk_fav" json:"ref"`
	CreatedAt time.Time `json:"created_at"`
}

// InitialToken DCR 初始访问令牌（管理员签发/吊销）
type InitialToken struct {
	ID        uint       `gorm:"primarykey" json:"id"`
	TokenHash string     `gorm:"size:128" json:"-"`
	Remark    string     `gorm:"size:256" json:"remark"`
	ExpiresAt time.Time  `json:"expires_at"`
	Revoked   bool       `json:"revoked"`
	CreatedAt time.Time  `json:"created_at"`
}

// SigningKey Ed25519 签名密钥（kid 区分，多 key 共存实现轮换）
type SigningKey struct {
	KID        string    `gorm:"primarykey;size:16"`
	PrivateB64 string    `gorm:"type:text"`
	CreatedAt  time.Time
	Active     bool
}

// AuthCode 一次性授权码（5 分钟有效）
type AuthCode struct {
	Code                string    `gorm:"primarykey;size:64"`
	ClientID            string    `gorm:"size:64"`
	UserID              uint
	RedirectURI         string
	Scope               string
	Nonce               string
	CodeChallenge       string
	CodeChallengeMethod string
	ExpiresAt           time.Time
	Used                bool
}

// RefreshToken opaque 刷新令牌（使用即轮换）
type RefreshToken struct {
	Token     string    `gorm:"primarykey;size:64"`
	Hash      string    `gorm:"uniqueIndex;size:128"`
	ClientID  string    `gorm:"size:64"`
	UserID    uint
	Scope     string
	ExpiresAt time.Time
	Revoked   bool
	CreatedAt time.Time
}

// FeishuState 飞书上游登录 state（防 CSRF，10 分钟有效）
type FeishuState struct {
	State     string    `gorm:"primarykey;size:64"`
	ReturnTo  string
	ExpiresAt time.Time
}

// AuditLog 审计日志
type AuditLog struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Time      time.Time `json:"time"`
	Actor     string    `gorm:"index;size:64" json:"actor"`
	Action    string    `gorm:"index;size:32" json:"action"`
	Detail    string    `gorm:"size:512" json:"detail"`
	IP        string    `gorm:"size:64" json:"ip"`
}

// Setting 键值配置（飞书上游凭据等）
type Setting struct {
	Key   string `gorm:"primarykey;size:64"`
	Value string `gorm:"type:text"`
}
