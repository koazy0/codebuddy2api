package service

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type modelCache struct {
	mu        sync.Mutex
	expiresAt time.Time
	items     []openaiModel
}

type openaiModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

var liveModels = &modelCache{}

func (p *Proxy) HandleModels(c *gin.Context) {
	items, err := p.listLiveModels(c.Request.Context())
	if err != nil {
		global.CORE_LOG.Warn("upstream models failed, fallback to local catalog", zap.Error(err))
		items = localOpenAIModels()
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   items,
	})
}

func (p *Proxy) listLiveModels(ctx context.Context) ([]openaiModel, error) {
	liveModels.mu.Lock()
	if time.Now().Before(liveModels.expiresAt) && len(liveModels.items) > 0 {
		items := append([]openaiModel(nil), liveModels.items...)
		liveModels.mu.Unlock()
		return items, nil
	}
	liveModels.mu.Unlock()

	acc, err := p.rotator.Next(nil)
	if err != nil {
		return nil, err
	}
	raw, err := p.client.FetchConfig(ctx, acc)
	if err != nil {
		return nil, err
	}
	items := parseUpstreamModels(raw)
	if len(items) == 0 {
		return nil, errEmptyUpstreamModels
	}
	items = appendAliases(items)

	liveModels.mu.Lock()
	liveModels.items = items
	liveModels.expiresAt = time.Now().Add(5 * time.Minute)
	liveModels.mu.Unlock()
	return items, nil
}

var errEmptyUpstreamModels = errString("upstream returned no models")

type errString string

func (e errString) Error() string { return string(e) }

func parseUpstreamModels(raw []byte) []openaiModel {
	var parsed struct {
		Code int `json:"code"`
		Data struct {
			Models []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	now := time.Now().Unix()
	out := make([]openaiModel, 0, len(parsed.Data.Models))
	seen := map[string]struct{}{}
	for _, m := range parsed.Data.Models {
		id := m.ID
		if id == "" {
			id = m.Name
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, openaiModel{
			ID:      id,
			Object:  "model",
			Created: now,
			OwnedBy: "codebuddy",
		})
	}
	return out
}

func appendAliases(items []openaiModel) []openaiModel {
	seen := map[string]struct{}{}
	for _, m := range items {
		seen[m.ID] = struct{}{}
	}
	now := time.Now().Unix()
	for _, alias := range global.CORE_CONFIG.Gateway.ModelAlias {
		if alias.From == "" {
			continue
		}
		if _, ok := seen[alias.From]; ok {
			continue
		}
		seen[alias.From] = struct{}{}
		items = append(items, openaiModel{
			ID:      alias.From,
			Object:  "model",
			Created: now,
			OwnedBy: "codebuddy",
		})
	}
	return items
}

func localOpenAIModels() []openaiModel {
	list, err := model.ListEnabledModels()
	now := time.Now().Unix()
	out := make([]openaiModel, 0)
	if err == nil {
		for _, m := range list {
			out = append(out, openaiModel{
				ID:      m.ModelID,
				Object:  "model",
				Created: now,
				OwnedBy: "codebuddy",
			})
		}
	}
	return appendAliases(out)
}
