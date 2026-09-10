package middleware

import (
	"net/http"
	"strings"

	"codebuddy-gateway/global"

	"github.com/gin-gonic/gin"
)

func OpenAIAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !matchKey(extractBearer(c), global.CORE_CONFIG.Gateway.APIKey) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "invalid api key", "type": "invalid_request_error"},
			})
			return
		}
		c.Next()
	}
}

func AdminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extractBearer(c)
		if key == "" {
			key = strings.TrimSpace(c.GetHeader("X-Admin-Key"))
		}
		if !matchKey(key, global.CORE_CONFIG.Gateway.AdminKey) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "invalid admin key",
				"data": nil,
			})
			return
		}
		c.Next()
	}
}

func extractBearer(c *gin.Context) string {
	h := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if v := strings.TrimSpace(c.GetHeader("api-key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Query("api_key")); v != "" {
		return v
	}
	return h
}

func matchKey(got, want string) bool {
	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	return got == want
}
