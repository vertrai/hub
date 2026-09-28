package manager

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type agentResourceError struct{ resource string }

func (e *agentResourceError) Error() string {
	if e.resource == "" {
		return "暂时无法确认资源状态，请稍后重试。"
	}
	return resourceExhaustedMessage(e.resource)
}

// Preflight only: actual allocation still arbitrates concurrent consumers.
func checkRequiredAgentResources(db *gorm.DB, resources []string) error {
	if len(resources) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(db.Statement.Context, 2*time.Second)
	defer cancel()
	for _, resource := range resources {
		model, _ := resourcePoolModel(resource)
		if model == nil {
			return &agentResourceError{}
		}
		var ids []string
		if err := db.WithContext(ctx).Model(model).Where("hub_access_key_id IS NULL").Limit(1).Pluck("id", &ids).Error; err != nil {
			return &agentResourceError{}
		}
		if len(ids) == 0 {
			return &agentResourceError{resource: resource}
		}
	}
	return nil
}
