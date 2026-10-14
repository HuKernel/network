package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type sharedFolderPermission struct {
	SubjectType     string `json:"subjectType"`
	SubjectID       string `json:"subjectId"`
	CanList         bool   `json:"canList"`
	CanDownload     bool   `json:"canDownload"`
	CanUpload       bool   `json:"canUpload"`
	CanUploadFolder bool   `json:"canUploadFolder"`
	CanCreateFolder bool   `json:"canCreateFolder"`
	CanDeleteOwn    bool   `json:"canDeleteOwn"`
	CanDeleteAny    bool   `json:"canDeleteAny"`
	CanRename       bool   `json:"canRename"`
	CanManage       bool   `json:"canManage"`
}

type sharedFolder struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	RootPath    string `json:"rootPath"`
	SafeRoot    string `json:"safeRootPath"`
	OwnerUserID string `json:"ownerUserId"`
	Mode        string `json:"mode"`
	IsEnabled   bool   `json:"isEnabled"`
	ExpiresAt   string `json:"expiresAt"`
	CreatedAt   string `json:"createdAt"`
}

func (a *app) initPhase3DB() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS shared_folders (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			root_path TEXT NOT NULL,
			safe_root_path TEXT NOT NULL,
			owner_user_id TEXT NOT NULL,
			mode TEXT NOT NULL,
			is_enabled INTEGER NOT NULL DEFAULT 1,
			expires_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS shared_folder_permissions (
			id TEXT PRIMARY KEY,
			folder_id TEXT NOT NULL,
			subject_type TEXT NOT NULL,
			subject_id TEXT NOT NULL,
			can_list INTEGER NOT NULL DEFAULT 0,
			can_download INTEGER NOT NULL DEFAULT 0,
			can_upload INTEGER NOT NULL DEFAULT 0,
			can_upload_folder INTEGER NOT NULL DEFAULT 0,
			can_create_folder INTEGER NOT NULL DEFAULT 0,
			can_delete_own INTEGER NOT NULL DEFAULT 0,
			can_delete_any INTEGER NOT NULL DEFAULT 0,
			can_rename INTEGER NOT NULL DEFAULT 0,
			can_manage INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS trash_items (
			id TEXT PRIMARY KEY,
			original_item_id TEXT NOT NULL,
			item_type TEXT NOT NULL,
			original_path TEXT NOT NULL,
			trash_path TEXT NOT NULL,
			original_name TEXT NOT NULL,
			size INTEGER NOT NULL DEFAULT 0,
			deleted_by TEXT NOT NULL,
			deleted_at TEXT NOT NULL,
			delete_reason TEXT,
			restore_status TEXT NOT NULL DEFAULT 'restorable'
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
		{"files", "is_deleted", `ALTER TABLE files ADD COLUMN is_deleted INTEGER NOT NULL DEFAULT 0`},
		{"files", "deleted_at", `ALTER TABLE files ADD COLUMN deleted_at TEXT`},
		{"files", "deleted_by", `ALTER TABLE files ADD COLUMN deleted_by TEXT`},
		{"files", "trash_path", `ALTER TABLE files ADD COLUMN trash_path TEXT`},
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
	return os.MkdirAll(a.trashRoot(), 0o755)
}

func (a *app) createSharedFolder(c *gin.Context) {
	var req struct {
		Name        string                   `json:"name"`
		RootPath    string                   `json:"rootPath"`
		Mode        string                   `json:"mode"`
		ExpiresAt   string                   `json:"expiresAt"`
		Permissions []sharedFolderPermission `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid payload"})
		return
	}
	if strings.TrimSpace(req.RootPath) == "" {
		req.RootPath = filepath.Join(a.dataDir, "shared-folders", safeFolderName(req.Name))
	}
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "collaboration"
	}
	if err := os.MkdirAll(req.RootPath, 0o755); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	safeRoot, err := filepath.Abs(req.RootPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	folderID := uuid.NewString()
	ctx := mustAuth(c)
	if _, err := a.db.Exec(`INSERT INTO shared_folders (id, name, root_path, safe_root_path, owner_user_id, mode, is_enabled, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 1, NULLIF(?, ''), ?, ?)`,
		folderID, req.Name, req.RootPath, safeRoot, ctx.UserID, mode, req.ExpiresAt, nowText(), nowText()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	permissions := req.Permissions
	if len(permissions) == 0 {
		permissions = []sharedFolderPermission{defaultSharedFolderPermission(mode)}
	}
	if err := a.replaceSharedFolderPermissions(folderID, permissions); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	a.writeAudit("shared_folder_created", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "create shared folder "+req.Name)
	c.JSON(http.StatusOK, gin.H{"message": "ok", "id": folderID})
}

func (a *app) listSharedFolders(c *gin.Context) {
	ctx := mustAuth(c)
	rows, err := a.db.Query(`SELECT id, name, root_path, safe_root_path, owner_user_id, mode, is_enabled, COALESCE(expires_at, ''), created_at
		FROM shared_folders ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer rows.Close()
	folders := []gin.H{}
	for rows.Next() {
		var folder sharedFolder
		var enabled int
		if err := rows.Scan(&folder.ID, &folder.Name, &folder.RootPath, &folder.SafeRoot, &folder.OwnerUserID, &folder.Mode, &enabled, &folder.ExpiresAt, &folder.CreatedAt); err != nil {
			continue
		}
		folder.IsEnabled = enabled == 1
		perm, ok := a.loadSharedFolderPermission(folder.ID, ctx)
		if !ok || !folder.IsEnabled || (folder.ExpiresAt != "" && isExpired(folder.ExpiresAt)) {
			continue
		}
		folders = append(folders, gin.H{
			"id": folder.ID, "name": folder.Name, "mode": folder.Mode, "expiresAt": folder.ExpiresAt,
			"canList": perm.CanList, "canDownload": perm.CanDownload, "canUpload": perm.CanUpload,
			"canCreateFolder": perm.CanCreateFolder, "canDeleteOwn": perm.CanDeleteOwn, "canDeleteAny": perm.CanDeleteAny,
		})
	}
	c.JSON(http.StatusOK, gin.H{"folders": folders})
}

func (a *app) listSharedFolderItems(c *gin.Context) {
	folder, perm, root, relativePath, ok := a.authorizeFolderPath(c, "list")
	if !ok {
		return
	}
	targetPath := root
	if relativePath != "" {
		targetPath = filepath.Join(root, relativePath)
	}
	entries, err := os.ReadDir(targetPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	items := []gin.H{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		childRelative := filepath.ToSlash(filepath.Join(relativePath, entry.Name()))
		itemType := "file"
		if entry.IsDir() {
			itemType = "folder"
		}
		items = append(items, gin.H{
			"name": entry.Name(), "itemType": itemType, "path": childRelative, "size": info.Size(),
			"updatedAt": info.ModTime().Format(time.RFC3339), "canDelete": perm.CanDeleteAny || perm.CanDeleteOwn,
		})
	}
	c.JSON(http.StatusOK, gin.H{"folder": gin.H{"id": folder.ID, "name": folder.Name, "mode": folder.Mode}, "items": items})
}

func (a *app) uploadSharedFolderItem(c *gin.Context) {
	_, perm, root, relativePath, ok := a.authorizeFolderPath(c, "upload")
	if !ok {
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "file is required"})
		return
	}
	defer file.Close()
	if relativePath == "" {
		relativePath = c.PostForm("relativePath")
	}
	targetPath, err := safeJoin(root, relativePath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if strings.Contains(relativePath, "/") && !perm.CanUploadFolder {
		c.JSON(http.StatusForbidden, gin.H{"message": "folder upload is not allowed"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	dst, err := os.Create(targetPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer dst.Close()
	written, err := io.Copy(dst, file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	ctx := mustAuth(c)
	a.writeTransfer("", "", ctx.UserID, ctx.DeviceID, ctx.RemoteIP, "upload", "success", written)
	a.writeAudit("shared_folder_uploaded", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "upload "+header.Filename+" to "+relativePath)
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (a *app) downloadSharedFolderItem(c *gin.Context) {
	_, _, root, relativePath, ok := a.authorizeFolderPath(c, "download")
	if !ok {
		return
	}
	targetPath, err := safeJoin(root, relativePath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	info, err := os.Stat(targetPath)
	if err != nil || info.IsDir() {
		c.JSON(http.StatusNotFound, gin.H{"message": "item not found"})
		return
	}
	ctx := mustAuth(c)
	a.writeTransfer("", "", ctx.UserID, ctx.DeviceID, ctx.RemoteIP, "download", "success", info.Size())
	a.writeAudit("shared_folder_downloaded", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "download "+relativePath)
	c.FileAttachment(targetPath, filepath.Base(targetPath))
}

func (a *app) mkdirSharedFolderItem(c *gin.Context) {
	_, perm, root, relativePath, ok := a.authorizeFolderPath(c, "mkdir")
	if !ok {
		return
	}
	if !perm.CanCreateFolder {
		c.JSON(http.StatusForbidden, gin.H{"message": "permission denied"})
		return
	}
	var req struct{ Name string `json:"name"` }
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "name is required"})
		return
	}
	targetPath, err := safeJoin(root, filepath.ToSlash(filepath.Join(relativePath, req.Name)))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	ctx := mustAuth(c)
	a.writeAudit("shared_folder_mkdir", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "mkdir "+req.Name)
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (a *app) deleteSharedFolderItem(c *gin.Context) {
	folder, perm, root, relativePath, ok := a.authorizeFolderPath(c, "delete")
	if !ok {
		return
	}
	targetPath, err := safeJoin(root, relativePath)
	if err != nil {
		a.writeAudit("delete_failed", mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), "invalid shared folder path")
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		a.writeAudit("delete_failed", mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), "shared folder item missing")
		c.JSON(http.StatusNotFound, gin.H{"message": "item not found"})
		return
	}
	if info.IsDir() && !perm.CanDeleteAny {
		c.JSON(http.StatusForbidden, gin.H{"message": "permission denied"})
		return
	}
	deleteType := strings.TrimSpace(c.Query("deleteType"))
	if deleteType == "" {
		deleteType = "soft"
	}
	if deleteType == "hard" {
		if info.IsDir() {
			err = os.RemoveAll(targetPath)
		} else {
			err = os.Remove(targetPath)
		}
		if err != nil {
			a.writeAudit("delete_failed", mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}
		a.writeAudit(folderDeleteEvent(info.IsDir(), true), mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), "hard delete "+relativePath)
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
		return
	}
	trashPath, err := a.moveToTrash(targetPath, filepath.Base(targetPath))
	if err != nil {
		a.writeAudit("delete_failed", mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	ctx := mustAuth(c)
	_, _ = a.db.Exec(`INSERT INTO trash_items (id, original_item_id, item_type, original_path, trash_path, original_name, size, deleted_by, deleted_at, delete_reason, restore_status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', 'restorable')`,
		uuid.NewString(), folder.ID+":"+filepath.ToSlash(relativePath), itemTypeFromDir(info.IsDir()), targetPath, trashPath, filepath.Base(targetPath), info.Size(), ctx.UserID, nowText())
	a.writeAudit(folderDeleteEvent(info.IsDir(), false), ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "soft delete "+relativePath)
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func defaultSharedFolderPermission(mode string) sharedFolderPermission {
	perm := sharedFolderPermission{SubjectType: "role", SubjectID: userRole}
	switch mode {
	case "readonly":
		perm.CanList, perm.CanDownload = true, true
	case "upload_box":
		perm.CanUpload, perm.CanUploadFolder = true, true
	case "collaboration":
		perm.CanList, perm.CanDownload, perm.CanUpload, perm.CanUploadFolder, perm.CanCreateFolder = true, true, true, true, true
	default:
		perm.CanList, perm.CanDownload, perm.CanUpload = true, true, true
	}
	return perm
}

func (a *app) replaceSharedFolderPermissions(folderID string, permissions []sharedFolderPermission) error {
	if _, err := a.db.Exec(`DELETE FROM shared_folder_permissions WHERE folder_id = ?`, folderID); err != nil {
		return err
	}
	for _, permission := range permissions {
		if strings.TrimSpace(permission.SubjectType) == "" || strings.TrimSpace(permission.SubjectID) == "" {
			continue
		}
		_, err := a.db.Exec(`INSERT INTO shared_folder_permissions
			(id, folder_id, subject_type, subject_id, can_list, can_download, can_upload, can_upload_folder, can_create_folder, can_delete_own, can_delete_any, can_rename, can_manage, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), folderID, permission.SubjectType, permission.SubjectID,
			boolInt(permission.CanList), boolInt(permission.CanDownload), boolInt(permission.CanUpload), boolInt(permission.CanUploadFolder),
			boolInt(permission.CanCreateFolder), boolInt(permission.CanDeleteOwn), boolInt(permission.CanDeleteAny), boolInt(permission.CanRename), boolInt(permission.CanManage), nowText())
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *app) authorizeFolderPath(c *gin.Context, action string) (*sharedFolder, sharedFolderPermission, string, string, bool) {
	ctx := mustAuth(c)
	folder, err := a.loadSharedFolder(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "shared folder not found"})
		return nil, sharedFolderPermission{}, "", "", false
	}
	perm, ok := a.loadSharedFolderPermission(folder.ID, ctx)
	if !ok || !allowsFolderAction(perm, action) {
		c.JSON(http.StatusForbidden, gin.H{"message": "permission denied"})
		return nil, sharedFolderPermission{}, "", "", false
	}
	relativePath := strings.TrimSpace(c.Query("path"))
	if relativePath == "" {
		relativePath = strings.TrimSpace(c.PostForm("path"))
	}
	return folder, perm, folder.SafeRoot, relativePath, true
}

func allowsFolderAction(perm sharedFolderPermission, action string) bool {
	switch action {
	case "list":
		return perm.CanList
	case "download":
		return perm.CanDownload
	case "upload":
		return perm.CanUpload || perm.CanUploadFolder
	case "mkdir":
		return perm.CanCreateFolder
	case "delete":
		return perm.CanDeleteAny || perm.CanDeleteOwn
	default:
		return false
	}
}

func (a *app) loadSharedFolder(folderID string) (*sharedFolder, error) {
	folder := &sharedFolder{}
	var enabled int
	err := a.db.QueryRow(`SELECT id, name, root_path, safe_root_path, owner_user_id, mode, is_enabled, COALESCE(expires_at, ''), created_at
		FROM shared_folders WHERE id = ?`, folderID).
		Scan(&folder.ID, &folder.Name, &folder.RootPath, &folder.SafeRoot, &folder.OwnerUserID, &folder.Mode, &enabled, &folder.ExpiresAt, &folder.CreatedAt)
	if err != nil {
		return nil, err
	}
	folder.IsEnabled = enabled == 1
	return folder, nil
}

func (a *app) loadSharedFolderPermission(folderID string, ctx *authContext) (sharedFolderPermission, bool) {
	if ctx.RoleID == adminRole {
		return sharedFolderPermission{CanList: true, CanDownload: true, CanUpload: true, CanUploadFolder: true, CanCreateFolder: true, CanDeleteOwn: true, CanDeleteAny: true, CanManage: true}, true
	}
	rows, err := a.db.Query(`SELECT subject_type, subject_id, can_list, can_download, can_upload, can_upload_folder, can_create_folder, can_delete_own, can_delete_any, can_rename, can_manage
		FROM shared_folder_permissions WHERE folder_id = ?`, folderID)
	if err != nil {
		return sharedFolderPermission{}, false
	}
	defer rows.Close()
	for rows.Next() {
		var perm sharedFolderPermission
		var canList, canDownload, canUpload, canUploadFolder, canCreateFolder, canDeleteOwn, canDeleteAny, canRename, canManage int
		if err := rows.Scan(&perm.SubjectType, &perm.SubjectID, &canList, &canDownload, &canUpload, &canUploadFolder, &canCreateFolder, &canDeleteOwn, &canDeleteAny, &canRename, &canManage); err != nil {
			continue
		}
		if !permissionMatches(perm.SubjectType, perm.SubjectID, ctx) {
			continue
		}
		perm.CanList = canList == 1
		perm.CanDownload = canDownload == 1
		perm.CanUpload = canUpload == 1
		perm.CanUploadFolder = canUploadFolder == 1
		perm.CanCreateFolder = canCreateFolder == 1
		perm.CanDeleteOwn = canDeleteOwn == 1
		perm.CanDeleteAny = canDeleteAny == 1
		perm.CanRename = canRename == 1
		perm.CanManage = canManage == 1
		return perm, true
	}
	return sharedFolderPermission{}, false
}

func permissionMatches(subjectType, subjectID string, ctx *authContext) bool {
	switch subjectType {
	case "user":
		return subjectID == ctx.UserID
	case "role":
		return subjectID == ctx.RoleID
	case "device":
		return subjectID == ctx.DeviceID
	case "ip":
		return subjectID == ctx.RemoteIP
	default:
		return false
	}
}

func safeJoin(root, relativePath string) (string, error) {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	normalized := strings.TrimSpace(strings.ReplaceAll(relativePath, "\\", "/"))
	normalized = strings.TrimPrefix(normalized, "/")
	if normalized == "" {
		return cleanRoot, nil
	}
	if filepath.IsAbs(normalized) || strings.Contains(normalized, ":") {
		return "", errors.New("absolute paths are not allowed")
	}
	target, err := filepath.Abs(filepath.Join(cleanRoot, filepath.FromSlash(normalized)))
	if err != nil {
		return "", err
	}
	if target != cleanRoot && !strings.HasPrefix(target, cleanRoot+string(os.PathSeparator)) {
		return "", errors.New("path escapes shared folder root")
	}
	return target, nil
}

func itemTypeFromDir(isDir bool) string {
	if isDir {
		return "folder"
	}
	return "file"
}

func safeFolderName(name string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	cleaned := strings.TrimSpace(replacer.Replace(name))
	if cleaned == "" {
		return "shared-folder"
	}
	return cleaned
}

func folderDeleteEvent(isDir, hard bool) string {
	if isDir {
		if hard {
			return "folder_hard_deleted"
		}
		return "folder_deleted"
	}
	if hard {
		return "file_hard_deleted"
	}
	return "file_deleted"
}

func (a *app) trashRoot() string {
	return filepath.Join(a.dataDir, "trash")
}

func (a *app) moveToTrash(sourcePath, name string) (string, error) {
	trashDir := filepath.Join(a.trashRoot(), nowText()[:10])
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return "", err
	}
	targetPath := filepath.Join(trashDir, uuid.NewString()+"_"+name)
	return targetPath, os.Rename(sourcePath, targetPath)
}
