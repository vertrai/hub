package manager

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"net/http/httptest"
	"testing"
	"time"
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

func TestMiniProgramRebindRunsWithoutPageRequests(t *testing.T) {
	for _, completed := range []bool{true, false} {
		m := &Manager{weixinAttempts: map[string]weixinAttempt{
			"a": {ID: "a", UserID: "u", MiniProgramTaskID: "t", AutoRebind: true, Credentials: &WeixinCredentials{BotID: "b"}, CredentialExpiresAt: time.Now().Add(time.Hour)},
		}}
		calls := 0
		reset := func(ctx context.Context, podID, botID string) (int, gin.H) {
			calls++
			if podID != "p" || botID != "b" || ctx.Err() != nil {
				t.Fatal("invalid reset context or ownership")
			}
			if completed {
				return 202, gin.H{"completed": true}
			}
			return 502, gin.H{"error": "uncertain"}
		}
		task := schema.MiniProgramAgentTask{ID: "t", UserID: "u", PodID: "p"}
		m.runMiniProgramRebindWithReset(task, "a", reset)
		m.runMiniProgramRebindWithReset(task, "a", reset)
		a := m.weixinAttempts["a"]
		want := "uncertain"
		if completed {
			want = "completed"
		}
		if calls != 1 || a.RebindState != want || !a.Submitting || a.Credentials != nil {
			t.Fatalf("unexpected result: calls=%d state=%s", calls, a.RebindState)
		}
	}
}

func TestMiniProgramRebindCancelledBeforeWorkerDoesNotSubmit(t *testing.T) {
	m := &Manager{weixinAttempts: map[string]weixinAttempt{}}
	m.runMiniProgramRebindWithReset(schema.MiniProgramAgentTask{ID: "t", UserID: "u"}, "a", func(context.Context, string, string) (int, gin.H) {
		t.Fatal("cancelled attempt submitted")
		return 500, nil
	})
}
