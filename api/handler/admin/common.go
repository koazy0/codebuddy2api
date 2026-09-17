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

// Health 跑一轮看门狗并返回本轮统计 + 账号快照。
//
// 返回形状从「账号数组」改为 {round, accounts}：面板需要知道这一轮
// 做了什么（检查/恢复/判异常了多少），只拿到账号列表看不出这些。
// accounts 一并带上，省掉调用方紧跟一次 /admin/accounts 的往返。
func Health(c *gin.Context) {
	round := service.DefaultWatchdog.RunOnce(c.Request.Context())
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
	response.Success(c, gin.H{"round": round, "accounts": out})
}

func RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/accounts", ListAccounts)
	rg.POST("/accounts", CreateAccount)
	rg.POST("/accounts/import/preview", PreviewImportAccounts)
	rg.POST("/accounts/import", ImportAccounts)
	rg.PUT("/accounts/:id", UpdateAccount)
	rg.DELETE("/accounts/:id", DeleteAccount)
	rg.POST("/accounts/:id/refresh", RefreshAccount)
	rg.POST("/accounts/:id/sync-credit", SyncAccountCredit)
	rg.POST("/accounts/:id/enable", EnableAccount)
	rg.POST("/accounts/:id/disable", DisableAccount)
	// 成长中心任务中心
	rg.GET("/accounts/:id/tasks", TaskList)
	rg.POST("/accounts/:id/tasks/run", TaskRun)
	rg.POST("/accounts/:id/tasks/accept", TaskAcceptAll)
	rg.POST("/accounts/:id/tasks/:code/claim", TaskClaim)
	rg.GET("/tasks/catalog", TaskCatalogList)
	rg.GET("/cost-ledger", CostLedger)
	// 批量：启动即返回，进度靠轮询
	rg.POST("/tasks/batch", TaskBatchStart)
	rg.GET("/tasks/batch", TaskBatchStatus)
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
