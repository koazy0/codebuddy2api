package handler

import (
	"codebuddy-gateway/api/handler/admin"
	"codebuddy-gateway/api/handler/openai"
	"codebuddy-gateway/api/handler/ping"
	"codebuddy-gateway/api/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(engine *gin.Engine) {
	engine.GET("/healthz", ping.Healthz)

	v1 := engine.Group("/v1")
	v1.Use(middleware.OpenAIAuth())
	openai.RegisterRoutes(v1)

	adminGroup := engine.Group("/admin")
	adminGroup.Use(middleware.AdminAuth())
	admin.RegisterRoutes(adminGroup)
}
