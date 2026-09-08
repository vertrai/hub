package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func llmStringList(raw string) []string {
	values := []string{}
	_ = json.Unmarshal([]byte(raw), &values)
	return values
}
func llmEncodeList(values []string) string {
	if values == nil {
		values = []string{}
	}
	raw, _ := json.Marshal(values)
	return string(raw)
}
func hasLLMModel(values []string, id string) bool {
	for _, v := range values {
		if v == id {
			return true
		}
	}
	return false
}

func (m *Manager) resolveLLMModel(id string) (schema.LLMProvider, string, error) {
	var route schema.LLMRoute
	var p schema.LLMProvider
	provider, model, canonical := strings.Cut(id, "/")
	if !canonical {
		if err := m.wdb.Db.First(&route, "id = ?", id).Error; err != nil {
			return p, "", err
		}
		provider, model = route.ProviderID, route.UpstreamModel
	}
	if err := m.wdb.Db.First(&p, "id = ? AND enabled = ?", provider, true).Error; err != nil {
		return p, "", err
	}
	if !hasLLMModel(llmStringList(p.Models), model) {
		return p, "", gorm.ErrRecordNotFound
	}
	return p, model, nil
}
func (m *Manager) availableLLMModels() ([]schema.LLMRoute, error) {
	var routes []schema.LLMRoute
	if err := m.wdb.Db.Order("id").Find(&routes).Error; err != nil {
		return nil, err
	}
	var providers []schema.LLMProvider
	if err := m.wdb.Db.Where("enabled = ?", true).Find(&providers).Error; err != nil {
		return nil, err
	}
	byID := map[string]schema.LLMProvider{}
	for _, p := range providers {
		byID[p.ID] = p
	}
	result := []schema.LLMRoute{}
	for _, r := range routes {
		p, ok := byID[r.ProviderID]
		if ok && hasLLMModel(llmStringList(p.Models), r.UpstreamModel) {
			result = append(result, r)
		}
	}
	return result, nil
}
func (m *Manager) validateLLMPolicy(allowed []string, defaultModel string) error {
	if len(allowed) > 200 {
		return errors.New("最多允许 200 个模型")
	}
	seen := map[string]bool{}
	for _, id := range allowed {
		if id == "" || id == "hub-chat" || strings.Contains(id, "/") || seen[id] {
			return errors.New("模型不能为空、重复或使用保留名称 hub-chat")
		}
		seen[id] = true
		if _, _, err := m.resolveLLMModel(id); err != nil {
			return errors.New("模型不存在或上游已停用：" + id)
		}
	}
	if defaultModel != "" && !seen[defaultModel] {
		return errors.New("默认模型必须在允许列表中")
	}
	if len(allowed) > 0 && defaultModel == "" {
		return errors.New("请选择默认模型")
	}
	return nil
}
func (m *Manager) adminLLMRoutes(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "database unavailable"})
		return
	}
	if c.Request.Method == http.MethodGet {
		var routes []schema.LLMRoute
		if err := m.wdb.Db.Order("id").Find(&routes).Error; err != nil {
			c.JSON(500, gin.H{"error": "路由查询失败"})
			return
		}
		available, err := m.availableLLMModels()
		if err != nil {
			c.JSON(500, gin.H{"error": "模型查询失败"})
			return
		}
		c.JSON(200, gin.H{"items": routes, "availableModels": available})
		return
	}
	var route schema.LLMRoute
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if c.ShouldBindJSON(&route) != nil {
		c.JSON(400, gin.H{"error": "invalid route"})
		return
	}
	route.ID = strings.TrimSpace(route.ID)
	if route.ID == "" || route.ID == "hub-chat" || len(route.ID) > 200 || strings.ContainsAny(route.ID, "/\n\r") {
		c.JSON(400, gin.H{"error": "对外模型名无效，hub-chat 是保留名称"})
		return
	}
	m.llmMu.Lock()
	defer m.llmMu.Unlock()
	if _, _, err := m.resolveLLMModel(route.ProviderID + "/" + route.UpstreamModel); err != nil {
		c.JSON(400, gin.H{"error": "请选择已启用上游的模型"})
		return
	}
	if route.Name == "" {
		route.Name = route.ID
	}
	if err := m.wdb.Db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"name", "provider_id", "upstream_model", "updated_at"})}).Create(&route).Error; err != nil {
		c.JSON(500, gin.H{"error": "路由保存失败"})
		return
	}
	c.JSON(200, gin.H{"saved": true})
}

func (m *Manager) deleteLLMRoute(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "database unavailable"})
		return
	}
	m.llmMu.Lock()
	defer m.llmMu.Unlock()
	if err := m.wdb.Db.Delete(&schema.LLMRoute{}, "id = ?", c.Param("id")).Error; err != nil {
		c.JSON(500, gin.H{"error": "路由删除失败"})
		return
	}
	c.JSON(200, gin.H{"deleted": true})
}
