package admin

import (
	"strings"
	"time"

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

type accountImportReq struct {
	Accounts []accountCreateReq `json:"accounts" binding:"required"`
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

func fillAccountFromJWT(acc *model.Account) {
	if acc.JWT == "" {
		return
	}
	acc.JWT = strings.TrimPrefix(strings.TrimSpace(acc.JWT), "Bearer ")
	if claims, err := service.ParseJWTClaims(acc.JWT); err == nil {
		if acc.Username == "" {
			acc.Username = claims.PreferredUsername
		}
		if acc.Name == "" {
			if claims.PreferredUsername != "" {
				acc.Name = claims.PreferredUsername
			} else {
				acc.Name = claims.Sub
			}
		}
		if claims.Exp > 0 {
			t := time.Unix(claims.Exp, 0)
			acc.JWTExpiresAt = &t
		}
	}
	if acc.RefreshToken != "" {
		if exp, err := service.JWTExpiry(acc.RefreshToken); err == nil {
			acc.RefreshExpiresAt = &exp
		}
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
	fillAccountFromJWT(acc)
	if err := model.CreateAccount(acc); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, publicAccount(*acc))
}

func ImportAccounts(c *gin.Context) {
	var req accountImportReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	created := make([]gin.H, 0, len(req.Accounts))
	for _, item := range req.Accounts {
		acc := &model.Account{
			Name:                strings.TrimSpace(item.Name),
			JWT:                 strings.TrimSpace(item.JWT),
			RefreshToken:        strings.TrimSpace(item.RefreshToken),
			SessionCookie:       strings.TrimSpace(item.SessionCookie),
			Weight:              item.Weight,
			Remark:              item.Remark,
			Status:              item.Status,
			MonthlyCreditTotal:  item.MonthlyCreditTotal,
			MonthlyCreditRemain: item.MonthlyCreditRemain,
			OnetimeCreditTotal:  item.OnetimeCreditTotal,
			OnetimeCreditRemain: item.OnetimeCreditRemain,
		}
		if acc.JWT == "" {
			continue
		}
		if acc.MonthlyCreditRemain == 0 && acc.MonthlyCreditTotal > 0 {
			acc.MonthlyCreditRemain = acc.MonthlyCreditTotal
		}
		if acc.OnetimeCreditRemain == 0 && acc.OnetimeCreditTotal > 0 {
			acc.OnetimeCreditRemain = acc.OnetimeCreditTotal
		}
		acc.CreditRemain = acc.MonthlyCreditRemain + acc.OnetimeCreditRemain
		fillAccountFromJWT(acc)
		if err := model.CreateAccount(acc); err != nil {
			response.Fail(c, err.Error())
			return
		}
		created = append(created, publicAccount(*acc))
	}
	response.Success(c, gin.H{"count": len(created), "accounts": created})
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
		fillAccountFromJWT(acc)
	}
	if req.RefreshToken != nil {
		acc.RefreshToken = strings.TrimSpace(*req.RefreshToken)
		fillAccountFromJWT(acc)
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
