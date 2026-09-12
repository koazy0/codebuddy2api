package handler

import (
	"codebuddy-gateway/api/handler/admin"
	"codebuddy-gateway/api/handler/dashboard"
	"codebuddy-gateway/api/handler/openai"
	"codebuddy-gateway/api/handler/ping"
	"codebuddy-gateway/api/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(engine *gin.Engine) {
	engine.GET("/healthz", ping.Healthz)
	engine.GET("/", dashboard.Index)
	engine.GET("/dashboard", dashboard.Index)
	engine.GET("/index.html", dashboard.Index)
	engine.GET("/assets/app.css", dashboard.AppCSS)
	engine.GET("/assets/app.js", dashboard.AppJS)
	engine.POST("/admin/auth/verify", admin.VerifyAccess)

	v1 := engine.Group("/v1")
	v1.Use(middleware.OpenAIAuth())
	openai.RegisterRoutes(v1)

	adminGroup := engine.Group("/admin")
	adminGroup.Use(middleware.AdminAuth())
	admin.RegisterRoutes(adminGroup)
}
