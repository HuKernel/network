package main

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
	"golang.org/x/crypto/bcrypt"
)

type roleTemplate struct {
	ID          string
	Name        string
	Description string
	IsSystem    bool
	CanUpload   bool
	CanDownload bool
}

type userBinding struct {
	AllowedIPs       []string `json:"allowedIPs"`
	AllowedDeviceIDs []string `json:"allowedDeviceIds"`
}

type shareDetails struct {
	ID                  string
	FileID              string
	Visibility          string
	IsEnabled           bool
	RequireFilePassword bool
	FilePasswordHash    string
	ExpiresAt           string
	MaxDownloads        int
	DownloadCount       int
	FilePath            string
	FileName            string
	FileSize            int64
	CreatedAt           string
}

type importedUserRow struct {
	Username    string
	DisplayName string
	Password    string
	RoleID      string
	Status      string
	ExpiresAt   string
	CanUpload   *bool
	CanDownload *bool
	Remark      string
}

func (a *app) initPhase2DB() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS roles (
			id TEXT PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			description TEXT,
			is_system INTEGER NOT NULL DEFAULT 0,
			can_upload INTEGER NOT NULL DEFAULT 1,
			can_download INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS user_allowed_ips (
			user_id TEXT NOT NULL,
			ip_value TEXT NOT NULL,
			PRIMARY KEY (user_id, ip_value)
		)`,
		`CREATE TABLE IF NOT EXISTS user_allowed_devices (
			user_id TEXT NOT NULL,
			device_id TEXT NOT NULL,
			PRIMARY KEY (user_id, device_id)
		)`,
		`CREATE TABLE IF NOT EXISTS share_allowed_roles (
			share_id TEXT NOT NULL,
			role_id TEXT NOT NULL,
			PRIMARY KEY (share_id, role_id)
		)`,
		`CREATE TABLE IF NOT EXISTS share_allowed_devices (
			share_id TEXT NOT NULL,
			device_id TEXT NOT NULL,
			PRIMARY KEY (share_id, device_id)
		)`,
		`CREATE TABLE IF NOT EXISTS share_allowed_ips (
			share_id TEXT NOT NULL,
			ip_value TEXT NOT NULL,
			PRIMARY KEY (share_id, ip_value)
		)`,
	}
	for _, statement := range statements {
		if _, err := a.db.Exec(statement); err != nil {
			return err
		}
	}

	columns := []struct {
		table  string
		column string
		sql    string
	}{
		{"users", "can_view_shared_list", `ALTER TABLE users ADD COLUMN can_view_shared_list INTEGER NOT NULL DEFAULT 1`},
		{"users", "can_create_share", `ALTER TABLE users ADD COLUMN can_create_share INTEGER NOT NULL DEFAULT 0`},
		{"users", "can_manage_own_files", `ALTER TABLE users ADD COLUMN can_manage_own_files INTEGER NOT NULL DEFAULT 1`},
		{"users", "max_file_size_mb", `ALTER TABLE users ADD COLUMN max_file_size_mb INTEGER NOT NULL DEFAULT 0`},
		{"users", "daily_upload_limit_mb", `ALTER TABLE users ADD COLUMN daily_upload_limit_mb INTEGER NOT NULL DEFAULT 0`},
		{"shares", "require_file_password", `ALTER TABLE shares ADD COLUMN require_file_password INTEGER NOT NULL DEFAULT 0`},
		{"shares", "file_password_hash", `ALTER TABLE shares ADD COLUMN file_password_hash TEXT`},
		{"shares", "expires_at", `ALTER TABLE shares ADD COLUMN expires_at TEXT`},
		{"shares", "max_downloads", `ALTER TABLE shares ADD COLUMN max_downloads INTEGER NOT NULL DEFAULT 0`},
		{"shares", "download_count", `ALTER TABLE shares ADD COLUMN download_count INTEGER NOT NULL DEFAULT 0`},
	}
	for _, column := range columns {
		ok, err := a.hasColumn(column.table, column.column)
		if err != nil {
			return err
		}
		if !ok {
			if _, err := a.db.Exec(column.sql); err != nil {
				return err
			}
		}
	}

	return a.seedDefaultRoles()
}

func (a *app) hasColumn(tableName, columnName string) (bool, error) {
	rows, err := a.db.Query(`PRAGMA table_info(` + tableName + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == columnName {
			return true, nil
		}
	}
	return false, nil
}

func (a *app) seedDefaultRoles() error {
	roles := []roleTemplate{
		{ID: adminRole, Name: "Admin", Description: "System admin", IsSystem: true, CanUpload: true, CanDownload: true},
		{ID: userRole, Name: "User", Description: "Upload and download", IsSystem: true, CanUpload: true, CanDownload: true},
		{ID: "upload_only", Name: "Upload Only", Description: "Upload only", IsSystem: true, CanUpload: true, CanDownload: false},
		{ID: "download_only", Name: "Download Only", Description: "Download only", IsSystem: true, CanUpload: false, CanDownload: true},
	}

	for _, role := range roles {
		_, err := a.db.Exec(`INSERT OR IGNORE INTO roles (id, name, description, is_system, can_upload, can_download, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
			role.ID, role.Name, role.Description, boolInt(role.IsSystem), boolInt(role.CanUpload), boolInt(role.CanDownload), nowText())
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *app) listRoles(c *gin.Context) {
	rows, err := a.db.Query(`SELECT id, name, description, is_system, can_upload, can_download, created_at FROM roles ORDER BY is_system DESC, name ASC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer rows.Close()

	var roles []gin.H
	for rows.Next() {
		var id, name, description, createdAt string
		var isSystem, canUpload, canDownload int
		_ = rows.Scan(&id, &name, &description, &isSystem, &canUpload, &canDownload, &createdAt)
		roles = append(roles, gin.H{
			"id":          id,
			"name":        name,
			"description": description,
			"isSystem":    isSystem == 1,
			"canUpload":   canUpload == 1,
			"canDownload": canDownload == 1,
			"createdAt":   createdAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

func (a *app) createRole(c *gin.Context) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		CanUpload   bool   `json:"canUpload"`
		CanDownload bool   `json:"canDownload"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "role name is required"})
		return
	}
	id := uuid.NewString()
	_, err := a.db.Exec(`INSERT INTO roles (id, name, description, is_system, can_upload, can_download, created_at)
	VALUES (?, ?, ?, 0, ?, ?, ?)`, id, req.Name, req.Description, boolInt(req.CanUpload), boolInt(req.CanDownload), nowText())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "role create failed"})
		return
	}
	a.writeAudit("system_config_changed", mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), "create role "+req.Name)
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (a *app) updateRole(c *gin.Context) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		CanUpload   bool   `json:"canUpload"`
		CanDownload bool   `json:"canDownload"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "role name is required"})
		return
	}
	_, err := a.db.Exec(`UPDATE roles SET name = ?, description = ?, can_upload = ?, can_download = ? WHERE id = ?`,
		req.Name, req.Description, boolInt(req.CanUpload), boolInt(req.CanDownload), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	a.writeAudit("system_config_changed", mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), "update role "+c.Param("id"))
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (a *app) deleteRole(c *gin.Context) {
	var isSystem int
	if err := a.db.QueryRow(`SELECT is_system FROM roles WHERE id = ?`, c.Param("id")).Scan(&isSystem); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "role not found"})
		return
	}
	if isSystem == 1 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "system role cannot be deleted"})
		return
	}
	_, err := a.db.Exec(`DELETE FROM roles WHERE id = ?`, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	a.writeAudit("system_config_changed", mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), "delete role "+c.Param("id"))
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (a *app) getUserBindings(c *gin.Context) {
	binding, err := a.loadUserBindings(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, binding)
}

func (a *app) updateUserBindings(c *gin.Context) {
	var req userBinding
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid payload"})
		return
	}
	if err := a.saveUserBindings(c.Param("id"), req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	a.writeAudit("system_config_changed", mustAuth(c).UserID, c.Param("id"), c.GetString("device_id"), clientIP(c), "update user bindings")
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (a *app) loadUserBindings(userID string) (*userBinding, error) {
	binding := &userBinding{
		AllowedIPs:       []string{},
		AllowedDeviceIDs: []string{},
	}
	ipRows, err := a.db.Query(`SELECT ip_value FROM user_allowed_ips WHERE user_id = ? ORDER BY ip_value`, userID)
	if err != nil {
		return nil, err
	}
	defer ipRows.Close()
	for ipRows.Next() {
		var ipValue string
		_ = ipRows.Scan(&ipValue)
		binding.AllowedIPs = append(binding.AllowedIPs, ipValue)
	}

	deviceRows, err := a.db.Query(`SELECT device_id FROM user_allowed_devices WHERE user_id = ? ORDER BY device_id`, userID)
	if err != nil {
		return nil, err
	}
	defer deviceRows.Close()
	for deviceRows.Next() {
		var deviceID string
		_ = deviceRows.Scan(&deviceID)
		binding.AllowedDeviceIDs = append(binding.AllowedDeviceIDs, deviceID)
	}
	return binding, nil
}

func (a *app) saveUserBindings(userID string, binding userBinding) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM user_allowed_ips WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, ipValue := range uniqueStrings(binding.AllowedIPs) {
		ipValue = strings.TrimSpace(ipValue)
		if ipValue == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO user_allowed_ips (user_id, ip_value) VALUES (?, ?)`, userID, ipValue); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`DELETE FROM user_allowed_devices WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, deviceID := range uniqueStrings(binding.AllowedDeviceIDs) {
		deviceID = strings.TrimSpace(deviceID)
		if deviceID == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO user_allowed_devices (user_id, device_id) VALUES (?, ?)`, userID, deviceID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (a *app) userAllowsIP(userID, ip string) (bool, error) {
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_allowed_ips WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return false, err
	}
	if count == 0 {
		return true, nil
	}
	var allowed int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM user_allowed_ips WHERE user_id = ? AND ip_value = ?`, userID, ip).Scan(&allowed)
	return allowed > 0, err
}

func (a *app) userAllowsDevice(userID, deviceID string) (bool, error) {
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_allowed_devices WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return false, err
	}
	if count == 0 {
		return true, nil
	}
	var allowed int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM user_allowed_devices WHERE user_id = ? AND device_id = ?`, userID, deviceID).Scan(&allowed)
	return allowed > 0, err
}

func (a *app) exportUsers(c *gin.Context) {
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "csv")))
	roleID := strings.TrimSpace(c.Query("roleId"))
	status := strings.TrimSpace(c.Query("status"))

	query := `SELECT username, display_name, role_id, status, can_upload, can_download, expires_at, last_login_at, created_at, remark FROM users WHERE 1=1`
	args := []any{}
	if roleID != "" {
		query += ` AND role_id = ?`
		args = append(args, roleID)
	}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := a.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer rows.Close()

	header := []string{"username", "display_name", "role_id", "status", "can_upload", "can_download", "expires_at", "last_login_at", "created_at", "remark"}
	records := [][]string{header}
	for rows.Next() {
		var username, displayName, roleValue, statusValue, createdAt, remark string
		var expiresAt, lastLoginAt sql.NullString
		var canUpload, canDownload int
		_ = rows.Scan(&username, &displayName, &roleValue, &statusValue, &canUpload, &canDownload, &expiresAt, &lastLoginAt, &createdAt, &remark)
		records = append(records, []string{
			username,
			displayName,
			roleValue,
			statusValue,
			strconv.FormatBool(canUpload == 1),
			strconv.FormatBool(canDownload == 1),
			expiresAt.String,
			lastLoginAt.String,
			createdAt,
			remark,
		})
	}

	if format == "xlsx" {
		file := excelize.NewFile()
		sheet := file.GetSheetName(0)
		for rowIndex, row := range records {
			for colIndex, value := range row {
				cell, _ := excelize.CoordinatesToCellName(colIndex+1, rowIndex+1)
				_ = file.SetCellValue(sheet, cell, value)
			}
		}
		var buffer bytes.Buffer
		if err := file.Write(&buffer); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}
		c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		c.Header("Content-Disposition", `attachment; filename="users.xlsx"`)
		_, _ = c.Writer.Write(buffer.Bytes())
	} else {
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", `attachment; filename="users.csv"`)
		writer := csv.NewWriter(c.Writer)
		_ = writer.WriteAll(records)
		writer.Flush()
	}

	a.writeAudit("users_exported", mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), "export users "+format)
}

func (a *app) importUsers(c *gin.Context) {
	conflictStrategy := strings.TrimSpace(c.DefaultPostForm("conflictStrategy", "skip"))
	defaultRoleID := strings.TrimSpace(c.DefaultPostForm("defaultRoleId", userRole))
	defaultStatus := strings.TrimSpace(c.DefaultPostForm("defaultStatus", "active"))
	if defaultStatus == "" {
		defaultStatus = "active"
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "file is required"})
		return
	}
	rows, err := a.readImportRows(fileHeader, defaultRoleID, defaultStatus)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if len(rows) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "no rows found"})
		return
	}

	imported := 0
	skipped := 0
	errorsList := []string{}
	for _, row := range rows {
		exists, userID, err := a.findUserByUsername(row.Username)
		if err != nil {
			errorsList = append(errorsList, row.Username+": lookup failed")
			continue
		}
		if exists {
			switch conflictStrategy {
			case "skip":
				skipped++
				continue
			case "stop":
				c.JSON(http.StatusBadRequest, gin.H{"message": "user exists: " + row.Username})
				return
			case "update", "overwrite":
				if err := a.updateImportedUser(userID, row); err != nil {
					errorsList = append(errorsList, row.Username+": update failed")
					continue
				}
				imported++
				continue
			default:
				skipped++
				continue
			}
		}
		if err := a.insertImportedUser(row); err != nil {
			errorsList = append(errorsList, row.Username+": create failed")
			continue
		}
		imported++
	}

	a.writeAudit("users_imported", mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), fmt.Sprintf("import users count=%d skipped=%d", imported, skipped))
	c.JSON(http.StatusOK, gin.H{"imported": imported, "skipped": skipped, "errors": errorsList})
}

func (a *app) readImportRows(fileHeader *multipart.FileHeader, defaultRoleID, defaultStatus string) ([]importedUserRow, error) {
	file, err := fileHeader.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
	switch ext {
	case ".csv":
		reader := csv.NewReader(file)
		return parseImportCSV(reader, defaultRoleID, defaultStatus)
	case ".xlsx":
		content, err := io.ReadAll(file)
		if err != nil {
			return nil, err
		}
		xlsx, err := excelize.OpenReader(bytes.NewReader(content))
		if err != nil {
			return nil, err
		}
		sheet := xlsx.GetSheetName(0)
		rows, err := xlsx.GetRows(sheet)
		if err != nil {
			return nil, err
		}
		return parseImportGrid(rows, defaultRoleID, defaultStatus)
	default:
		return nil, errors.New("only csv and xlsx are supported")
	}
}

func parseImportCSV(reader *csv.Reader, defaultRoleID, defaultStatus string) ([]importedUserRow, error) {
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	return parseImportGrid(records, defaultRoleID, defaultStatus)
}

func parseImportGrid(records [][]string, defaultRoleID, defaultStatus string) ([]importedUserRow, error) {
	if len(records) < 2 {
		return nil, nil
	}
	indexes := map[string]int{}
	for index, name := range records[0] {
		indexes[strings.ToLower(strings.TrimSpace(name))] = index
	}

	get := func(row []string, key string) string {
		index, ok := indexes[key]
		if !ok || index >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[index])
	}

	result := []importedUserRow{}
	for _, row := range records[1:] {
		username := get(row, "username")
		if username == "" {
			continue
		}
		canUpload := parseOptionalBool(get(row, "can_upload"))
		canDownload := parseOptionalBool(get(row, "can_download"))
		roleID := get(row, "role")
		if roleID == "" {
			roleID = get(row, "role_id")
		}
		if roleID == "" {
			roleID = defaultRoleID
		}
		statusValue := get(row, "status")
		if statusValue == "" {
			statusValue = defaultStatus
		}
		result = append(result, importedUserRow{
			Username:    username,
			DisplayName: defaultString(get(row, "display_name"), username),
			Password:    get(row, "password"),
			RoleID:      roleID,
			Status:      statusValue,
			ExpiresAt:   get(row, "expires_at"),
			CanUpload:   canUpload,
			CanDownload: canDownload,
			Remark:      get(row, "remark"),
		})
	}
	return result, nil
}

func parseOptionalBool(value string) *bool {
	if value == "" {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y":
		v := true
		return &v
	case "false", "0", "no", "n":
		v := false
		return &v
	default:
		return nil
	}
}

func (a *app) findUserByUsername(username string) (bool, string, error) {
	var userID string
	err := a.db.QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&userID)
	if err == sql.ErrNoRows {
		return false, "", nil
	}
	return err == nil, userID, err
}

func (a *app) resolveRolePermissions(roleID string) (bool, bool, error) {
	var canUpload, canDownload int
	err := a.db.QueryRow(`SELECT can_upload, can_download FROM roles WHERE id = ?`, roleID).Scan(&canUpload, &canDownload)
	if err == sql.ErrNoRows {
		return true, true, nil
	}
	return canUpload == 1, canDownload == 1, err
}

func (a *app) insertImportedUser(row importedUserRow) error {
	password := row.Password
	if password == "" {
		password = randomPassword()
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	canUpload, canDownload, err := a.resolveRolePermissions(row.RoleID)
	if err != nil {
		return err
	}
	if row.CanUpload != nil {
		canUpload = *row.CanUpload
	}
	if row.CanDownload != nil {
		canDownload = *row.CanDownload
	}
	_, err = a.db.Exec(`INSERT INTO users (id, username, display_name, password_hash, role_id, status, can_upload, can_download, must_change_password, created_at, expires_at, remark)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, NULLIF(?, ''), ?)`,
		uuid.NewString(), row.Username, row.DisplayName, string(hash), row.RoleID, row.Status, boolInt(canUpload), boolInt(canDownload), nowText(), row.ExpiresAt, row.Remark)
	return err
}

func (a *app) updateImportedUser(userID string, row importedUserRow) error {
	canUpload, canDownload, err := a.resolveRolePermissions(row.RoleID)
	if err != nil {
		return err
	}
	if row.CanUpload != nil {
		canUpload = *row.CanUpload
	}
	if row.CanDownload != nil {
		canDownload = *row.CanDownload
	}
	query := `UPDATE users SET display_name = ?, role_id = ?, status = ?, can_upload = ?, can_download = ?, expires_at = NULLIF(?, ''), remark = ?`
	args := []any{row.DisplayName, row.RoleID, row.Status, boolInt(canUpload), boolInt(canDownload), row.ExpiresAt, row.Remark}
	if row.Password != "" {
		hash, _ := bcrypt.GenerateFromPassword([]byte(row.Password), bcrypt.DefaultCost)
		query += `, password_hash = ?`
		args = append(args, string(hash))
	}
	query += ` WHERE id = ?`
	args = append(args, userID)
	_, err = a.db.Exec(query, args...)
	return err
}

func (a *app) loadShareDetails(shareID string) (*shareDetails, error) {
	var details shareDetails
	var enabled, requirePassword int
	err := a.db.QueryRow(`SELECT s.id, s.file_id, s.visibility, s.is_enabled, s.require_file_password, COALESCE(s.file_password_hash, ''), COALESCE(s.expires_at, ''), COALESCE(s.max_downloads, 0), COALESCE(s.download_count, 0), f.real_path, f.original_name, f.size, s.created_at
		FROM shares s
		JOIN files f ON f.id = s.file_id
		WHERE s.id = ? AND COALESCE(f.is_deleted, 0) = 0`, shareID).
		Scan(&details.ID, &details.FileID, &details.Visibility, &enabled, &requirePassword, &details.FilePasswordHash, &details.ExpiresAt, &details.MaxDownloads, &details.DownloadCount, &details.FilePath, &details.FileName, &details.FileSize, &details.CreatedAt)
	if err != nil {
		return nil, err
	}
	details.IsEnabled = enabled == 1
	details.RequireFilePassword = requirePassword == 1
	return &details, nil
}

func (a *app) shareVisibleToUser(details *shareDetails, ctx *authContext, password string) (bool, string) {
	if !details.IsEnabled {
		return false, "share disabled"
	}
	if ctx.RoleID == adminRole {
		return true, ""
	}
	if details.ExpiresAt != "" && isExpired(details.ExpiresAt) {
		return false, "share expired"
	}
	if details.MaxDownloads > 0 && details.DownloadCount >= details.MaxDownloads {
		return false, "share exhausted"
	}

	switch details.Visibility {
	case "all_users":
		return true, ""
	case "specified_users":
		var allowed int
		_ = a.db.QueryRow(`SELECT COUNT(*) FROM share_allowed_users WHERE share_id = ? AND user_id = ?`, details.ID, ctx.UserID).Scan(&allowed)
		if allowed > 0 {
			return true, ""
		}
	case "specified_roles":
		var allowed int
		_ = a.db.QueryRow(`SELECT COUNT(*) FROM share_allowed_roles WHERE share_id = ? AND role_id = ?`, details.ID, ctx.RoleID).Scan(&allowed)
		if allowed > 0 {
			return true, ""
		}
	case "specified_devices":
		var allowed int
		_ = a.db.QueryRow(`SELECT COUNT(*) FROM share_allowed_devices WHERE share_id = ? AND device_id = ?`, details.ID, ctx.DeviceID).Scan(&allowed)
		if allowed > 0 {
			return true, ""
		}
	case "specified_ips":
		var allowed int
		_ = a.db.QueryRow(`SELECT COUNT(*) FROM share_allowed_ips WHERE share_id = ? AND ip_value = ?`, details.ID, ctx.RemoteIP).Scan(&allowed)
		if allowed > 0 {
			return true, ""
		}
	}
	return false, "not allowed"
}

func (a *app) verifySharePassword(details *shareDetails, password string) bool {
	if !details.RequireFilePassword {
		return true
	}
	if password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(details.FilePasswordHash), []byte(password)) == nil
}

func (a *app) upsertShareRelations(shareID string, values []string, table, column string) error {
	if _, err := a.db.Exec(`DELETE FROM `+table+` WHERE share_id = ?`, shareID); err != nil {
		return err
	}
	for _, value := range uniqueStrings(values) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, err := a.db.Exec(`INSERT INTO `+table+` (share_id, `+column+`) VALUES (?, ?)`, shareID, value); err != nil {
			return err
		}
	}
	return nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		key := strings.TrimSpace(value)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, key)
	}
	return result
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
