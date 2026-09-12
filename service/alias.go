package service

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"codebuddy-gateway/global"
)

const defaultFallbackModel = "deepseek-v4.1-flash"

var goodModelCache struct {
	mu   sync.Mutex
	name string
	dbAt time.Time
}

func ResolveModelAlias(name string) string {
	name = strings.TrimSpace(name)
	for _, alias := range global.CORE_CONFIG.Gateway.ModelAlias {
		if alias.From == name && alias.To != "" {
			return alias.To
		}
	}
	if isOpenAIHostedModel(name) {
		if fallback := fallbackUpstreamModel(); fallback != "" && fallback != name {
			return fallback
		}
	}
	return name
}

func isOpenAIHostedModel(name string) bool {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		return false
	}
	if strings.Contains(s, "codebuddy") {
		return false
	}
	if strings.Contains(s, "luna") || strings.Contains(s, "codex") {
		return true
	}
	if strings.HasPrefix(s, "gpt-") || strings.HasPrefix(s, "chatgpt") {
		return true
	}
	for _, p := range []string{"o1", "o3", "o4"} {
		if s == p || strings.HasPrefix(s, p+"-") || strings.HasPrefix(s, p+".") {
			return true
		}
	}
	return false
}

func fallbackUpstreamModel() string {
	if v := strings.TrimSpace(global.CORE_CONFIG.Gateway.FallbackModel); v != "" {
		return v
	}
	goodModelCache.mu.Lock()
	name := goodModelCache.name
	needDB := name == "" && global.CORE_DB != nil && time.Since(goodModelCache.dbAt) > 15*time.Second
	if needDB {
		goodModelCache.dbAt = time.Now()
	}
	goodModelCache.mu.Unlock()
	if name != "" && !isOpenAIHostedModel(name) {
		return name
	}
	if needDB {
		if dbName := latestSuccessfulUpstreamModel(); dbName != "" && !isOpenAIHostedModel(dbName) {
			rememberGoodModel(dbName)
			return dbName
		}
	}
	return defaultFallbackModel
}

func rememberGoodModel(name string) {
	name = strings.TrimSpace(name)
	if name == "" || isOpenAIHostedModel(name) {
		return
	}
	goodModelCache.mu.Lock()
	goodModelCache.name = name
	goodModelCache.mu.Unlock()
}

func latestSuccessfulUpstreamModel() string {
	db := global.CORE_DB
	if db == nil {
		return ""
	}
	var name string
	if err := db.Table("usage_logs").
		Select("upstream_model").
		Where("status_code = ? AND upstream_model <> ''", 200).
		Order("id desc").
		Limit(1).
		Scan(&name).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

func rewriteChatModel(body []byte, model string) []byte {
	if len(body) == 0 || strings.TrimSpace(model) == "" {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	payload["model"] = model
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

func maybeRewriteUnavailableModel(meta *ChatRequestMeta) bool {
	if meta == nil {
		return false
	}
	fallback := fallbackUpstreamModel()
	if fallback == "" || fallback == meta.UpstreamModel {
		return false
	}
	rewritten := rewriteChatModel(meta.Body, fallback)
	if len(rewritten) == 0 {
		return false
	}
	meta.UpstreamModel = fallback
	meta.Body = rewritten
	return true
}

func resetGoodModelCache() {
	goodModelCache.mu.Lock()
	goodModelCache.name = ""
	goodModelCache.dbAt = time.Time{}
	goodModelCache.mu.Unlock()
}
