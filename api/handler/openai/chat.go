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

func Responses(c *gin.Context) {
	service.DefaultProxy.HandleResponses(c)
}

func Messages(c *gin.Context) {
	service.DefaultProxy.HandleMessages(c)
}

func CountTokens(c *gin.Context) {
	service.DefaultProxy.HandleCountTokens(c)
}

func RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/chat/completions", ChatCompletions)
	rg.POST("/completions", Completions)
	rg.POST("/responses", Responses)
	rg.POST("/messages", Messages)
	rg.POST("/messages/count_tokens", CountTokens)
	rg.GET("/models", ListModels)
}
