package manager

import (
	"github.com/vertrai/hub/manager/schema"
	"testing"
)

func TestResolveLegacyWebPodNames(t *testing.T) {
	m := newCommerceTestManager(t)
	for _, a := range []schema.WebAgent{
		{ID: "web-name", PodID: "web-pod", CatalogID: "x", Product: "x_agent", Source: "name-1"},
		{ID: "legacy-name", PodID: "legacy-pod", Product: "x_agent", Source: "name-2"},
		{ID: "custom-name", PodID: "custom-pod", CatalogID: "x", Product: "x_agent", Source: "name-3"},
	} {
		if err := m.wdb.Db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
	}
	pods := []schema.HymatrixPod{{ID: "web-pod", Name: "x_agent"}, {ID: "legacy-pod", Name: "x_agent"}, {ID: "custom-pod", Name: "Custom"}, {ID: "wx-pod", Name: "Mini program agent"}}
	if err := m.resolveWebPodNames(pods); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"X", "X", "Custom", "Mini program agent"} {
		if pods[i].Name != want {
			t.Fatalf("pod %s: got %q, want %q", pods[i].ID, pods[i].Name, want)
		}
	}
}
