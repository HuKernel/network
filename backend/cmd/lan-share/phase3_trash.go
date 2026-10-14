package main

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (a *app) deleteManagedFile(c *gin.Context) {
	ctx := mustAuth(c)
	var req struct {
		DeleteType string `json:"deleteType"`
		Reason     string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)
	deleteType := strings.TrimSpace(req.DeleteType)
	if deleteType == "" {
		deleteType = "soft"
	}
	var originalName, realPath string
	var size int64
	err := a.db.QueryRow(`SELECT original_name, real_path, size FROM files WHERE id = ? AND COALESCE(is_deleted, 0) = 0`, c.Param("id")).Scan(&originalName, &realPath, &size)
	if err == sql.ErrNoRows {
		a.writeAudit("delete_failed", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "file not found "+c.Param("id"))
		c.JSON(http.StatusNotFound, gin.H{"message": "file not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if deleteType == "hard" {
		if err := os.Remove(realPath); err != nil && !os.IsNotExist(err) {
			a.writeAudit("delete_failed", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}
		_, _ = a.db.Exec(`DELETE FROM files WHERE id = ?`, c.Param("id"))
		_, _ = a.db.Exec(`DELETE FROM shares WHERE file_id = ?`, c.Param("id"))
		a.writeAudit("file_hard_deleted", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "hard delete "+originalName)
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
		return
	}
	trashPath, err := a.moveToTrash(realPath, originalName)
	if err != nil {
		a.writeAudit("delete_failed", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	trashID := uuid.NewString()
	_, _ = a.db.Exec(`INSERT INTO trash_items (id, original_item_id, item_type, original_path, trash_path, original_name, size, deleted_by, deleted_at, delete_reason, restore_status)
		VALUES (?, ?, 'file', ?, ?, ?, ?, ?, ?, ?, 'restorable')`,
		trashID, c.Param("id"), realPath, trashPath, originalName, size, ctx.UserID, nowText(), req.Reason)
	_, _ = a.db.Exec(`UPDATE files SET is_deleted = 1, deleted_at = ?, deleted_by = ?, trash_path = ? WHERE id = ?`, nowText(), ctx.UserID, trashPath, c.Param("id"))
	a.writeAudit("file_deleted", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "soft delete "+originalName)
	c.JSON(http.StatusOK, gin.H{"message": "ok", "trashId": trashID})
}

func (a *app) listTrashItems(c *gin.Context) {
	rows, err := a.db.Query(`SELECT id, item_type, original_path, trash_path, original_name, size, deleted_by, deleted_at, COALESCE(delete_reason, ''), restore_status
		FROM trash_items ORDER BY deleted_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id, itemType, originalPath, trashPath, originalName, deletedBy, deletedAt, deleteReason, restoreStatus string
		var size int64
		_ = rows.Scan(&id, &itemType, &originalPath, &trashPath, &originalName, &size, &deletedBy, &deletedAt, &deleteReason, &restoreStatus)
		items = append(items, gin.H{"id": id, "itemType": itemType, "originalPath": originalPath, "trashPath": trashPath, "originalName": originalName, "size": size, "deletedBy": deletedBy, "deletedAt": deletedAt, "deleteReason": deleteReason, "restoreStatus": restoreStatus})
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *app) restoreTrashItem(c *gin.Context) {
	ctx := mustAuth(c)
	trash, err := a.loadTrashItem(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "trash item not found"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(trash.OriginalPath), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if err := os.Rename(trash.TrashPath, trash.OriginalPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if strings.Contains(trash.OriginalItemID, ":") {
		a.writeAudit("file_restored", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "restore shared folder item "+trash.OriginalName)
	} else {
		_, _ = a.db.Exec(`UPDATE files SET is_deleted = 0, deleted_at = NULL, deleted_by = NULL, trash_path = NULL WHERE id = ?`, trash.OriginalItemID)
		a.writeAudit("file_restored", ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "restore file "+trash.OriginalName)
	}
	_, _ = a.db.Exec(`DELETE FROM trash_items WHERE id = ?`, c.Param("id"))
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (a *app) purgeTrashItem(c *gin.Context) {
	ctx := mustAuth(c)
	trash, err := a.loadTrashItem(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "trash item not found"})
		return
	}
	if trash.ItemType == "folder" {
		err = os.RemoveAll(trash.TrashPath)
	} else {
		err = os.Remove(trash.TrashPath)
	}
	if err != nil && !os.IsNotExist(err) {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if !strings.Contains(trash.OriginalItemID, ":") {
		_, _ = a.db.Exec(`DELETE FROM files WHERE id = ? AND COALESCE(is_deleted, 0) = 1`, trash.OriginalItemID)
		_, _ = a.db.Exec(`DELETE FROM shares WHERE file_id = ?`, trash.OriginalItemID)
	}
	_, _ = a.db.Exec(`DELETE FROM trash_items WHERE id = ?`, c.Param("id"))
	a.writeAudit(folderDeleteEvent(trash.ItemType == "folder", true), ctx.UserID, "", ctx.DeviceID, ctx.RemoteIP, "purge trash "+trash.OriginalName)
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

type trashItemRecord struct {
	ID             string
	OriginalItemID string
	ItemType       string
	OriginalPath   string
	TrashPath      string
	OriginalName   string
}

func (a *app) loadTrashItem(id string) (*trashItemRecord, error) {
	item := &trashItemRecord{}
	err := a.db.QueryRow(`SELECT id, original_item_id, item_type, original_path, trash_path, original_name FROM trash_items WHERE id = ?`, id).
		Scan(&item.ID, &item.OriginalItemID, &item.ItemType, &item.OriginalPath, &item.TrashPath, &item.OriginalName)
	if err != nil {
		return nil, err
	}
	return item, nil
}
