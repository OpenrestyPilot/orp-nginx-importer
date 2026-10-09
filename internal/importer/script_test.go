package importer

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCollectorScriptEndpoint(t *testing.T) {
	api, err := New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/scripts/send_nginx_config_script.sh", nil)
	res := httptest.NewRecorder()
	api.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if !strings.Contains(res.Header().Get("Content-Type"), "text/x-shellscript") {
		t.Fatalf("unexpected content type: %s", res.Header().Get("Content-Type"))
	}
	if !strings.Contains(res.Body.String(), "NGINX_CONFIG_API_BASE_URL") || !strings.Contains(res.Body.String(), "docker ps -q") {
		t.Fatal("served file does not contain the configured collector script")
	}
}

func TestCollectorScriptEndpointRejectsPost(t *testing.T) {
	api, err := New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/scripts/send_nginx_config_script.sh", nil)
	res := httptest.NewRecorder()
	api.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", res.Code)
	}
}
