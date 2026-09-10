package api

import (
	"context"
	"net/http"
	"time"

	"codebuddy-gateway/api/handler"
	"codebuddy-gateway/global"

	"github.com/gin-gonic/gin"
)

type APIServer struct {
	Listen string
	srv    *http.Server
}

func NewAPIServer() *APIServer {
	return &APIServer{Listen: global.CORE_CONFIG.System.ListenAddr}
}

func (b *APIServer) ServerRun() {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery(), corsMiddleware(), gin.Logger())

	handler.RegisterRoutes(engine)

	b.srv = &http.Server{
		Addr:              b.Listen,
		Handler:           engine,
		ReadHeaderTimeout: 15 * time.Second,
	}

	global.CORE_LOG.Info("server starting on " + b.Listen)
	if err := b.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		global.CORE_LOG.Fatal("listen: " + err.Error())
	}
}

func (b *APIServer) ServerShutdown() {
	if b.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = b.srv.Shutdown(ctx)
	global.CORE_LOG.Info("server shutdown successfully")
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, Token, X-Token, X-Admin-Key, api-key")
		c.Header("Access-Control-Allow-Methods", "POST, GET, OPTIONS, DELETE, PUT")
		c.Header("Access-Control-Allow-Credentials", "true")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
