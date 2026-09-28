package manager

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vertrai/hub/manager/schema"
)

func TestMiniProgramOptionalNickPersistsWithoutChangingOwnership(t *testing.T) {
	m := newLLMTestManager(t)
	if err := m.wdb.Db.AutoMigrate(&schema.User{}, &schema.AgentCatalogEntry{}, &schema.MiniProgramAgentTask{}); err != nil {
		t.Fatal(err)
	}
	user := schema.User{ID: "wx_original", Name: "original", Status: "active"}
	if err := m.wdb.Db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		input []string
		want  string
	}{
		{nil, ""}, {[]string{""}, ""}, {[]string{"  "}, ""},
		{[]string{" 小周🐱 "}, "小周🐱"}, {[]string{strings.Repeat("周", 70)}, strings.Repeat("周", 64)},
	}
	for i, tc := range cases {
		id := fmt.Sprintf("nick-agent-%d", i)
		if err := m.wdb.Db.Create(&schema.AgentCatalogEntry{ID: id, Name: "助手", Module: "module"}).Error; err != nil {
			t.Fatal(err)
		}
		task, err := m.reserveMiniProgramAgentTask(user.ID, id, "hash", tc.input...)
		if err != nil {
			t.Fatal(err)
		}
		var stored schema.MiniProgramAgentTask
		if err := m.wdb.Db.First(&stored, "id = ?", task.ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Nick != tc.want || stored.UserID != user.ID || stored.Status != schema.MiniProgramTaskSpawning {
			t.Fatalf("unexpected task: %+v", stored)
		}
	}
	var unchanged schema.User
	if err := m.wdb.Db.First(&unchanged, "id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchanged.Name != user.Name {
		t.Fatal("nickname must not rename the identity")
	}
}
