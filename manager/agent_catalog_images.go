package manager

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm/clause"
)

var catalogImagePath = regexp.MustCompile(`^/v1/wechat/catalog-images/[a-f0-9]{64}$`)

const maxCatalogImageBytes = 5 << 20

func readCatalogImage(r io.Reader) (schema.AgentCatalogImage, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxCatalogImageBytes+1))
	if err != nil || len(data) > maxCatalogImageBytes {
		return schema.AgentCatalogImage{}, fmt.Errorf("图片不能超过 5 MB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 {
		return schema.AgentCatalogImage{}, fmt.Errorf("请上传尺寸不超过 4096 × 4096 的 PNG 或 JPEG 图片")
	}
	if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
		return schema.AgentCatalogImage{}, fmt.Errorf("图片损坏，请重新选择")
	}
	sum := sha256.Sum256(data)
	return schema.AgentCatalogImage{ID: fmt.Sprintf("%x", sum), ContentType: "image/" + format, Data: data}, nil
}

func (m *Manager) uploadAgentCatalogImage(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCatalogImageBytes)
	asset, err := readCatalogImage(c.Request.Body)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err = m.wdb.Db.Clauses(clause.OnConflict{DoNothing: true}).Create(&asset).Error; err != nil {
		catalogError(c, err)
		return
	}
	c.JSON(200, gin.H{"url": "/v1/wechat/catalog-images/" + asset.ID})
}

func (m *Manager) getAgentCatalogImage(c *gin.Context) {
	if !catalogImagePath.MatchString("/v1/wechat/catalog-images/" + c.Param("id")) {
		c.Status(404)
		return
	}
	if !m.catalogDB(c) {
		return
	}
	var asset schema.AgentCatalogImage
	if err := m.wdb.Db.First(&asset, "id = ?", c.Param("id")).Error; err != nil {
		catalogError(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, asset.ContentType, asset.Data)
}
