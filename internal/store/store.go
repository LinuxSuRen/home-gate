// Package store 数据库初始化与数据访问（GORM + 纯 Go SQLite）。
package store

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/linuxsuren/home-gate/internal/model"
)

var ErrNotFound = gorm.ErrRecordNotFound

type DB struct {
	*gorm.DB
}

// Open 打开数据库并迁移表结构、初始化种子数据。
func Open(path string) (*DB, error) {
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}
	if err := gdb.AutoMigrate(
		&model.User{}, &model.Group{}, &model.Session{}, &model.Client{},
		&model.InitialToken{}, &model.SigningKey{}, &model.AuthCode{},
		&model.RefreshToken{}, &model.FeishuState{}, &model.AuditLog{}, &model.Setting{},
		&model.AppLink{},
	); err != nil {
		return nil, err
	}
	db := &DB{gdb}
	if err := db.seed(); err != nil {
		return nil, err
	}
	return db, nil
}

func (d *DB) seed() error {
	for _, name := range []string{"admin", "operate", "view"} {
		var g model.Group
		err := d.Where("name = ?", name).First(&g).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := d.Create(&model.Group{Name: name}).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	var cnt int64
	d.Model(&model.User{}).Count(&cnt)
	if cnt == 0 {
		hash, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		var adminGroup model.Group
		if err := d.Where("name = ?", "admin").First(&adminGroup).Error; err != nil {
			return err
		}
		admin := model.User{
			Username: "admin", PasswordHash: string(hash),
			Name: "管理员", Active: true, MustChangePasswd: true,
			Groups: []model.Group{adminGroup},
		}
		if err := d.Create(&admin).Error; err != nil {
			return err
		}
		fmt.Println("已创建初始管理员账号 admin / admin123（首次登录请修改密码）")
	}
	return nil
}

// RandomToken 生成随机 token（url-safe base64，n 字节熵）。
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func HashSecret(s string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(s), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return string(h)
}

func CheckSecret(hash, s string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(s)) == nil
}

// ---- 审计 ----

func (d *DB) Audit(actor, action, detail, ip string) {
	d.Create(&model.AuditLog{Time: time.Now(), Actor: actor, Action: action, Detail: detail, IP: ip})
}

// ---- 设置 ----

func (d *DB) GetSetting(key string) string {
	var s model.Setting
	if err := d.Where("key = ?", key).First(&s).Error; err != nil {
		return ""
	}
	return s.Value
}

func (d *DB) SetSetting(key, value string) error {
	return d.Where("key = ?", key).Assign(model.Setting{Value: value}).
		FirstOrCreate(&model.Setting{Key: key}).Error
}

// GetSettingInt 读取整型设置，缺失或非法时返回 def。
func (d *DB) GetSettingInt(key string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(d.GetSetting(key)))
	if err != nil || v <= 0 {
		return def
	}
	return v
}

// ---- 用户 ----

// FindUserByID 预加载用户组。
func (d *DB) FindUserByID(id uint) (*model.User, error) {
	var u model.User
	err := d.Preload("Groups").First(&u, id).Error
	return &u, err
}

func (d *DB) FindUserByUsername(username string) (*model.User, error) {
	var u model.User
	err := d.Preload("Groups").Where("username = ?", username).First(&u).Error
	return &u, err
}

func (d *DB) FindUserByFeishu(unionID string) (*model.User, error) {
	var u model.User
	err := d.Preload("Groups").Where("feishu_union_id = ?", unionID).First(&u).Error
	return &u, err
}

func (d *DB) ListUsers() ([]model.User, error) {
	var us []model.User
	err := d.Preload("Groups").Order("id").Find(&us).Error
	return us, err
}

func (d *DB) SetUserGroups(u *model.User, names []string) error {
	var gs []model.Group
	if len(names) > 0 {
		if err := d.Where("name IN ?", names).Find(&gs).Error; err != nil {
			return err
		}
	}
	return d.Model(u).Association("Groups").Replace(gs)
}

// ---- 会话 ----

// CreateSession 创建会话，有效期 ttl（由管理员设置，默认 24 小时）。
func (d *DB) CreateSession(userID uint, ip string, ttl time.Duration) (*model.Session, error) {
	s := &model.Session{
		ID: RandomToken(32), UserID: userID,
		ExpiresAt: time.Now().Add(ttl),
		CreatedAt: time.Now(), CreatedIP: ip,
	}
	return s, d.Create(s).Error
}

func (d *DB) GetSession(id string) (*model.Session, bool) {
	var s model.Session
	if err := d.Where("id = ?", id).First(&s).Error; err != nil {
		return nil, false
	}
	if time.Now().After(s.ExpiresAt) {
		d.Delete(&s)
		return nil, false
	}
	return &s, true
}

func (d *DB) TouchSession(s *model.Session, ttl time.Duration) {
	s.ExpiresAt = time.Now().Add(ttl)
	d.Save(s)
}

func (d *DB) DeleteSession(id string) {
	d.Where("id = ?", id).Delete(&model.Session{})
}

// ---- 客户端 ----

func (d *DB) FindClient(clientID string) (*model.Client, error) {
	var c model.Client
	err := d.Where("client_id = ?", clientID).First(&c).Error
	return &c, err
}

func (d *DB) ListClients() ([]model.Client, error) {
	var cs []model.Client
	err := d.Order("id").Find(&cs).Error
	return cs, err
}

// ---- DCR 初始令牌 ----

// ValidInitialToken 校验 initial access token 并返回其记录。
func (d *DB) ValidInitialToken(token string) (*model.InitialToken, bool) {
	var its []model.InitialToken
	if err := d.Find(&its).Error; err != nil {
		return nil, false
	}
	for i := range its {
		it := &its[i]
		if CheckSecret(it.TokenHash, token) {
			if it.Revoked || time.Now().After(it.ExpiresAt) {
				return nil, false
			}
			return it, true
		}
	}
	return nil, false
}

// ---- 过期数据清理 ----

func (d *DB) CleanupExpired() {
	now := time.Now()
	d.Where("expires_at < ?", now).Delete(&model.AuthCode{})
	d.Where("expires_at < ?", now).Delete(&model.FeishuState{})
	d.Where("expires_at < ? OR revoked = ?", now.Add(-72*time.Hour), true).Delete(&model.RefreshToken{})
	d.Where("expires_at < ?", now.Add(-72*time.Hour)).Delete(&model.Session{})
}

// TrimScope 规整 scope 串（去重、排序稳定）。
func TrimScope(scope string) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range strings.Fields(scope) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}
