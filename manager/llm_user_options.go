package manager

import (
	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
)

// Selection uses local ownership only. Credential retrieval still validates
// the selected key against Resources before returning an LLM credential.
func (m *Manager) listLLMUserOptions(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	var users []schema.User
	var keys []schema.AccessKey
	db := m.wdb.Db.WithContext(c.Request.Context())
	if err := db.Where("status = ?", "active").Order("name asc").Find(&users).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if err := db.Select("user_id", "resource_key_id").Where("status IN ?", []string{"available", "assigned"}).Order("created_at desc").Find(&keys).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	byOwner := map[string][]gin.H{}
	for _, key := range keys {
		if key.ResourceKeyID != "" {
			byOwner[key.UserID] = append(byOwner[key.UserID], gin.H{"id": key.ResourceKeyID, "status": "active"})
		}
	}
	items := make([]gin.H, 0, len(users))
	for _, user := range users {
		owned := byOwner[user.ID]
		if owned == nil {
			owned = []gin.H{}
		}
		items = append(items, gin.H{"id": user.ID, "name": user.Name, "accessKeys": owned})
	}
	c.JSON(200, gin.H{"items": items})
}
