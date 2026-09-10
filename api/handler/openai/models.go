package openai

import (
	"net/http"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"

	"github.com/gin-gonic/gin"
)

func ListModels(c *gin.Context) {
	list, err := model.ListEnabledModels()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"message": err.Error(), "type": "internal_error"},
		})
		return
	}
	data := make([]gin.H, 0, len(list)+len(global.CORE_CONFIG.Gateway.ModelAlias))
	seen := map[string]struct{}{}
	now := time.Now().Unix()
	for _, m := range list {
		seen[m.ModelID] = struct{}{}
		data = append(data, gin.H{
			"id":       m.ModelID,
			"object":   "model",
			"created":  now,
			"owned_by": "codebuddy",
		})
	}
	for _, alias := range global.CORE_CONFIG.Gateway.ModelAlias {
		if alias.From == "" {
			continue
		}
		if _, ok := seen[alias.From]; ok {
			continue
		}
		seen[alias.From] = struct{}{}
		data = append(data, gin.H{
			"id":       alias.From,
			"object":   "model",
			"created":  now,
			"owned_by": "codebuddy",
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}
