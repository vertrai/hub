package manager

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"image"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogGeneratedID(t *testing.T) {
	for _, name := range []string{"旅行助手", "Travel Assistant", " MC 助手 ", strings.Repeat("x", 24)} {
		id := generatedCatalogID(name)
		if !catalogID.MatchString(id) || id != generatedCatalogID(name) {
			t.Fatalf("invalid or unstable ID: %s", id)
		}
	}
	if generatedCatalogID("旅行助手") == generatedCatalogID("票账助手") {
		t.Fatal("names collide")
	}
}

func TestCatalogImageValidation(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	asset, err := readCatalogImage(bytes.NewReader(data.Bytes()))
	if err != nil || asset.ContentType != "image/png" || !catalogImagePath.MatchString("/v1/wechat/catalog-images/"+asset.ID) || !bytes.Equal(asset.Data, data.Bytes()) {
		t.Fatalf("valid image rejected: %v", err)
	}
	again, _ := readCatalogImage(bytes.NewReader(data.Bytes()))
	if again.ID != asset.ID {
		t.Fatal("identical upload not deduplicated")
	}
	for _, invalid := range [][]byte{[]byte("<svg onload='alert(1)'></svg>"), data.Bytes()[:40], bytes.Repeat([]byte("x"), maxCatalogImageBytes+1)} {
		if _, err := readCatalogImage(bytes.NewReader(invalid)); err == nil {
			t.Fatal("accepted invalid image")
		}
	}
}

func TestCatalogImageUploadAndPublicRead(t *testing.T) {
	m := catalogFixture(t)
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	rec := catalogRequest(m, "POST", "/", data.String(), "", m.uploadAgentCatalogImage, nil)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var result struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(result.URL, "/v1/wechat/catalog-images/")
	rec = catalogRequest(m, "GET", result.URL, "", "", m.getAgentCatalogImage, gin.Params{{Key: "id", Value: id}})
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), data.Bytes()) {
		t.Fatal("uploaded image not retrievable")
	}
	entry := schema.AgentCatalogEntry{Name: "新旅行助手", LogoURL: result.URL, Intro: "旅行规划", Capabilities: []string{"规划"}, Module: "travel"}
	body, _ := json.Marshal(entry)
	rec = catalogRequest(m, "POST", "/", string(body), "", m.adminSaveAgentCatalog, nil)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &entry); err != nil || entry.ID != generatedCatalogID(entry.Name) {
		t.Fatal("ID not generated from name")
	}
	original := entry.ID
	entry.Name = "改名后旅行助手"
	body, _ = json.Marshal(entry)
	rec = catalogRequest(m, "PUT", "/", string(body), "", m.adminSaveAgentCatalog, gin.Params{{Key: "id", Value: original}})
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var saved schema.AgentCatalogEntry
	json.Unmarshal(rec.Body.Bytes(), &saved)
	if saved.ID != original {
		t.Fatal("rename changed ID")
	}
	service, err := New("test", Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	service.router().ServeHTTP(recorder, httptest.NewRequest("POST", "/v1/admin/agent-catalog-images", bytes.NewReader(data.Bytes())))
	if recorder.Code != 401 {
		t.Fatal("image upload is not protected")
	}
}
