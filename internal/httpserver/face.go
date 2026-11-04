package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/linuxsuren/home-gate/internal/model"
)

// faceRecognitionConfig reads the face-recognition-server integration
// settings; returns zero values when not configured.
func (s *Server) faceRecognitionConfig() (baseURL, token string) {
	baseURL = s.db.GetSetting("face_recognition_url")
	token = s.db.GetSetting("face_recognition_token")
	return
}

// handleFaceEnabled reports whether face login is available.
func (s *Server) handleFaceEnabled(w http.ResponseWriter, r *http.Request) {
	baseURL, token := s.faceRecognitionConfig()
	writeJSON(w, http.StatusOK, map[string]any{"enabled": baseURL != "" && token != ""})
}

// handleFaceLogin accepts a webcam photo, delegates identification to
// face-recognition-server, and opens a home-gate session for the matched
// person (auto-provisioning on first login).
func (s *Server) handleFaceLogin(w http.ResponseWriter, r *http.Request) {
	baseURL, token := s.faceRecognitionConfig()
	if baseURL == "" || token == "" {
		apiError(w, http.StatusNotImplemented, "not_configured", "人脸识别登录未配置")
		return
	}

	// read the uploaded image
	file, _, err := r.FormFile("image")
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request", "请上传人脸照片")
		return
	}
	defer file.Close()
	image, err := io.ReadAll(io.LimitReader(file, 10<<20))
	if err != nil || len(image) == 0 {
		apiError(w, http.StatusBadRequest, "invalid_request", "读取照片失败")
		return
	}

	// call face-recognition-server identify
	matches, err := s.faceIdentify(baseURL, token, image)
	if err != nil {
		s.db.Audit("face", "login_failed", err.Error(), clientIP(r))
		apiError(w, http.StatusUnauthorized, "face_error", "人脸识别服务不可用")
		return
	}
	if len(matches) == 0 {
		s.db.Audit("face", "login_failed", "未识别到已录入的人脸", clientIP(r))
		apiError(w, http.StatusUnauthorized, "no_match", "未识别到已录入的人脸，请先在人脸识别平台录入")
		return
	}
	best := matches[0]

	// find or provision the user
	u, err := s.faceFindOrCreateUser(r, best.Subject, best.Name)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "查找或创建用户失败")
		return
	}

	ttl := s.sessionTTL()
	sess, err := s.db.CreateSession(u.ID, clientIP(r), ttl)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "创建会话失败")
		return
	}
	s.db.Audit(u.Username, "login", fmt.Sprintf("人脸登录（相似度 %.2f）", best.Similarity), clientIP(r))
	s.setSessionCookie(w, sess.ID, ttl)
	writeJSON(w, http.StatusOK, map[string]any{"data": userBrief(u)})
}

// FaceMatch is one result from face-recognition-server identify.
type FaceMatch struct {
	Subject    string  `json:"employee_no"`
	Name       string  `json:"name"`
	Similarity float64 `json:"similarity"`
}

// faceIdentify calls face-recognition-server POST /api/v1/identify.
func (s *Server) faceIdentify(baseURL, token string, image []byte) ([]FaceMatch, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "login.jpg")
	if err != nil {
		return nil, fmt.Errorf("构建请求: %w", err)
	}
	if _, err := part.Write(image); err != nil {
		return nil, fmt.Errorf("写入图片: %w", err)
	}
	writer.Close()

	req, err := http.NewRequest("POST", baseURL+"/api/v1/identify", &body)
	if err != nil {
		return nil, fmt.Errorf("构建请求: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用识别服务: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("识别服务返回 %d", resp.StatusCode)
	}

	var result struct {
		FaceCount int         `json:"face_count"`
		Matches   []FaceMatch `json:"matches"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析响应: %w", err)
	}
	return result.Matches, nil
}

// faceFindOrCreateUser maps an identified face subject to a home-gate user;
// first-time subjects are auto-provisioned into the view group.
func (s *Server) faceFindOrCreateUser(r *http.Request, subject, name string) (*model.User, error) {
	// existing binding
	var u model.User
	if err := s.db.Where("face_subject = ?", subject).First(&u).Error; err == nil {
		if !u.Active {
			return nil, fmt.Errorf("账号已禁用")
		}
		return &u, nil
	}

	// also try matching by username (admin may have pre-created the account
	// with username = employee_no)
	if err := s.db.Where("username = ?", subject).First(&u).Error; err == nil {
		if !u.Active {
			return nil, fmt.Errorf("账号已禁用")
		}
		// bind the face to this existing account
		u.FaceSubject = subject
		if err := s.db.Save(&u).Error; err != nil {
			return nil, fmt.Errorf("绑定人脸: %w", err)
		}
		return &u, nil
	}

	// auto-provision into the view group
	var viewGroup model.Group
	if err := s.db.Where("name = ?", "view").First(&viewGroup).Error; err != nil {
		return nil, fmt.Errorf("view 组不存在: %w", err)
	}
	username := subject
	if username == "" {
		username = "face-" + name
	}
	// deduplicate username
	base := username
	for i := 1; ; i++ {
		if _, err := s.db.FindUserByUsername(username); err != nil {
			break
		}
		username = fmt.Sprintf("%s%d", base, i)
	}
	newUser := &model.User{
		Username:    username,
		Name:        name,
		FaceSubject: subject,
		Active:      true,
		Groups:      []model.Group{viewGroup},
	}
	if err := s.db.Create(newUser).Error; err != nil {
		return nil, fmt.Errorf("创建用户: %w", err)
	}
	s.db.Audit(username, "auto_provision", "人脸识别首次登录自动开户", clientIP(r))
	return newUser, nil
}
