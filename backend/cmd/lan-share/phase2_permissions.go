package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type userPermissionConfig struct {
	UserID              string `json:"userId"`
	RoleID              string `json:"roleId"`
	Status              string `json:"status"`
	CanUpload           bool   `json:"canUpload"`
	CanDownload         bool   `json:"canDownload"`
	CanViewSharedList   bool   `json:"canViewSharedList"`
	CanCreateShare      bool   `json:"canCreateShare"`
	CanManageOwnFiles   bool   `json:"canManageOwnFiles"`
	MaxFileSizeMB       int    `json:"maxFileSizeMB"`
	DailyUploadLimitMB  int    `json:"dailyUploadLimitMB"`
	ExpiresAt           string `json:"expiresAt"`
	MustChangePassword  bool   `json:"mustChangePassword"`
}

func (a *app) getUserPermissions(c *gin.Context) {
	protected, err := a.isProtectedAdminUser(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if protected {
		c.JSON(http.StatusForbidden, gin.H{"message": "管理员权限为最高级，不支持修改"})
		return
	}
	permissions, err := a.loadUserPermissionConfig(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, permissions)
}

func (a *app) updateUserPermissions(c *gin.Context) {
	var req struct {
		RoleID             string `json:"roleId"`
		Status             string `json:"status"`
		CanUpload          bool   `json:"canUpload"`
		CanDownload        bool   `json:"canDownload"`
		CanViewSharedList  bool   `json:"canViewSharedList"`
		CanCreateShare     bool   `json:"canCreateShare"`
		CanManageOwnFiles  bool   `json:"canManageOwnFiles"`
		MaxFileSizeMB      int    `json:"maxFileSizeMB"`
		DailyUploadLimitMB int    `json:"dailyUploadLimitMB"`
		ExpiresAt          string `json:"expiresAt"`
		ForceRelogin       bool   `json:"forceRelogin"`
		ForcePasswordChange bool  `json:"forcePasswordChange"`
		Reason             string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid payload"})
		return
	}
	protected, err := a.isProtectedAdminUser(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if protected {
		c.JSON(http.StatusForbidden, gin.H{"message": "管理员权限为最高级，不支持修改"})
		return
	}

	before, err := a.loadUserPermissionConfig(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if req.RoleID == "" {
		req.RoleID = before.RoleID
	}
	if req.Status == "" {
		req.Status = before.Status
	}

	_, err = a.db.Exec(`UPDATE users
		SET role_id = ?, status = ?, can_upload = ?, can_download = ?, can_view_shared_list = ?, can_create_share = ?,
			can_manage_own_files = ?, max_file_size_mb = ?, daily_upload_limit_mb = ?, expires_at = NULLIF(?, ''),
			must_change_password = ?, remark = remark
		WHERE id = ?`,
		req.RoleID,
		req.Status,
		boolInt(req.CanUpload),
		boolInt(req.CanDownload),
		boolInt(req.CanViewSharedList),
		boolInt(req.CanCreateShare),
		boolInt(req.CanManageOwnFiles),
		req.MaxFileSizeMB,
		req.DailyUploadLimitMB,
		req.ExpiresAt,
		boolInt(req.ForcePasswordChange),
		c.Param("id"),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	sensitiveChanged := before.RoleID != req.RoleID ||
		before.Status != req.Status ||
		before.CanUpload != req.CanUpload ||
		before.CanDownload != req.CanDownload
	if req.ForceRelogin || sensitiveChanged || req.Status != "active" {
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, c.Param("id"))
	}

	after, err := a.loadUserPermissionConfig(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	message := "before=" + string(beforeJSON) + " after=" + string(afterJSON)
	if strings.TrimSpace(req.Reason) != "" {
		message += " reason=" + strings.TrimSpace(req.Reason)
	}
	a.writeAudit("user_permission_updated", mustAuth(c).UserID, c.Param("id"), c.GetString("device_id"), clientIP(c), message)
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (a *app) loadUserPermissionConfig(userID string) (*userPermissionConfig, error) {
	config := &userPermissionConfig{UserID: userID}
	var canUpload, canDownload, canViewSharedList, canCreateShare, canManageOwnFiles, mustChangePassword int
	var maxFileSizeMB, dailyUploadLimitMB int
	var expiresAt sql.NullString
	err := a.db.QueryRow(`SELECT role_id, status, can_upload, can_download, can_view_shared_list, can_create_share,
		can_manage_own_files, max_file_size_mb, daily_upload_limit_mb, expires_at, must_change_password
		FROM users WHERE id = ?`, userID).
		Scan(
			&config.RoleID,
			&config.Status,
			&canUpload,
			&canDownload,
			&canViewSharedList,
			&canCreateShare,
			&canManageOwnFiles,
			&maxFileSizeMB,
			&dailyUploadLimitMB,
			&expiresAt,
			&mustChangePassword,
		)
	if err != nil {
		return nil, err
	}
	config.CanUpload = canUpload == 1
	config.CanDownload = canDownload == 1
	config.CanViewSharedList = canViewSharedList == 1
	config.CanCreateShare = canCreateShare == 1
	config.CanManageOwnFiles = canManageOwnFiles == 1
	config.MaxFileSizeMB = maxFileSizeMB
	config.DailyUploadLimitMB = dailyUploadLimitMB
	config.ExpiresAt = expiresAt.String
	config.MustChangePassword = mustChangePassword == 1
	return config, nil
}

func (a *app) currentUploadUsageBytes(userID string) int64 {
	todayStart := nowText()[:10]
	var total sql.NullInt64
	_ = a.db.QueryRow(`SELECT COALESCE(SUM(bytes_total), 0) FROM transfers
		WHERE user_id = ? AND direction = 'upload' AND substr(created_at, 1, 10) = ?`, userID, todayStart).Scan(&total)
	return total.Int64
}

func (a *app) isProtectedAdminUser(userID string) (bool, error) {
	var roleID string
	if err := a.db.QueryRow(`SELECT role_id FROM users WHERE id = ?`, userID).Scan(&roleID); err != nil {
		return false, err
	}
	return roleID == adminRole, nil
}
