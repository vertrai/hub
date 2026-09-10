package manager

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func seedAgentCatalog(db *gorm.DB) error {
	entries := []schema.AgentCatalogEntry{
		{ID: miniProgramTemplateTax, Name: "票账助手", LogoURL: "/assets/agents/jianbu-tax.png", Kicker: "来自简簿的票账智能助手", Intro: "整理票据、核对账目、归集材料，让日常票账整理更有条理。", Summary: "可以帮你整理票据信息、核对账目记录、归集相关材料。创建并连接微信后，就可以和助手对话。", Capabilities: []string{"票据整理", "账目核对", "材料归集"}, LoginCopy: "登录后即可创建你的专属票账助手。", CapabilityCopy: "准备票账助手能力", CreationDetail: "正在创建票账助手，完成后即可设置微信连接", CTALabel: "创建票账助手", Published: true, SortOrder: 10},
		{ID: miniProgramTemplateMicAI, Name: "MC 助手", LogoURL: "/assets/agents/micai-agent.png", Kicker: "你的《我的世界》建造伙伴", Intro: "我叫 Mic，可以陪你探索世界、规划任务，一起协作建造。", Summary: "你可以在微信里告诉我想建什么，连接 Xbox 账号和游戏世界后，我就能进入你的世界，帮你探索、规划和建造。", Capabilities: []string{"世界探索", "建造规划", "协作建造"}, LoginCopy: "登录后即可创建你的专属 MC 助手。", CapabilityCopy: "准备 MC 助手能力", CreationDetail: "正在准备世界探索、建造规划和协作建造能力，请稍候", CTALabel: "创建 MC 助手", CompatibilityNote: "适用于《我的世界》基岩版，需完成 Xbox 和游戏世界连接", Published: true, SortOrder: 20},
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&entries).Error
}

var catalogID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func validateCatalogEntry(a schema.AgentCatalogEntry) error {
	if !catalogID.MatchString(a.ID) {
		return errors.New("助手 ID 仅支持小写字母、数字和连字符，最多 64 位")
	}
	if strings.TrimSpace(a.Name) == "" || utf8.RuneCountInString(a.Name) > 24 {
		return errors.New("助手名称必填且最多 24 字")
	}
	if len(a.Capabilities) < 1 || len(a.Capabilities) > 8 {
		return errors.New("请填写 1 至 8 项能力")
	}
	for _, v := range a.Capabilities {
		if strings.TrimSpace(v) == "" || utf8.RuneCountInString(v) > 30 {
			return errors.New("每项能力需填写 1 至 30 字")
		}
	}
	for _, v := range []string{a.Intro, a.Summary, a.Kicker, a.LoginCopy, a.CapabilityCopy, a.CreationDetail, a.CTALabel, a.CompatibilityNote} {
		if utf8.RuneCountInString(v) > 2000 {
			return errors.New("文案长度不能超过 2000 字")
		}
	}
	if strings.TrimSpace(a.Intro) == "" {
		return errors.New("请填写助手介绍")
	}
	if a.LogoURL != "/assets/agents/jianbu-tax.png" && a.LogoURL != "/assets/agents/micai-agent.png" {
		u, err := url.Parse(a.LogoURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return errors.New("图标请使用 HTTPS 图片地址")
		}
	}
	if len(a.Module) > 512 || strings.ContainsAny(a.Module, "\r\n") {
		return errors.New("无效的模块标识")
	}
	if strings.TrimSpace(a.Module) == "" && a.ID != miniProgramTemplateTax && a.ID != miniProgramTemplateMicAI {
		return errors.New("请配置助手的 Hymatrix 模块")
	}
	return nil
}
func publicCatalogEntry(a schema.AgentCatalogEntry) gin.H {
	return gin.H{"id": a.ID, "name": a.Name, "logoUrl": a.LogoURL, "kicker": a.Kicker, "intro": a.Intro, "summary": a.Summary, "capabilities": a.Capabilities, "loginCopy": a.LoginCopy, "capabilityCopy": a.CapabilityCopy, "creationDetail": a.CreationDetail, "ctaLabel": a.CTALabel, "compatibilityNote": a.CompatibilityNote, "published": a.Published}
}
func (m *Manager) catalogDB(c *gin.Context) bool {
	if m.wdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"})
		return false
	}
	return true
}
func (m *Manager) listAgentCatalog(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	var entries []schema.AgentCatalogEntry
	if err := m.wdb.Db.Where("published = ?", true).Order("sort_order, id").Find(&entries).Error; err != nil {
		c.JSON(500, gin.H{"error": "读取助手目录失败"})
		return
	}
	result := make([]gin.H, 0, len(entries))
	for _, a := range entries {
		result = append(result, publicCatalogEntry(a))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"agents": result})
}
func (m *Manager) getAgentCatalogEntry(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	var a schema.AgentCatalogEntry
	if err := m.wdb.Db.First(&a, "id = ?", c.Param("id")).Error; err != nil {
		catalogError(c, err)
		return
	}
	if !a.Published {
		userID, ok := m.requireMiniProgramSession(c)
		if !ok {
			return
		}
		var count int64
		if err := m.wdb.Db.Model(&schema.MiniProgramAgentTask{}).Where("user_id = ? AND template = ?", userID, a.ID).Count(&count).Error; err != nil {
			catalogError(c, err)
			return
		}
		if count == 0 {
			c.JSON(404, gin.H{"error": "助手已下架"})
			return
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, publicCatalogEntry(a))
}
func (m *Manager) listMyMiniProgramAgents(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	userID, ok := m.requireMiniProgramSession(c)
	if !ok {
		return
	}
	var tasks []schema.MiniProgramAgentTask
	// Match the existing one-current-instance-per-agent contract, including failed attempts.
	err := m.wdb.Db.Raw(`SELECT DISTINCT ON (template) * FROM manager_mini_program_agent_tasks WHERE user_id = ? ORDER BY template, created_at DESC, id DESC`, userID).Scan(&tasks).Error
	if err != nil {
		catalogError(c, err)
		return
	}
	var entries []schema.AgentCatalogEntry
	if err = m.wdb.Db.Find(&entries).Error; err != nil {
		catalogError(c, err)
		return
	}
	byID := map[string]schema.AgentCatalogEntry{}
	for _, a := range entries {
		byID[a.ID] = a
	}
	result := make([]gin.H, 0, len(tasks))
	for _, t := range tasks {
		m.reconcileMiniProgramWeixinAttempt(&t)
		r := gin.H{"taskId": t.ID, "template": t.Template, "status": t.Status, "createdAt": t.CreatedAt}
		r["agentId"] = t.Template
		r["agent"] = publicCatalogEntry(byID[t.Template])
		result = append(result, r)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"tasks": result})
}
func catalogError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(404, gin.H{"error": "未找到助手"})
	} else {
		c.JSON(500, gin.H{"error": "助手目录操作失败"})
	}
}
func (m *Manager) adminAgentCatalog(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	var entries []schema.AgentCatalogEntry
	if err := m.wdb.Db.Order("sort_order, id").Find(&entries).Error; err != nil {
		catalogError(c, err)
		return
	}
	c.JSON(200, gin.H{"agents": entries})
}
func (m *Manager) adminSaveAgentCatalog(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	var a schema.AgentCatalogEntry
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32768)
	if c.ShouldBindJSON(&a) != nil {
		c.JSON(400, gin.H{"error": "无效的助手配置"})
		return
	}
	a.Module = strings.TrimSpace(a.Module)
	if err := validateCatalogEntry(a); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if c.Param("id") != "" && c.Param("id") != a.ID {
		c.JSON(400, gin.H{"error": "助手 ID 不可修改"})
		return
	}
	if a.Published && a.Module == "" && miniProgramModuleForTemplate(m.config.MiniProgram, a.ID) == "" {
		c.JSON(400, gin.H{"error": "请先配置助手模块再上架"})
		return
	}
	var err error
	if c.Request.Method == http.MethodPost {
		a.CreatedAt = time.Time{}
		a.UpdatedAt = time.Time{}
		result := m.wdb.Db.Clauses(clause.OnConflict{DoNothing: true}).Create(&a)
		err = result.Error
		if err == nil && result.RowsAffected == 0 {
			c.JSON(409, gin.H{"error": "助手 ID 已存在"})
			return
		}
	} else {
		err = m.wdb.Db.Transaction(func(tx *gorm.DB) error {
			var old schema.AgentCatalogEntry
			if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, "id = ?", a.ID).Error; e != nil {
				return e
			}
			a.CreatedAt = old.CreatedAt
			return tx.Save(&a).Error
		})
	}
	if err != nil {
		catalogError(c, err)
		return
	}
	c.JSON(200, a)
}
func (m *Manager) adminDeleteAgentCatalog(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		var a schema.AgentCatalogEntry
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&a, "id = ?", c.Param("id")).Error; err != nil {
			return err
		}
		if a.ID == miniProgramTemplateTax || a.ID == miniProgramTemplateMicAI {
			return errors.New("内置助手请使用下架，以保留旧版本兼容性")
		}
		if a.Published {
			return errors.New("请先下架助手")
		}
		var count int64
		if err := tx.Model(&schema.MiniProgramAgentTask{}).Where("template = ?", a.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("已有用户创建记录，请保留配置并使用下架")
		}
		return tx.Delete(&a).Error
	})
	if err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"deleted": true})
}
