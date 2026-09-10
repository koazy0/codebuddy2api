package openai

import (
	"codebuddy-gateway/service"

	"github.com/gin-gonic/gin"
)

func ListModels(c *gin.Context) {
	service.DefaultProxy.HandleModels(c)
}
