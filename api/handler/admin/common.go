package admin

import (
	"strconv"

	"codebuddy-gateway/api/response"
	"codebuddy-gateway/model"
	"codebuddy-gateway/service"

	"github.com/gin-gonic/gin"
)

func parseID(raw string) (uint, error) {
	n, err := strconv.ParseUint(raw, 10, 64)
	return uint(n), err
}

func ListModels(c *gin.Context) {
	list, err := model.ListModels()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, list)
}

type modelUpsertReq struct {
	ModelID     string `json:"model_id" binding:"required"`
	DisplayName string `json:"display_name"`
	MaxInput    int    `json:"max_input"`
	MaxOutput   int    `json:"max_output"`
	Vision      bool   `json:"vision"`
	Reasoning   bool   `json:"reasoning"`
	Enabled     *bool  `json:"enabled"`
	Sort        int    `json:"sort"`
	Remark      string `json:"remark"`
}

func UpsertModel(c *gin.Context) {
	var req modelUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	m := &model.LLMModel{
		ModelID:     req.ModelID,
		DisplayName: req.DisplayName,
		MaxInput:    req.MaxInput,
		MaxOutput:   req.MaxOutput,
		Vision:      req.Vision,
		Reasoning:   req.Reasoning,
		Enabled:     enabled,
		Sort:        req.Sort,
		Remark:      req.Remark,
	}
	if err := model.UpsertModel(m); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, m)
}

func Health(c *gin.Context) {
	service.DefaultWatchdog.RunOnce(c.Request.Context())
	accounts, err := model.ListAccounts()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	out := make([]gin.H, 0, len(accounts))
	for _, acc := range accounts {
		out = append(out, gin.H{
			"id":              acc.ID,
			"name":            acc.Name,
			"status":          acc.Status,
			"fail_count":      acc.FailCount,
			"last_error":      acc.LastError,
			"jwt_expires_at":  acc.JWTExpiresAt,
			"last_checked_at": acc.LastCheckedAt,
			"cooldown_until":  acc.CooldownUntil,
		})
	}
	response.Success(c, out)
}

func RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/accounts", ListAccounts)
	rg.POST("/accounts", CreateAccount)
	rg.POST("/accounts/import", ImportAccounts)
	rg.PUT("/accounts/:id", UpdateAccount)
	rg.DELETE("/accounts/:id", DeleteAccount)
	rg.POST("/accounts/:id/refresh", RefreshAccount)
	rg.POST("/accounts/:id/sync-credit", SyncAccountCredit)
	rg.POST("/accounts/:id/enable", EnableAccount)
	rg.POST("/accounts/:id/disable", DisableAccount)
	rg.POST("/refresh", RefreshAll)
	rg.POST("/sync-credit", SyncAllCredits)
	rg.GET("/models", ListModels)
	rg.PUT("/models", UpsertModel)
	rg.GET("/usage", ListUsage)
	rg.GET("/usage/summary", UsageOverview)
	rg.GET("/usage/models", ListUsageModels)
	rg.DELETE("/usage", DeleteUsageByFilter)
	rg.DELETE("/usage/all", ClearUsage)
	rg.GET("/usage/:id", GetUsage)
	rg.DELETE("/usage/:id", DeleteUsage)
	rg.GET("/stats/daily", UsageDaily)
	rg.GET("/stats/models", UsageByModel)
	rg.GET("/stats/accounts", UsageByAccount)
	rg.GET("/settings/refresh", GetRefreshSettings)
	rg.PUT("/settings/refresh", UpdateRefreshSettings)
	rg.GET("/settings/access", GetAccessSettings)
	rg.PUT("/settings/access", UpdateAccessSettings)
	rg.POST("/watchdog", Health)
}
