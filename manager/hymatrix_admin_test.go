package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStopHymatrixVM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/admin/vms/stop" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content type = %q", r.Header.Get("Content-Type"))
		}
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["pid"] != "process-1" {
			t.Fatalf("input = %#v, err = %v", input, err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": input["pid"], "message": "stopped"})
	}))
	defer server.Close()

	service, _ := New("test", Config{}, nil)
	if err := service.stopHymatrixVM(context.Background(), server.URL, "process-1"); err != nil {
		t.Fatal(err)
	}
}

func TestStopHymatrixVMTreatsMissingOrStoppedAsIdempotent(t *testing.T) {
	for _, code := range []string{"err_process_not_found", "err_process_stopped"} {
		t.Run(code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
			}))
			defer server.Close()
			service, _ := New("test", Config{}, nil)
			if err := service.stopHymatrixVM(context.Background(), server.URL, "process-1"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStopHymatrixVMReturnsBusinessError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "err_core_process_cannot_stop"})
	}))
	defer server.Close()
	service, _ := New("test", Config{}, nil)
	if err := service.stopHymatrixVM(context.Background(), server.URL, "process-1"); err == nil || err.Error() != "err_core_process_cannot_stop" {
		t.Fatalf("err = %v", err)
	}
}

func TestHymatrixAdminURLSupportsLegacyPods(t *testing.T) {
	service, _ := New("test", Config{}, nil)
	if got := service.hymatrixAdminURL(""); got != defaultHymatrixAdminURL {
		t.Fatalf("admin URL = %q", got)
	}
	service.config.MiniProgram.AdminURL = "http://node.internal:9082"
	if got := service.hymatrixAdminURL(""); got != "http://node.internal:9082" {
		t.Fatalf("configured admin URL = %q", got)
	}
	if got := service.hymatrixAdminURL("http://pod-node:8082"); got != "http://pod-node:8082" {
		t.Fatalf("pod admin URL = %q", got)
	}
}
