package schema

import (
	"reflect"
	"strings"
	"testing"
)

func TestManagerTableNames(t *testing.T) {
	if got := (User{}).TableName(); got != "manager_users" {
		t.Fatalf("User table = %q", got)
	}
	if got := (HymatrixPod{}).TableName(); got != "manager_hymatrix_pods" {
		t.Fatalf("HymatrixPod table = %q", got)
	}
	if got := (AccessKey{}).TableName(); got != "manager_access_keys" {
		t.Fatalf("AccessKey table = %q", got)
	}
	if got := (MiniProgramAgentTask{}).TableName(); got != "manager_mini_program_agent_tasks" {
		t.Fatalf("MiniProgramAgentTask table = %q", got)
	}
}

func TestPodAccessKeyAllowsHistoricalRetries(t *testing.T) {
	field, ok := reflect.TypeOf(HymatrixPod{}).FieldByName("AccessKeyID")
	if !ok {
		t.Fatal("AccessKeyID field not found")
	}
	tag := field.Tag.Get("gorm")
	if strings.Contains(strings.ToLower(tag), "unique") {
		t.Fatalf("AccessKeyID must not be unique: %q", tag)
	}
	if !strings.Contains(tag, "idx_manager_hymatrix_pods_access_key_history") {
		t.Fatalf("AccessKeyID history index missing: %q", tag)
	}
}
