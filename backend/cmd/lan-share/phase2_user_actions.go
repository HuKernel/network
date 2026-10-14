package main

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func (a *app) resetUserPassword(c *gin.Context) {
	var req struct {
		Password          string `json:"password"`
		ForceChangeOnNext bool   `json:"forceChangeOnNext"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid payload"})
		return
	}
	userID := c.Param("id")
	password := req.Password
	if password == "" {
		password = randomPassword()
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	forceChange := 1
	if !req.ForceChangeOnNext && req.Password != "" {
		forceChange = 0
	}
	if _, err := a.db.Exec(`UPDATE users SET password_hash = ?, must_change_password = ? WHERE id = ?`, string(hash), forceChange, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	_, _ = a.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	a.writeAudit("user_password_reset", mustAuth(c).UserID, userID, c.GetString("device_id"), clientIP(c), "reset user password")
	c.JSON(http.StatusOK, gin.H{"message": "ok", "initialPassword": password, "forceChangeOnNext": forceChange == 1})
}

func (a *app) batchUserAction(c *gin.Context) {
	var req struct {
		UserIDs   []string `json:"userIds"`
		Action    string   `json:"action"`
		RoleID    string   `json:"roleId"`
		ExpiresAt string   `json:"expiresAt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.UserIDs) == 0 || req.Action == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid payload"})
		return
	}
	if req.Action == "set_role" && req.RoleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "roleId is required"})
		return
	}
	results := []gin.H{}
	affected := 0
	skipped := 0
	for _, userID := range uniqueStrings(req.UserIDs) {
		username, roleID, err := a.loadUserActionTarget(userID)
		if err == sql.ErrNoRows {
			skipped++
			continue
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}
		protected := roleID == adminRole && req.Action != "reset_password"
		if protected {
			skipped++
			continue
		}
		switch req.Action {
		case "enable":
			_, err = a.db.Exec(`UPDATE users SET status = 'active' WHERE id = ?`, userID)
		case "disable":
			_, err = a.db.Exec(`UPDATE users SET status = 'disabled' WHERE id = ?`, userID)
			_, _ = a.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
		case "set_role":
			_, err = a.db.Exec(`UPDATE users SET role_id = ? WHERE id = ?`, req.RoleID, userID)
			_, _ = a.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
		case "set_expiry":
			_, err = a.db.Exec(`UPDATE users SET expires_at = NULLIF(?, '') WHERE id = ?`, req.ExpiresAt, userID)
			_, _ = a.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
		case "reset_password":
			password := randomPassword()
			hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			_, err = a.db.Exec(`UPDATE users SET password_hash = ?, must_change_password = 1 WHERE id = ?`, string(hash), userID)
			_, _ = a.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
			results = append(results, gin.H{"userId": userID, "username": username, "initialPassword": password})
		default:
			c.JSON(http.StatusBadRequest, gin.H{"message": "unsupported action"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}
		affected++
	}
	a.writeAudit("users_batch_updated", mustAuth(c).UserID, "", c.GetString("device_id"), clientIP(c), fmt.Sprintf("action=%s affected=%d skipped=%d", req.Action, affected, skipped))
	c.JSON(http.StatusOK, gin.H{"message": "ok", "affected": affected, "skipped": skipped, "passwords": results})
}

func (a *app) loadUserActionTarget(userID string) (string, string, error) {
	var username, roleID string
	err := a.db.QueryRow(`SELECT username, role_id FROM users WHERE id = ?`, userID).Scan(&username, &roleID)
	return username, roleID, err
}
