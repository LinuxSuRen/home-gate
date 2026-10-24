// Package token Ed25519 签名密钥管理与 JWT 签发/校验。
package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"github.com/linuxsuren/home-gate/internal/model"
)

type Manager struct {
	db   *gorm.DB
	mu   sync.RWMutex
	kid  string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// NewManager 加载或生成激活的签名密钥。
func NewManager(db *gorm.DB) (*Manager, error) {
	m := &Manager{db: db}
	var keys []model.SigningKey
	if err := db.Where("active = ?", true).Order("created_at DESC").Find(&keys).Error; err != nil {
		return nil, err
	}
	for i := range keys {
		priv, err := decodePrivate(keys[i].PrivateB64)
		if err != nil {
			continue
		}
		m.kid, m.priv, m.pub = keys[i].KID, priv, priv.Public().(ed25519.PublicKey)
		return m, nil
	}
	// 生成新密钥
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(pub)
	kid := hex.EncodeToString(sum[:])[:12]
	m.kid, m.priv, m.pub = kid, priv, pub
	return m, m.db.Create(&model.SigningKey{
		KID: kid, PrivateB64: encodePrivate(priv), CreatedAt: time.Now(), Active: true,
	}).Error
}

func encodePrivate(priv ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(priv)
}

func decodePrivate(b64 string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("bad private key size")
	}
	return ed25519.PrivateKey(raw), nil
}

// Claims 自定义声明集合，由调用方按 token 类型组装。
type Claims struct {
	Iss     string   `json:"iss"`
	Sub     string   `json:"sub"`
	Aud     string   `json:"aud,omitempty"`
	Exp     int64    `json:"exp"`
	Iat     int64    `json:"iat"`
	Scope   string   `json:"scope,omitempty"`
	Nonce   string   `json:"nonce,omitempty"`
	Name    string   `json:"name,omitempty"`
	Email   string   `json:"email,omitempty"`
	Prefer  string   `json:"preferred_username,omitempty"`
	Groups  []string `json:"groups,omitempty"`
}

func (m *Manager) Sign(c *Claims) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": c.Iss, "sub": c.Sub, "aud": c.Aud,
		"exp": c.Exp, "iat": c.Iat,
		"scope": c.Scope, "nonce": c.Nonce,
		"name": c.Name, "email": c.Email,
		"preferred_username": c.Prefer, "groups": c.Groups,
	})
	t.Header["kid"] = m.kid
	return t.SignedString(m.priv)
}

// Parse 校验签名与 iss/aud，返回声明。
func (m *Manager) Parse(tokenStr, issuer, audience string) (*Claims, error) {
	m.mu.RLock()
	priv := m.priv
	m.mu.RUnlock()
	opts := []jwt.ParserOption{jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithExpirationRequired()}
	if issuer != "" {
		opts = append(opts, jwt.WithIssuer(issuer))
	}
	if audience != "" {
		opts = append(opts, jwt.WithAudience(audience))
	}
	parsed, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return priv.Public(), nil
	}, opts...)
	if err != nil || !parsed.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims")
	}
	c := &Claims{}
	c.Iss, _ = mc["iss"].(string)
	c.Sub, _ = mc["sub"].(string)
	c.Aud, _ = mc["aud"].(string)
	c.Scope, _ = mc["scope"].(string)
	c.Nonce, _ = mc["nonce"].(string)
	c.Name, _ = mc["name"].(string)
	c.Email, _ = mc["email"].(string)
	c.Prefer, _ = mc["preferred_username"].(string)
	if gs, ok := mc["groups"].([]any); ok {
		for _, g := range gs {
			if s, ok := g.(string); ok {
				c.Groups = append(c.Groups, s)
			}
		}
	}
	return c, nil
}

// JWKS 公钥集合（OKP / Ed25519）。
func (m *Manager) JWKS() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return map[string]any{
		"keys": []map[string]any{{
			"kty": "OKP", "crv": "Ed25519", "use": "sig", "alg": "EdDSA",
			"kid": m.kid,
			"x":   base64.RawURLEncoding.EncodeToString(m.pub),
		}},
	}
}
