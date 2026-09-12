package admin

import (
	"io"
	"strings"

	"codebuddy-gateway/api/response"
	"codebuddy-gateway/model"
	"codebuddy-gateway/service"

	"github.com/gin-gonic/gin"
)

type accountCreateReq struct {
	Name                string  `json:"name"`
	JWT                 string  `json:"jwt" binding:"required"`
	RefreshToken        string  `json:"refresh_token"`
	SessionCookie       string  `json:"session_cookie"`
	Weight              int     `json:"weight"`
	Remark              string  `json:"remark"`
	Status              string  `json:"status"`
	MonthlyCreditTotal  float64 `json:"monthly_credit_total"`
	MonthlyCreditRemain float64 `json:"monthly_credit_remain"`
	OnetimeCreditTotal  float64 `json:"onetime_credit_total"`
	OnetimeCreditRemain float64 `json:"onetime_credit_remain"`
}

type accountUpdateReq struct {
	Name          *string  `json:"name"`
	JWT           *string  `json:"jwt"`
	RefreshToken  *string  `json:"refresh_token"`
	SessionCookie *string  `json:"session_cookie"`
	Weight        *int     `json:"weight"`
	Remark        *string  `json:"remark"`
	Status        *string  `json:"status"`
	CreditRemain  *float64 `json:"credit_remain"`
}

func publicAccount(acc model.Account) gin.H {
	return gin.H{
		"id":                    acc.ID,
		"name":                  acc.Name,
		"username":              acc.Username,
		"status":                acc.Status,
		"weight":                acc.Weight,
		"jwt":                   service.MaskToken(acc.JWT),
		"refresh_token":         service.MaskToken(acc.RefreshToken),
		"has_session_cookie":    acc.SessionCookie != "",
		"last_refresh":          acc.LastRefresh,
		"jwt_expires_at":        acc.JWTExpiresAt,
		"refresh_expires_at":    acc.RefreshExpiresAt,
		"last_used_at":          acc.LastUsedAt,
		"last_checked_at":       acc.LastCheckedAt,
		"last_error":            acc.LastError,
		"fail_count":            acc.FailCount,
		"cooldown_until":        acc.CooldownUntil,
		"credit_remain":         acc.CreditRemain,
		"credit_used":           acc.CreditUsed,
		"monthly_credit_total":  acc.MonthlyCreditTotal,
		"monthly_credit_remain": acc.MonthlyCreditRemain,
		"onetime_credit_total":  acc.OnetimeCreditTotal,
		"onetime_credit_remain": acc.OnetimeCreditRemain,
		"monthly_cycle_start":   acc.MonthlyCycleStart,
		"monthly_cycle_end":     acc.MonthlyCycleEnd,
		"credit_synced_at":      acc.CreditSyncedAt,
		"dosage_notify_code":    acc.DosageNotifyCode,
		"dosage_notify_msg":     acc.DosageNotifyMsg,
		"credit_packages":       acc.CreditPackages,
		"remark":                acc.Remark,
		"created_at":            acc.CreatedAt,
		"updated_at":            acc.UpdatedAt,
	}
}

func ListAccounts(c *gin.Context) {
	list, err := model.ListAccounts()
	if err != nil {
		response.InternalServerError(c, err.Error())
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, acc := range list {
		out = append(out, publicAccount(acc))
	}
	response.Success(c, out)
}

func CreateAccount(c *gin.Context) {
	var req accountCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	acc := &model.Account{
		Name:                strings.TrimSpace(req.Name),
		JWT:                 strings.TrimSpace(req.JWT),
		RefreshToken:        strings.TrimSpace(req.RefreshToken),
		SessionCookie:       strings.TrimSpace(req.SessionCookie),
		Weight:              req.Weight,
		Remark:              req.Remark,
		Status:              req.Status,
		MonthlyCreditTotal:  req.MonthlyCreditTotal,
		MonthlyCreditRemain: req.MonthlyCreditRemain,
		OnetimeCreditTotal:  req.OnetimeCreditTotal,
		OnetimeCreditRemain: req.OnetimeCreditRemain,
	}
	if acc.MonthlyCreditRemain == 0 && acc.MonthlyCreditTotal > 0 {
		acc.MonthlyCreditRemain = acc.MonthlyCreditTotal
	}
	if acc.OnetimeCreditRemain == 0 && acc.OnetimeCreditTotal > 0 {
		acc.OnetimeCreditRemain = acc.OnetimeCreditTotal
	}
	acc.CreditRemain = acc.MonthlyCreditRemain + acc.OnetimeCreditRemain
	service.HydrateAccount(acc)
	if err := model.CreateAccount(acc); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, publicAccount(*acc))
}

func parseImportedAccounts(c *gin.Context) ([]service.ImportedAccount, error) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	return service.ParseImportedJSON(raw)
}

func previewImportedAccounts(items []service.ImportedAccount) []gin.H {
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		acc := item.ToModel()
		service.HydrateAccount(acc)
		out = append(out, gin.H{
			"name":               acc.Name,
			"username":           acc.Username,
			"jwt":                service.MaskToken(acc.JWT),
			"refresh_token":      service.MaskToken(acc.RefreshToken),
			"has_refresh":        acc.RefreshToken != "",
			"has_session_cookie": acc.SessionCookie != "",
			"remark":             acc.Remark,
		})
	}
	return out
}

func PreviewImportAccounts(c *gin.Context) {
	items, err := parseImportedAccounts(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"count": len(items), "accounts": previewImportedAccounts(items)})
}

func ImportAccounts(c *gin.Context) {
	items, err := parseImportedAccounts(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	created, updated := 0, 0
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		acc, isNew, err := service.UpsertAccount(item.ToModel())
		if err != nil {
			response.Fail(c, err.Error())
			return
		}
		if acc == nil {
			continue
		}
		if isNew {
			created++
		} else {
			updated++
		}
		out = append(out, publicAccount(*acc))
	}
	response.Success(c, gin.H{
		"count":    created + updated,
		"created":  created,
		"updated":  updated,
		"accounts": out,
	})
}

func UpdateAccount(c *gin.Context) {
	acc, err := loadAccount(c)
	if err != nil {
		return
	}
	var req accountUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Name != nil {
		acc.Name = strings.TrimSpace(*req.Name)
	}
	if req.JWT != nil {
		acc.JWT = strings.TrimSpace(*req.JWT)
		service.HydrateAccount(acc)
	}
	if req.RefreshToken != nil {
		acc.RefreshToken = strings.TrimSpace(*req.RefreshToken)
		service.HydrateAccount(acc)
	}
	if req.SessionCookie != nil {
		acc.SessionCookie = strings.TrimSpace(*req.SessionCookie)
	}
	if req.Weight != nil {
		acc.Weight = *req.Weight
	}
	if req.Remark != nil {
		acc.Remark = *req.Remark
	}
	if req.Status != nil {
		acc.Status = *req.Status
	}
	if req.CreditRemain != nil {
		acc.CreditRemain = *req.CreditRemain
	}
	if err := model.UpdateAccount(acc); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, publicAccount(*acc))
}

func DeleteAccount(c *gin.Context) {
	acc, err := loadAccount(c)
	if err != nil {
		return
	}
	if err := model.DeleteAccount(acc.ID); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"id": acc.ID})
}

func RefreshAccount(c *gin.Context) {
	acc, err := loadAccount(c)
	if err != nil {
		return
	}
	if err := service.DefaultRefresher.RefreshAccount(c.Request.Context(), acc); err != nil {
		response.Fail(c, err.Error())
		return
	}
	latest, _ := model.GetAccountByID(acc.ID)
	if latest == nil {
		latest = acc
	}
	response.Success(c, publicAccount(*latest))
}

func SyncAccountCredit(c *gin.Context) {
	acc, err := loadAccount(c)
	if err != nil {
		return
	}
	if err := service.DefaultWatchdog.SyncAccountCredit(c.Request.Context(), acc); err != nil {
		response.Fail(c, err.Error())
		return
	}
	latest, _ := model.GetAccountByID(acc.ID)
	if latest == nil {
		latest = acc
	}
	response.Success(c, publicAccount(*latest))
}

func EnableAccount(c *gin.Context) {
	setAccountStatus(c, model.AccountStatusEnabled)
}

func DisableAccount(c *gin.Context) {
	setAccountStatus(c, model.AccountStatusDisabled)
}

func setAccountStatus(c *gin.Context, status string) {
	acc, err := loadAccount(c)
	if err != nil {
		return
	}
	acc.Status = status
	acc.FailCount = 0
	acc.LastError = ""
	acc.CooldownUntil = nil
	if err := model.UpdateAccount(acc); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, publicAccount(*acc))
}

func RefreshAll(c *gin.Context) {
	scanned, refreshed, failed := service.DefaultRefresher.RefreshDueAccounts(c.Request.Context())
	response.Success(c, gin.H{
		"scanned":   scanned,
		"refreshed": refreshed,
		"failed":    failed,
	})
}

func SyncAllCredits(c *gin.Context) {
	list, err := model.ListAccounts()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	ok, fail := 0, 0
	for i := range list {
		if err := service.DefaultWatchdog.SyncAccountCredit(c.Request.Context(), &list[i]); err != nil {
			fail++
			continue
		}
		ok++
	}
	response.Success(c, gin.H{"ok": ok, "failed": fail, "total": len(list)})
}

func loadAccount(c *gin.Context) (*model.Account, error) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid id")
		return nil, err
	}
	acc, err := model.GetAccountByID(id)
	if err != nil {
		if model.AccountNotFound(err) {
			response.NotFound(c, "account not found")
			return nil, err
		}
		response.Fail(c, err.Error())
		return nil, err
	}
	return acc, nil
}
