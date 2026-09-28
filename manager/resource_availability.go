package manager

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
)

func resourcePoolModel(resource string) (any, string) {
	switch resource {
	case "xbox-child":
		return &schema.XboxChild{}, "Xbox Child 游戏账户"
	case "netease":
		return &schema.NetEaseAccount{}, "网易游戏账户"
	default:
		return nil, ""
	}
}

func resourceExhaustedMessage(resource string) string {
	_, name := resourcePoolModel(resource)
	return name + "暂时用完，请稍后再试；如持续不可用，请联系官方补充资源。"
}

// Preserve existing allocation HTTP status and error text shape while giving
// callers a stable reason and recovery action independent of display language.
func resourcePoolExhausted(c *gin.Context, resource string) {
	c.JSON(http.StatusConflict, gin.H{
		"code": "RESOURCE_POOL_EXHAUSTED", "resource": resource,
		"error": resourceExhaustedMessage(resource), "action": "wait_for_restock",
	})
}

type resourceAvailabilityItem struct {
	Resource string `json:"resource"`
	Status   string `json:"status"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message,omitempty"`
}

// This public preflight reports only availability, never credentials or counts.
// It checks only requested tables and does not allocate or reserve accounts.
func (m *Manager) resourceAvailability(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	values, present := c.GetQueryArray("resources")
	var requested []string
	seen := map[string]bool{}
	valid := present
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			resource := strings.TrimSpace(part)
			model, _ := resourcePoolModel(resource)
			if model == nil {
				valid = false
				continue
			}
			if !seen[resource] {
				seen[resource] = true
				requested = append(requested, resource)
			}
		}
	}
	if !valid || len(requested) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_RESOURCES", "error": "请通过 resources 指定要查询的资源：xbox-child、netease，多个资源用逗号分隔。"})
		return
	}
	if m.wdb == nil || m.wdb.Db == nil {
		resourceStatusUnavailable(c)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	items := make([]resourceAvailabilityItem, 0, len(requested))
	allAvailable := true
	for _, resource := range requested {
		model, _ := resourcePoolModel(resource)
		var ids []string
		// Stop at the first unallocated row; no COUNT, joins or credential reads.
		err := m.wdb.Db.WithContext(ctx).Model(model).Where("hub_access_key_id IS NULL").Limit(1).Pluck("id", &ids).Error
		if err != nil {
			resourceStatusUnavailable(c)
			return
		}
		item := resourceAvailabilityItem{Resource: resource, Status: "available"}
		if len(ids) == 0 {
			allAvailable = false
			item.Status = "exhausted"
			item.Code = "RESOURCE_POOL_EXHAUSTED"
			item.Message = resourceExhaustedMessage(resource)
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{"resources": items, "allAvailable": allAvailable, "checkedAt": time.Now().UTC()})
}
func resourceStatusUnavailable(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{"code": "RESOURCE_STATUS_UNAVAILABLE", "status": "unknown", "error": "暂时无法确认资源状态，请稍后重试。"})
}

// Resource names and labels belong to Hub; admin clients render these choices.
func catalogResourceOptions() []gin.H {
	options := []gin.H{}
	for _, resource := range []string{"netease", "xbox-child"} {
		_, label := resourcePoolModel(resource)
		options = append(options, gin.H{"resource": resource, "label": label})
	}
	return options
}
