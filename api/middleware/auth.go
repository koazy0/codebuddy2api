package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"codebuddy-gateway/global"

	"github.com/gin-gonic/gin"
)

func OpenAIAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !matchKey(extractBearer(c), global.CORE_CONFIG.Gateway.APIKey) {
			if strings.HasPrefix(c.Request.URL.Path, "/v1/messages") {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"type":  "error",
					"error": gin.H{"type": "authentication_error", "message": "invalid api key"},
				})
				return
			}
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
		if matchKey(key, global.CORE_CONFIG.Gateway.AdminKey) {
			c.Next()
			return
		}
		if dashboardPasswordAllowed(c, key) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"code": 401,
			"msg":  "invalid admin key",
			"data": nil,
		})
	}
}

func dashboardPasswordAllowed(c *gin.Context, bearer string) bool {
	cfg := global.CORE_CONFIG.Dashboard
	stored := strings.TrimSpace(cfg.Password)
	if stored == "" || cfg.RequireAdminKey() {
		return false
	}
	got := strings.TrimSpace(c.GetHeader("X-Dashboard-Password"))
	if got == "" {
		got = bearer
	}
	return matchDashboardSecret(got, stored)
}

func matchDashboardSecret(got, stored string) bool {
	got = strings.TrimSpace(got)
	stored = strings.TrimSpace(stored)
	if got == "" || stored == "" {
		return false
	}
	sumGot := sha256.Sum256([]byte(got))
	if strings.HasPrefix(stored, "sha256:") {
		want, err := hex.DecodeString(strings.TrimPrefix(stored, "sha256:"))
		if err != nil || len(want) != len(sumGot) {
			return false
		}
		return subtle.ConstantTimeCompare(sumGot[:], want) == 1
	}
	sumWant := sha256.Sum256([]byte(stored))
	return subtle.ConstantTimeCompare(sumGot[:], sumWant[:]) == 1
}

func extractBearer(c *gin.Context) string {
	h := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if v := strings.TrimSpace(c.GetHeader("api-key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.GetHeader("X-Api-Key")); v != "" {
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
