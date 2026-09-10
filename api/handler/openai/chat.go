package openai

import (
	"codebuddy-gateway/service"

	"github.com/gin-gonic/gin"
)

func ChatCompletions(c *gin.Context) {
	service.DefaultProxy.HandleChat(c)
}

func Completions(c *gin.Context) {
	service.DefaultProxy.HandleCompletions(c)
}

func RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/chat/completions", ChatCompletions)
	rg.POST("/completions", Completions)
	rg.GET("/models", ListModels)
}
