package importer

import (
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ORP/orp-nginx-importer/scripts"
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
	if !strings.Contains(res.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("unexpected content type: %s", res.Header().Get("Content-Type"))
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(res.Body.String()))
	if err != nil {
		t.Fatalf("response is not valid base64: %v", err)
	}
	original, err := fs.ReadFile(scripts.Files, "send_nginx_config_script.sh")
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(original) {
		t.Fatal("base64 response does not decode to the embedded collector script")
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
