package main

import (
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

//go:embed all:web
var webFS embed.FS
const (
	adminRole = "admin"
	userRole  = "user"
)

type app struct {
	db         *sql.DB
	dataDir    string
	storageDir string
}
type authContext struct {
	UserID       string
	Username     string
	RoleID       string
	Status       string
	CanUpload    bool
	CanDownload  bool
	MustChange   bool
	DeviceID     string
	RemoteIP     string
	SessionToken string
}
type storedFile struct {
	storedName string
	realPath   string
	size       int64
}
func main() {
	addr := flag.String("addr", ":8080", "HTTP 监听地址")
	dataDir := flag.String("data", ".", "数据根目录（数据库、上传文件、共享文件夹、回收站都存放在这里）")
	flag.Parse()

	gin.SetMode(gin.ReleaseMode)

	dataRoot := *dataDir
	storageDir := filepath.Join(dataRoot, "storage")
	if err := os.MkdirAll(filepath.Join(dataRoot, "data"), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		log.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "lan-share.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	a := &app{db: db, dataDir: dataRoot, storageDir: storageDir}
	if err := a.initDB(); err != nil {
		log.Fatal(err)
	}
	if err := a.initPhase2DB(); err != nil {
		log.Fatal(err)
	}
	if err := a.initPhase3DB(); err != nil {
		log.Fatal(err)
	}
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	router.MaxMultipartMemory = 32 << 20
	if err := router.SetTrustedProxies(nil); err != nil {
		log.Fatal(err)
	}
	router.Use(a.deviceMiddleware())
	a.registerRoutes(router)
	registerWeb(router)
	log.Printf("LAN Share running at http://localhost%s (data dir: %s)", *addr, dataRoot)
	if err := router.Run(*addr); err != nil {
		log.Fatal(err)
	}
}

// registerWeb 服务内嵌的前端产物；开发模式由 Vite (5173) 接管，生产模式单 exe 直接可用
func registerWeb(router *gin.Engine) {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			c.JSON(http.StatusNotFound, gin.H{"message": "接口不存在"})
			return
		}
		path := strings.TrimPrefix(c.Request.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(sub, path); err == nil {
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}
func (a *app) initDB() error {
	schema := `
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT UNIQUE NOT NULL,
  display_name TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  role_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  can_upload INTEGER NOT NULL DEFAULT 1,
  can_download INTEGER NOT NULL DEFAULT 1,
  must_change_password INTEGER NOT NULL DEFAULT 0,
  expires_at TEXT,
  created_at TEXT NOT NULL,
  last_login_at TEXT,
  remark TEXT
);
CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  device_name TEXT,
  status TEXT NOT NULL DEFAULT 'unknown',
  first_ip TEXT,
  last_ip TEXT,
  user_agent TEXT,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ip_rules (
  id TEXT PRIMARY KEY,
  rule_type TEXT NOT NULL,
  ip_value TEXT NOT NULL,
  reason TEXT,
  created_by TEXT,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS files (
  id TEXT PRIMARY KEY,
  original_name TEXT NOT NULL,
  stored_name TEXT NOT NULL,
  real_path TEXT NOT NULL,
  size INTEGER NOT NULL,
  owner_user_id TEXT NOT NULL,
  source_device_id TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS shares (
  id TEXT PRIMARY KEY,
  file_id TEXT NOT NULL,
  visibility TEXT NOT NULL,
  is_enabled INTEGER NOT NULL DEFAULT 1,
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS share_allowed_users (
  share_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  PRIMARY KEY (share_id, user_id)
);
CREATE TABLE IF NOT EXISTS transfers (
  id TEXT PRIMARY KEY,
  file_id TEXT,
  share_id TEXT,
  user_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  ip TEXT NOT NULL,
  direction TEXT NOT NULL,
  status TEXT NOT NULL,
  bytes_total INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS audit_logs (
  id TEXT PRIMARY KEY,
  event_type TEXT NOT NULL,
  operator_user_id TEXT,
  target_user_id TEXT,
  device_id TEXT,
  ip TEXT,
  message TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS generated_batches (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL,
  initial_password TEXT NOT NULL,
  created_at TEXT NOT NULL
);
`
	_, err := a.db.Exec(schema)
	return err
}
func (a *app) setupStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"initialized": a.count(`SELECT COUNT(*) FROM users WHERE role_id = 'admin'`) > 0})
}
func (a *app) setupAdmin(c *gin.Context) {
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"displayName"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "请求格式错误"})
		return
	}
	if req.Username == "" || req.DisplayName == "" || len(req.Password) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "用户名、显示名必填，密码至少 8 位"})
		return
	}
	if a.count(`SELECT COUNT(*) FROM users WHERE role_id = 'admin'`) > 0 {
		c.JSON(http.StatusConflict, gin.H{"message": "管理员已存在"})
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	id := uuid.NewString()
	_, err := a.db.Exec(`INSERT INTO users (id, username, display_name, password_hash, role_id, status, can_upload, can_download, created_at)
	VALUES (?, ?, ?, ?, ?, 'active', 1, 1, ?)`, id, req.Username, req.DisplayName, string(hash), adminRole, nowText())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	a.writeAudit("admin_created", id, "", c.GetString("device_id"), clientIP(c), "首次初始化管理员")
	c.JSON(http.StatusOK, gin.H{"message": "管理员创建成功"})
}
func (a *app) login(c *gin.Context) {
	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		DeviceName string `json:"deviceName"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "请求格式错误"})
		return
	}
	ip := clientIP(c)
	if blocked, _ := a.isIPBlocked(ip); blocked {
		a.writeAudit("login_failed", "", "", c.GetString("device_id"), ip, "IP 已被封禁")
		c.JSON(http.StatusForbidden, gin.H{"message": "当前 IP 已被封禁"})
		return
	}

	var user struct {
		ID, PasswordHash, RoleID, Status string
		CanUpload, CanDownload, MustChange int
		ExpiresAt sql.NullString
	}
	err := a.db.QueryRow(`SELECT id, password_hash, role_id, status, can_upload, can_download, must_change_password, expires_at
		FROM users WHERE username = ?`, req.Username).
		Scan(&user.ID, &user.PasswordHash, &user.RoleID, &user.Status, &user.CanUpload, &user.CanDownload, &user.MustChange, &user.ExpiresAt)
	if err != nil {
		a.writeAudit("login_failed", "", "", c.GetString("device_id"), ip, "用户名不存在")
		c.JSON(http.StatusUnauthorized, gin.H{"message": "用户名或密码错误"})
		return
	}
	if user.Status != "active" || isExpired(user.ExpiresAt.String) {
		a.writeAudit("login_failed", "", user.ID, c.GetString("device_id"), ip, "账号不可用")
		c.JSON(http.StatusForbidden, gin.H{"message": "账号已禁用或已过期"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		a.writeAudit("login_failed", "", user.ID, c.GetString("device_id"), ip, "密码错误")
		c.JSON(http.StatusUnauthorized, gin.H{"message": "用户名或密码错误"})
		return
	}
	if err := a.touchDevice(c.GetString("device_id"), req.DeviceName, ip, c.Request.UserAgent()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if blocked, _ := a.isDeviceBlocked(c.GetString("device_id")); blocked {
		a.writeAudit("login_failed", "", user.ID, c.GetString("device_id"), ip, "设备已拉黑")
		c.JSON(http.StatusForbidden, gin.H{"message": "当前设备已被拉黑"})
		return
	}
	if allowed, _ := a.userAllowsIP(user.ID, ip); !allowed {
		a.writeAudit("login_failed", "", user.ID, c.GetString("device_id"), ip, "IP not bound")
		c.JSON(http.StatusForbidden, gin.H{"message": "当前 IP 不在账号允许范围内"})
		return
	}
	if allowed, _ := a.userAllowsDevice(user.ID, c.GetString("device_id")); !allowed {
		a.writeAudit("login_failed", "", user.ID, c.GetString("device_id"), ip, "device not bound")
		c.JSON(http.StatusForbidden, gin.H{"message": "当前设备不在账号允许范围内"})
		return
	}

	token := randomToken(32)
	_, err = a.db.Exec(`INSERT INTO sessions (token, user_id, device_id, created_at) VALUES (?, ?, ?, ?)`,
		token, user.ID, c.GetString("device_id"), nowText())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	_, _ = a.db.Exec(`UPDATE users SET last_login_at = ? WHERE id = ?`, nowText(), user.ID)
	http.SetCookie(c.Writer, &http.Cookie{Name: "lan_share_token", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	a.writeAudit("login_success", user.ID, user.ID, c.GetString("device_id"), ip, "登录成功")
	c.JSON(http.StatusOK, gin.H{"message": "登录成功"})
}
func (a *app) logout(c *gin.Context) {
	ctx := mustAuth(c)
	_, _ = a.db.Exec(`DELETE FROM sessions WHERE token = ?`, ctx.SessionToken)
	http.SetCookie(c.Writer, &http.Cookie{Name: "lan_share_token", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	c.JSON(http.StatusOK, gin.H{"message": "已退出"})
}
func (a *app) me(c *gin.Context) {
	ctx := mustAuth(c)
	var displayName string
	_ = a.db.QueryRow(`SELECT display_name FROM users WHERE id = ?`, ctx.UserID).Scan(&displayName)
	c.JSON(http.StatusOK, gin.H{"user": gin.H{
		"id": ctx.UserID, "username": ctx.Username, "displayName": displayName, "roleId": ctx.RoleID,
		"canUpload": ctx.CanUpload, "canDownload": ctx.CanDownload, "mustChangePassword": ctx.MustChange,
	}})
}
func (a *app) overview(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"stats": gin.H{
		"users": a.count(`SELECT COUNT(*) FROM users`), "files": a.count(`SELECT COUNT(*) FROM files`),
		"shares": a.count(`SELECT COUNT(*) FROM shares WHERE is_enabled = 1`), "devices": a.count(`SELECT COUNT(*) FROM devices`),
		"blockedIP": a.count(`SELECT COUNT(*) FROM ip_rules WHERE rule_type = 'deny'`),
	}})
}
func (a *app) listUsers(c *gin.Context) {
	rows, err := a.db.Query(`SELECT id, username, display_name, role_id, status, can_upload, can_download, created_at, last_login_at
		FROM users ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer rows.Close()

	var users []gin.H
	for rows.Next() {
		var id, username, displayName, roleID, status, createdAt string
		var lastLogin sql.NullString
		var canUpload, canDownload int
		_ = rows.Scan(&id, &username, &displayName, &roleID, &status, &canUpload, &canDownload, &createdAt, &lastLogin)
		users = append(users, gin.H{
			"id": id, "username": username, "displayName": displayName, "roleId": roleID, "status": status,
			"canUpload": canUpload == 1, "canDownload": canDownload == 1, "createdAt": createdAt, "lastLoginAt": lastLogin.String,
		})
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}
func (a *app) createUser(c *gin.Context) {
	var req struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		Password    string `json:"password"`
		RoleID      string `json:"roleId"`
		Status      string `json:"status"`
		CanUpload   bool   `json:"canUpload"`
		CanDownload bool   `json:"canDownload"`
		ExpiresAt   string `json:"expiresAt"`
		Remark      string `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.DisplayName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "用户名和显示名必填"})
		return
	}
	if req.RoleID == "" {
		req.RoleID = userRole
	}
	if req.Status == "" {
		req.Status = "active"
	}
	password := req.Password
	if password == "" {
		password = randomPassword()
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	id := uuid.NewString()
	_, err := a.db.Exec(`INSERT INTO users (id, username, display_name, password_hash, role_id, status, can_upload, can_download, created_at, expires_at, remark)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)`,
		id, req.Username, req.DisplayName, string(hash), req.RoleID, req.Status, boolInt(req.CanUpload), boolInt(req.CanDownload), nowText(), req.ExpiresAt, req.Remark)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "创建失败，用户名可能重复"})
		return
	}
	a.writeAudit("user_created", mustAuth(c).UserID, id, c.GetString("device_id"), clientIP(c), "创建用户 "+req.Username)
	c.JSON(http.StatusOK, gin.H{"message": "创建成功", "initialPassword": password})
}
func (a *app) batchGenerateUsers(c *gin.Context) {
	var req struct {
		Prefix        string `json:"prefix"`
		StartNumber   int    `json:"startNumber"`
		Count         int    `json:"count"`
		NumberWidth   int    `json:"numberWidth"`
		RoleID        string `json:"roleId"`
		DefaultStatus string `json:"defaultStatus"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Prefix == "" || req.Count <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}
	if req.NumberWidth <= 0 {
		req.NumberWidth = 3
	}
	if req.RoleID == "" {
		req.RoleID = userRole
	}
	if req.DefaultStatus == "" {
		req.DefaultStatus = "active"
	}
	_, _ = a.db.Exec(`DELETE FROM generated_batches`)

	created := make([]gin.H, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		username := fmt.Sprintf("%s%0*d", req.Prefix, req.NumberWidth, req.StartNumber+i)
		password := randomPassword()
		hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		id := uuid.NewString()
		if _, err := a.db.Exec(`INSERT INTO users (id, username, display_name, password_hash, role_id, status, can_upload, can_download, must_change_password, created_at)
			VALUES (?, ?, ?, ?, ?, ?, 1, 1, 1, ?)`, id, username, username, string(hash), req.RoleID, req.DefaultStatus, nowText()); err != nil {
			continue
		}
		_, _ = a.db.Exec(`INSERT INTO generated_batches (id, username, initial_password, created_at) VALUES (?, ?, ?, ?)`,
			uuid.NewString(), username, password, nowText())
		created = append(created, gin.H{"username": username, "initialPassword": password})
	}
	a.writeAudit("users_batch_created", mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), fmt.Sprintf("批量生成 %d 个账号", len(created)))
	c.JSON(http.StatusOK, gin.H{"createdCount": len(created), "users": created})
}
func (a *app) exportLatestBatch(c *gin.Context) {
	rows, err := a.db.Query(`SELECT username, initial_password, created_at FROM generated_batches ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="latest-batch.csv"`)
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{"username", "initial_password", "created_at"})
	for rows.Next() {
		var username, password, createdAt string
		_ = rows.Scan(&username, &password, &createdAt)
		_ = writer.Write([]string{username, password, createdAt})
	}
	writer.Flush()
}
func (a *app) disableUser(c *gin.Context) { a.setUserStatus(c, "disabled", "user_disabled") }
func (a *app) enableUser(c *gin.Context)  { a.setUserStatus(c, "active", "user_enabled") }
func (a *app) setUserStatus(c *gin.Context, status, event string) {
	id := c.Param("id")
	if protected, _ := a.isProtectedAdminUser(id); protected { c.JSON(http.StatusForbidden, gin.H{"message": "管理员权限为最高级，不支持修改"}); return }
	if _, err := a.db.Exec(`UPDATE users SET status = ? WHERE id = ?`, status, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if status != "active" {
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, id)
	}
	a.writeAudit(event, mustAuth(c).UserID, id, c.GetString("device_id"), clientIP(c), "修改用户状态为 "+status)
	c.JSON(http.StatusOK, gin.H{"message": "操作成功"})
}
func (a *app) listIPRules(c *gin.Context) {
	rows, err := a.db.Query(`SELECT id, rule_type, ip_value, reason, created_at FROM ip_rules ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer rows.Close()

	var rules []gin.H
	for rows.Next() {
		var id, ruleType, ipValue, reason, createdAt string
		_ = rows.Scan(&id, &ruleType, &ipValue, &reason, &createdAt)
		rules = append(rules, gin.H{"id": id, "ruleType": ruleType, "ipValue": ipValue, "reason": reason, "createdAt": createdAt})
	}
	c.JSON(http.StatusOK, gin.H{"rules": rules})
}
func (a *app) createIPRule(c *gin.Context) {
	var req struct {
		RuleType string `json:"ruleType"`
		IPValue  string `json:"ipValue"`
		Reason   string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.RuleType == "" || req.IPValue == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}
	if net.ParseIP(req.IPValue) == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "MVP 仅支持单个 IP"})
		return
	}
	_, err := a.db.Exec(`INSERT INTO ip_rules (id, rule_type, ip_value, reason, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), req.RuleType, req.IPValue, req.Reason, mustAuth(c).UserID, nowText())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	a.writeAudit("ip_blocked", mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), "新增 IP 规则 "+req.IPValue)
	c.JSON(http.StatusOK, gin.H{"message": "添加成功"})
}
func (a *app) listDevices(c *gin.Context) {
	rows, err := a.db.Query(`SELECT id, device_name, status, first_ip, last_ip, first_seen_at, last_seen_at FROM devices ORDER BY last_seen_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer rows.Close()

	var devices []gin.H
	for rows.Next() {
		var id, deviceName, status, firstIP, lastIP, firstSeenAt, lastSeenAt string
		_ = rows.Scan(&id, &deviceName, &status, &firstIP, &lastIP, &firstSeenAt, &lastSeenAt)
		devices = append(devices, gin.H{"id": id, "deviceName": deviceName, "status": status, "firstIP": firstIP, "lastIP": lastIP, "firstSeenAt": firstSeenAt, "lastSeenAt": lastSeenAt})
	}
	c.JSON(http.StatusOK, gin.H{"devices": devices})
}
func (a *app) blockDevice(c *gin.Context) {
	id := c.Param("id")
	_, err := a.db.Exec(`UPDATE devices SET status = 'blocked' WHERE id = ?`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	_, _ = a.db.Exec(`DELETE FROM sessions WHERE device_id = ?`, id)
	a.writeAudit("device_blocked", mustAuth(c).UserID, "", id, clientIP(c), "拉黑设备")
	c.JSON(http.StatusOK, gin.H{"message": "设备已拉黑"})
}
func (a *app) listAuditLogs(c *gin.Context) {
	rows, err := a.db.Query(`SELECT id, event_type, message, ip, created_at FROM audit_logs ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer rows.Close()

	var logs []gin.H
	for rows.Next() {
		var id, eventType, message, ip, createdAt string
		_ = rows.Scan(&id, &eventType, &message, &ip, &createdAt)
		logs = append(logs, gin.H{"id": id, "eventType": eventType, "message": message, "ip": ip, "createdAt": createdAt})
	}
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}
func (a *app) listTransfers(c *gin.Context) {
	rows, err := a.db.Query(`SELECT id, direction, status, bytes_total, ip, created_at FROM transfers ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer rows.Close()

	var transfers []gin.H
	for rows.Next() {
		var id, direction, status, ip, createdAt string
		var bytesTotal int64
		_ = rows.Scan(&id, &direction, &status, &bytesTotal, &ip, &createdAt)
		transfers = append(transfers, gin.H{"id": id, "direction": direction, "status": status, "bytesTotal": bytesTotal, "ip": ip, "createdAt": createdAt})
	}
	c.JSON(http.StatusOK, gin.H{"transfers": transfers})
}
func (a *app) listFiles(c *gin.Context) {
	ctx := mustAuth(c)
	query := `SELECT id, original_name, size, owner_user_id, created_at FROM files WHERE COALESCE(is_deleted, 0) = 0`
	args := []any{}
	if ctx.RoleID != adminRole {
		query += ` AND owner_user_id = ?`
		args = append(args, ctx.UserID)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := a.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer rows.Close()

	var files []gin.H
	for rows.Next() {
		var id, originalName, ownerUserID, createdAt string
		var size int64
		_ = rows.Scan(&id, &originalName, &size, &ownerUserID, &createdAt)
		files = append(files, gin.H{"id": id, "originalName": originalName, "size": size, "ownerUserId": ownerUserID, "createdAt": createdAt})
	}
	c.JSON(http.StatusOK, gin.H{"files": files})
}

func (a *app) uploadFile(c *gin.Context) {
	ctx := mustAuth(c)
	if !ctx.CanUpload {
		c.JSON(http.StatusForbidden, gin.H{"message": "当前账号没有上传权限"})
		return
	}
	permissions, err := a.loadUserPermissionConfig(ctx.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if blocked, _ := a.isIPBlocked(ctx.RemoteIP); blocked {
		c.JSON(http.StatusForbidden, gin.H{"message": "当前 IP 已被封禁"})
		return
	}
	if blocked, _ := a.isDeviceBlocked(ctx.DeviceID); blocked {
		c.JSON(http.StatusForbidden, gin.H{"message": "当前设备已被拉黑"})
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "请选择文件"})
		return
	}
	defer file.Close()
	if permissions.MaxFileSizeMB > 0 && header.Size > int64(permissions.MaxFileSizeMB)*1024*1024 {
		c.JSON(http.StatusForbidden, gin.H{"message": "文件大小超过账号限制"})
		return
	}
	if permissions.DailyUploadLimitMB > 0 {
		used := a.currentUploadUsageBytes(ctx.UserID)
		limit := int64(permissions.DailyUploadLimitMB) * 1024 * 1024
		if used+header.Size > limit {
			c.JSON(http.StatusForbidden, gin.H{"message": "今日上传总量已超限"})
			return
		}
	}

	meta, err := a.storeUpload(file, header)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	fileID := uuid.NewString()
	_, err = a.db.Exec(`INSERT INTO files (id, original_name, stored_name, real_path, size, owner_user_id, source_device_id, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		fileID, header.Filename, meta.storedName, meta.realPath, meta.size, ctx.UserID, ctx.DeviceID, nowText())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	a.writeTransfer(fileID, "", ctx.UserID, ctx.DeviceID, ctx.RemoteIP, "upload", "success", meta.size)
	a.writeAudit("file_uploaded", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "上传文件 "+header.Filename)
	c.JSON(http.StatusOK, gin.H{"message": "上传成功"})
}
func (a *app) createShare(c *gin.Context) {
	ctx := mustAuth(c)
	permissions, err := a.loadUserPermissionConfig(ctx.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if ctx.RoleID != adminRole && !permissions.CanCreateShare {
		c.JSON(http.StatusForbidden, gin.H{"message": "当前账号没有创建共享权限"})
		return
	}
	var req struct {
		FileID              string   `json:"fileId"`
		Visibility          string   `json:"visibility"`
		AllowedUserIDs      []string `json:"allowedUserIds"`
		AllowedRoleIDs      []string `json:"allowedRoleIds"`
		AllowedDeviceIDs    []string `json:"allowedDeviceIds"`
		AllowedIPs          []string `json:"allowedIPs"`
		RequireFilePassword bool     `json:"requireFilePassword"`
		FilePassword        string   `json:"filePassword"`
		ExpiresAt           string   `json:"expiresAt"`
		MaxDownloads        int      `json:"maxDownloads"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.FileID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}
	if req.Visibility == "" {
		if len(req.AllowedUserIDs) > 0 {
			req.Visibility = "specified_users"
		} else if len(req.AllowedRoleIDs) > 0 {
			req.Visibility = "specified_roles"
		} else if len(req.AllowedDeviceIDs) > 0 {
			req.Visibility = "specified_devices"
		} else if len(req.AllowedIPs) > 0 {
			req.Visibility = "specified_ips"
		} else {
			req.Visibility = "all_users"
		}
	}
	passwordHash := ""
	if req.RequireFilePassword {
		if req.FilePassword == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "文件密码不能为空"})
			return
		}
		hash, _ := bcrypt.GenerateFromPassword([]byte(req.FilePassword), bcrypt.DefaultCost)
		passwordHash = string(hash)
	}
	shareID := uuid.NewString()
	_, err = a.db.Exec(`INSERT INTO shares (id, file_id, visibility, is_enabled, require_file_password, file_password_hash, expires_at, max_downloads, download_count, created_by, created_at)
		VALUES (?, ?, ?, 1, ?, NULLIF(?, ''), NULLIF(?, ''), ?, 0, ?, ?)`,
		shareID, req.FileID, req.Visibility, boolInt(req.RequireFilePassword), passwordHash, req.ExpiresAt, req.MaxDownloads, ctx.UserID, nowText())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if err := a.upsertShareRelations(shareID, req.AllowedUserIDs, "share_allowed_users", "user_id"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if err := a.upsertShareRelations(shareID, req.AllowedRoleIDs, "share_allowed_roles", "role_id"); err != nil { c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()}); return }
	if err := a.upsertShareRelations(shareID, req.AllowedDeviceIDs, "share_allowed_devices", "device_id"); err != nil { c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()}); return }
	if err := a.upsertShareRelations(shareID, req.AllowedIPs, "share_allowed_ips", "ip_value"); err != nil { c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()}); return }
	a.writeAudit("share_created", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "创建共享 "+shareID)
	c.JSON(http.StatusOK, gin.H{"message": "共享已创建"})
}
func (a *app) listShares(c *gin.Context) {
	ctx := mustAuth(c)
	permissions, err := a.loadUserPermissionConfig(ctx.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if ctx.RoleID != adminRole && !permissions.CanViewSharedList {
		c.JSON(http.StatusOK, gin.H{"shares": []gin.H{}})
		return
	}
	rows, err := a.db.Query(`SELECT id FROM shares ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer rows.Close()

	var shares []gin.H
	for rows.Next() {
		var shareID string
		_ = rows.Scan(&shareID)
		details, err := a.loadShareDetails(shareID)
		if err != nil {
			continue
		}
		if ok, _ := a.shareVisibleToUser(details, ctx, ""); !ok {
			continue
		}
		shares = append(shares, gin.H{
			"id":                  details.ID,
			"visibility":          details.Visibility,
			"fileName":            details.FileName,
			"size":                details.FileSize,
			"createdAt":           details.CreatedAt,
			"expiresAt":           details.ExpiresAt,
			"maxDownloads":        details.MaxDownloads,
			"downloadCount":       details.DownloadCount,
			"requireFilePassword": details.RequireFilePassword,
		})
	}
	c.JSON(http.StatusOK, gin.H{"shares": shares})
}
func (a *app) downloadShare(c *gin.Context) {
	ctx := mustAuth(c)
	if !ctx.CanDownload {
		c.JSON(http.StatusForbidden, gin.H{"message": "当前账号没有下载权限"})
		return
	}
	if blocked, _ := a.isIPBlocked(ctx.RemoteIP); blocked {
		c.JSON(http.StatusForbidden, gin.H{"message": "当前 IP 已被封禁"})
		return
	}
	if blocked, _ := a.isDeviceBlocked(ctx.DeviceID); blocked {
		c.JSON(http.StatusForbidden, gin.H{"message": "当前设备已被拉黑"})
		return
	}
	details, err := a.loadShareDetails(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "共享不存在"})
		return
	}
	if ok, _ := a.shareVisibleToUser(details, ctx, ""); !ok {
		a.writeAudit("permission_denied", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "下载无权限共享")
		c.JSON(http.StatusForbidden, gin.H{"message": "你没有访问该文件的权限，请联系管理员"})
		return
	}
	if !a.verifySharePassword(details, c.Query("password")) {
		c.JSON(http.StatusForbidden, gin.H{"message": "文件密码错误或缺失"})
		return
	}
	_, _ = a.db.Exec(`UPDATE shares SET download_count = download_count + 1 WHERE id = ?`, details.ID)
	a.writeTransfer(details.FileID, c.Param("id"), ctx.UserID, ctx.DeviceID, ctx.RemoteIP, "download", "success", fileSize(details.FilePath))
	a.writeAudit("file_downloaded", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "下载文件 "+details.FileName)
	c.FileAttachment(details.FilePath, details.FileName)
}

func (a *app) authRequired(adminOnly bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Request.Cookie("lan_share_token")
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "请先登录"})
			return
		}
		ctx, err := a.loadSession(cookie.Value)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "登录已失效"})
			return
		}
		if adminOnly && ctx.RoleID != adminRole {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "需要管理员权限"})
			return
		}
		currentIP := clientIP(c)
		if allowed, _ := a.userAllowsIP(ctx.UserID, currentIP); !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "当前 IP 不在账号允许范围内"})
			return
		}
		if allowed, _ := a.userAllowsDevice(ctx.UserID, ctx.DeviceID); !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "当前设备不在账号允许范围内"})
			return
		}
		c.Set("auth", ctx)
		c.Next()
	}
}

func (a *app) loadSession(token string) (*authContext, error) {
	var ctx authContext
	var canUpload, canDownload, mustChange int
	var expiresAt sql.NullString
	err := a.db.QueryRow(`SELECT s.token, s.device_id, u.id, u.username, u.role_id, u.status, u.can_upload, u.can_download, u.must_change_password, u.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token = ?`, token).
		Scan(&ctx.SessionToken, &ctx.DeviceID, &ctx.UserID, &ctx.Username, &ctx.RoleID, &ctx.Status, &canUpload, &canDownload, &mustChange, &expiresAt)
	if err != nil {
		return nil, err
	}
	if ctx.Status != "active" || isExpired(expiresAt.String) {
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
		return nil, errors.New("账号不可用")
	}
	if blocked, _ := a.isDeviceBlocked(ctx.DeviceID); blocked {
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
		return nil, errors.New("设备已拉黑")
	}
	ctx.CanUpload = canUpload == 1 || ctx.RoleID == adminRole
	ctx.CanDownload = canDownload == 1 || ctx.RoleID == adminRole
	ctx.MustChange = mustChange == 1
	return &ctx, nil
}

func (a *app) deviceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		deviceID := c.GetHeader("X-Device-Id")
		if deviceID == "" {
			if cookie, err := c.Request.Cookie("lan_share_device"); err == nil {
				deviceID = cookie.Value
			}
		}
		if deviceID == "" {
			deviceID = uuid.NewString()
			http.SetCookie(c.Writer, &http.Cookie{Name: "lan_share_device", Value: deviceID, Path: "/", MaxAge: 86400 * 365, SameSite: http.SameSiteLaxMode})
		}
		c.Set("device_id", deviceID)
		c.Next()
	}
}

func (a *app) touchDevice(id, name, ip, userAgent string) error {
	if name == "" {
		name = "Browser"
	}
	now := nowText()
	_, err := a.db.Exec(`INSERT INTO devices (id, device_name, status, first_ip, last_ip, user_agent, first_seen_at, last_seen_at)
	VALUES (?, ?, 'unknown', ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET device_name = excluded.device_name, last_ip = excluded.last_ip, user_agent = excluded.user_agent, last_seen_at = excluded.last_seen_at`,
		id, name, ip, ip, userAgent, now, now)
	return err
}

func (a *app) isIPBlocked(ip string) (bool, error) {
	var count int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM ip_rules WHERE rule_type = 'deny' AND ip_value = ?`, ip).Scan(&count)
	return count > 0, err
}

func (a *app) isDeviceBlocked(deviceID string) (bool, error) {
	var status string
	err := a.db.QueryRow(`SELECT status FROM devices WHERE id = ?`, deviceID).Scan(&status)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return status == "blocked", err
}

func (a *app) storeUpload(file multipart.File, header *multipart.FileHeader) (*storedFile, error) {
	ext := filepath.Ext(header.Filename)
	base := strings.TrimSuffix(header.Filename, ext)
	safeBase := strings.Map(func(r rune) rune {
		if r == '.' || r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return r
		}
		return '_'
	}, base)
	if safeBase == "" {
		safeBase = "file"
	}
	storedName := uuid.NewString() + "_" + safeBase + ext
	realPath := filepath.Join(a.storageDir, storedName)
	out, err := os.Create(realPath)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	size, err := io.Copy(out, file)
	if err != nil {
		return nil, err
	}
	return &storedFile{storedName: storedName, realPath: realPath, size: size}, nil
}

func (a *app) writeTransfer(fileID, shareID, userID, deviceID, ip, direction, status string, bytesTotal int64) {
	_, _ = a.db.Exec(`INSERT INTO transfers (id, file_id, share_id, user_id, device_id, ip, direction, status, bytes_total, created_at)
	VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), fileID, shareID, userID, deviceID, ip, direction, status, bytesTotal, nowText())
}

func (a *app) writeAudit(eventType, operatorUserID, targetUserID, deviceID, ip, message string) {
	_, _ = a.db.Exec(`INSERT INTO audit_logs (id, event_type, operator_user_id, target_user_id, device_id, ip, message, created_at)
	VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)`,
		uuid.NewString(), eventType, operatorUserID, targetUserID, deviceID, ip, message, nowText())
}

func (a *app) count(query string) int {
	var n int
	_ = a.db.QueryRow(query).Scan(&n)
	return n
}

func mustAuth(c *gin.Context) *authContext {
	ctxAny, _ := c.Get("auth")
	ctx := ctxAny.(*authContext)
	ctx.RemoteIP = clientIP(c)
	return ctx
}

func clientIP(c *gin.Context) string {
	ip := c.ClientIP()
	if strings.Contains(ip, "::1") {
		return "127.0.0.1"
	}
	return ip
}

func nowText() string {
	return time.Now().Format(time.RFC3339)
}

func isExpired(value string) bool {
	if value == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t, err = time.Parse("2006-01-02", value)
	}
	return err == nil && time.Now().After(t)
}

func randomToken(size int) string {
	buf := make([]byte, size)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func randomPassword() string {
	// ponytail: 16 位 hex（64 bit 熵），需更强时换 4 词 diceware
	return "Ls@" + randomToken(8)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
