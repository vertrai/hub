package manager

import (
	"github.com/vertrai/hub/manager/schema"
	"net/http/httptest"
	"testing"
)

func TestMiniProgramRebindRoutesRequireSession(t *testing.T) {
	m, err := New("test", Config{MiniProgram: MiniProgramConfig{AppSecret: "test-secret"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path string }{
		{"POST", "/v1/wechat/agents/task/weixin-rebind"},
		{"GET", "/v1/wechat/agents/task/weixin-rebind/attempt"},
		{"DELETE", "/v1/wechat/agents/task/weixin-rebind/attempt"},
		{"POST", "/v1/wechat/agents/task/weixin-rebind/attempt/confirm"},
	} {
		r := httptest.NewRecorder()
		m.router().ServeHTTP(r, httptest.NewRequest(tc.method, tc.path, nil))
		if r.Code != 401 {
			t.Fatalf("%s %s: got %d", tc.method, tc.path, r.Code)
		}
	}
}

func TestMiniProgramRebindAttemptIsolation(t *testing.T) {
	task := schema.MiniProgramAgentTask{ID: "task-a", UserID: "user-a"}
	for _, tc := range []struct {
		a    weixinAttempt
		want bool
	}{
		{weixinAttempt{UserID: "user-a", MiniProgramTaskID: "task-a"}, true},
		{weixinAttempt{UserID: "user-b", MiniProgramTaskID: "task-a"}, false},
		{weixinAttempt{UserID: "user-a", MiniProgramTaskID: "task-b"}, false},
		{weixinAttempt{UserID: "user-a"}, false},
	} {
		if ownsMiniProgramRebind(tc.a, task) != tc.want {
			t.Fatalf("unexpected ownership: %+v", tc.a)
		}
	}
}
