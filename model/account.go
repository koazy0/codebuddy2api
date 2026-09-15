package model

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"codebuddy-gateway/global"

	"gorm.io/gorm"
)

// jwtSubject 从 JWT 载荷里取出 sub（= 成长中心的 userId）。
// 解析失败返回空串——调用方按「没有 userId」处理，不外抛：
// 拿不到 userId 只是事件可能不计数，不该让整个任务流程报错。
func jwtSubject(token string) string {
	token = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(token), "Bearer "))
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return strings.TrimSpace(claims.Sub)
}

const (
	AccountStatusEnabled  = "enabled"
	AccountStatusDisabled = "disabled"
	AccountStatusCooldown = "cooldown"
)

type Account struct {
	global.CORE_MODEL
	Name                string     `gorm:"size:100;index" json:"name"`
	Username            string     `gorm:"size:100;index" json:"username"`
	JWT                 string     `gorm:"type:text;not null" json:"-"`
	RefreshToken        string     `gorm:"type:text" json:"-"`
	SessionCookie       string     `gorm:"type:text" json:"-"`
	Status              string     `gorm:"size:20;index;default:enabled" json:"status"`
	Weight              int        `gorm:"default:1" json:"weight"`
	LastRefresh         *time.Time `json:"last_refresh"`
	JWTExpiresAt        *time.Time `json:"jwt_expires_at"`
	RefreshExpiresAt    *time.Time `json:"refresh_expires_at"`
	LastUsedAt          *time.Time `json:"last_used_at"`
	LastCheckedAt       *time.Time `json:"last_checked_at"`
	LastError           string     `gorm:"type:text" json:"last_error"`
	FailCount           int        `gorm:"default:0" json:"fail_count"`
	CooldownUntil       *time.Time `json:"cooldown_until"`
	CreditRemain        float64    `gorm:"default:0" json:"credit_remain"`
	CreditUsed          float64    `gorm:"default:0" json:"credit_used"`
	MonthlyCreditTotal  float64    `gorm:"default:0" json:"monthly_credit_total"`
	MonthlyCreditRemain float64    `gorm:"default:0" json:"monthly_credit_remain"`
	OnetimeCreditTotal  float64    `gorm:"default:0" json:"onetime_credit_total"`
	OnetimeCreditRemain float64    `gorm:"default:0" json:"onetime_credit_remain"`
	MonthlyCycleStart   *time.Time `json:"monthly_cycle_start"`
	MonthlyCycleEnd     *time.Time `json:"monthly_cycle_end"`
	CreditSyncedAt      *time.Time `json:"credit_synced_at"`
	DosageNotifyCode    int        `json:"dosage_notify_code"`
	DosageNotifyMsg     string     `gorm:"size:500" json:"dosage_notify_msg"`
	CreditPackages      string     `gorm:"type:text" json:"credit_packages"`
	Remark              string     `gorm:"size:500" json:"remark"`
	// UserID 是成长中心任务用的 userId（= JWT sub / /v2/plugin/accounts 的 uid）。
	// 行为事件上报缺了它会被上游静默丢弃（返回 200 但不计进度）。
	// 导入路径会直接落库；未落库的账号由 UID() 从 JWT 实时解析。
	UserID string `gorm:"size:64;index" json:"user_id"`
}

func (Account) TableName() string { return "accounts" }

// UID 返回成长中心任务用的 userId：优先用已解析的值，缺失时从 JWT 的 sub 解析。
//
// 解析结果只缓存在内存（a.UserID），**不写库**：
// JWT 解析是纯本地操作，开销可忽略，而每次任务调用都写一次库只会带来
// 无谓的写放大与锁竞争。导入路径会把它直接落库（见 account_import），
// 那种情况下一开始就有值，走不到这里。
//
// 之所以保留 JWT 回落：userId 是后加的需求，老库这一列是空的，
// 而 JWT 的 sub 实测与 /v2/plugin/accounts 返回的 uid 完全一致，
// 直接解析即可，不必为此多打一次上游请求。
func (a *Account) UID() string {
	if a == nil {
		return ""
	}
	if a.UserID != "" {
		return a.UserID
	}
	return jwtSubject(a.JWT)
}

func CreateAccount(acc *Account) error {
	if acc.Status == "" {
		acc.Status = AccountStatusEnabled
	}
	if acc.Weight <= 0 {
		acc.Weight = 1
	}
	return MustDB().Create(acc).Error
}

func UpdateAccount(acc *Account) error {
	return MustDB().Save(acc).Error
}

func DeleteAccount(id uint) error {
	return MustDB().Delete(&Account{}, id).Error
}

func GetAccountByID(id uint) (*Account, error) {
	var acc Account
	err := MustDB().First(&acc, id).Error
	if err != nil {
		return nil, err
	}
	return &acc, nil
}

func GetAccountByUsername(username string) (*Account, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var acc Account
	err := MustDB().Where("username = ?", username).Order("id asc").First(&acc).Error
	if err != nil {
		return nil, err
	}
	return &acc, nil
}

func GetAccountByJWT(jwt string) (*Account, error) {
	jwt = strings.TrimSpace(jwt)
	if jwt == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var acc Account
	err := MustDB().Where("jwt = ?", jwt).Order("id asc").First(&acc).Error
	if err != nil {
		return nil, err
	}
	return &acc, nil
}

func ListAccounts() ([]Account, error) {
	var list []Account
	err := MustDB().Order("id asc").Find(&list).Error
	return list, err
}

func ListEnabledAccounts() ([]Account, error) {
	var list []Account
	now := time.Now()
	err := MustDB().Where("status IN ?", []string{AccountStatusEnabled, AccountStatusCooldown}).
		Where("cooldown_until IS NULL OR cooldown_until < ?", now).
		Order("id asc").
		Find(&list).Error
	return list, err
}

func ListRefreshableAccounts() ([]Account, error) {
	var list []Account
	err := MustDB().Where("status IN ?", []string{AccountStatusEnabled, AccountStatusCooldown, AccountStatusDisabled}).
		Where("jwt <> ''").
		Order("id asc").
		Find(&list).Error
	return list, err
}

func SaveAccountTokens(id uint, jwt, refreshToken string, jwtExp, refreshExp *time.Time) error {
	now := time.Now()
	updates := map[string]any{
		"jwt":          jwt,
		"last_refresh": now,
		"last_error":   "",
		"fail_count":   0,
	}
	if refreshToken != "" {
		updates["refresh_token"] = refreshToken
	}
	if jwtExp != nil {
		updates["jwt_expires_at"] = jwtExp
	}
	if refreshExp != nil {
		updates["refresh_expires_at"] = refreshExp
	}
	return MustDB().Model(&Account{}).Where("id = ?", id).Updates(updates).Error
}

func MarkAccountUsed(id uint) error {
	now := time.Now()
	return MustDB().Model(&Account{}).Where("id = ?", id).Updates(map[string]any{
		"last_used_at":   now,
		"fail_count":     0,
		"last_error":     "",
		"status":         AccountStatusEnabled,
		"cooldown_until": nil,
	}).Error
}

func MarkAccountFailure(id uint, errMsg string, failCount int, status string, cooldown *time.Time) error {
	updates := map[string]any{
		"last_error":      errMsg,
		"fail_count":      failCount,
		"status":          status,
		"last_checked_at": time.Now(),
	}
	if cooldown != nil {
		updates["cooldown_until"] = cooldown
	}
	return MustDB().Model(&Account{}).Where("id = ?", id).Updates(updates).Error
}

type CreditDeduction struct {
	Monthly       float64
	Onetime       float64
	Source        string
	MonthlyRemain float64
	OnetimeRemain float64
	Remain        float64
}

type CreditSnapshot struct {
	MonthlyTotal  float64
	MonthlyRemain float64
	OnetimeTotal  float64
	OnetimeRemain float64
	CycleStart    *time.Time
	CycleEnd      *time.Time
	PackagesJSON  string
	NotifyCode    int
	NotifyMsg     string
}

func ApplyCreditDeduction(id uint, credit float64) (*CreditDeduction, error) {
	acc, err := GetAccountByID(id)
	if err != nil {
		return nil, err
	}
	refreshMonthlyCycle(acc, time.Now())
	result := deductCredit(acc.MonthlyCreditRemain, acc.OnetimeCreditRemain, credit)
	acc.MonthlyCreditRemain = result.MonthlyRemain
	acc.OnetimeCreditRemain = result.OnetimeRemain
	acc.CreditRemain = result.Remain
	if credit > 0 {
		acc.CreditUsed += credit
	}
	if err := MustDB().Model(&Account{}).Where("id = ?", id).Updates(map[string]any{
		"monthly_credit_remain": acc.MonthlyCreditRemain,
		"onetime_credit_remain": acc.OnetimeCreditRemain,
		"credit_remain":         acc.CreditRemain,
		"credit_used":           acc.CreditUsed,
		"monthly_cycle_start":   acc.MonthlyCycleStart,
		"monthly_cycle_end":     acc.MonthlyCycleEnd,
	}).Error; err != nil {
		return nil, err
	}
	return result, nil
}

func deductCredit(monthlyRemain, onetimeRemain, credit float64) *CreditDeduction {
	result := &CreditDeduction{MonthlyRemain: monthlyRemain, OnetimeRemain: onetimeRemain}
	if credit <= 0 {
		result.Source = ""
		result.Remain = monthlyRemain + onetimeRemain
		if result.Remain < 0 {
			result.Remain = 0
		}
		return result
	}
	left := credit
	if result.MonthlyRemain > 0 {
		if result.MonthlyRemain >= left {
			result.Monthly = left
			result.MonthlyRemain -= left
			left = 0
		} else {
			result.Monthly = result.MonthlyRemain
			left -= result.MonthlyRemain
			result.MonthlyRemain = 0
		}
	}
	if left > 0 && result.OnetimeRemain > 0 {
		if result.OnetimeRemain >= left {
			result.Onetime = left
			result.OnetimeRemain -= left
			left = 0
		} else {
			result.Onetime = result.OnetimeRemain
			left -= result.OnetimeRemain
			result.OnetimeRemain = 0
		}
	}
	if result.Monthly > 0 && result.Onetime > 0 {
		result.Source = "mixed"
	} else if result.Monthly > 0 {
		result.Source = "monthly"
	} else if result.Onetime > 0 {
		result.Source = "onetime"
	} else {
		result.Source = "untracked"
	}
	result.Remain = result.MonthlyRemain + result.OnetimeRemain
	if result.Remain < 0 {
		result.Remain = 0
	}
	return result
}

func refreshMonthlyCycle(acc *Account, now time.Time) {
	if acc.MonthlyCreditTotal <= 0 {
		return
	}
	if acc.MonthlyCycleStart == nil || acc.MonthlyCycleEnd == nil {
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		end := start.AddDate(0, 1, 0).Add(-time.Second)
		acc.MonthlyCycleStart = &start
		acc.MonthlyCycleEnd = &end
		acc.MonthlyCreditRemain = acc.MonthlyCreditTotal
		return
	}
	if now.After(*acc.MonthlyCycleEnd) {
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		end := start.AddDate(0, 1, 0).Add(-time.Second)
		acc.MonthlyCycleStart = &start
		acc.MonthlyCycleEnd = &end
		acc.MonthlyCreditRemain = acc.MonthlyCreditTotal
	}
}

func RefreshMonthlyCycleIfNeeded(id uint) error {
	acc, err := GetAccountByID(id)
	if err != nil {
		return err
	}
	refreshMonthlyCycle(acc, time.Now())
	return MustDB().Model(&Account{}).Where("id = ?", id).Updates(map[string]any{
		"monthly_credit_remain": acc.MonthlyCreditRemain,
		"credit_remain":         acc.MonthlyCreditRemain + acc.OnetimeCreditRemain,
		"monthly_cycle_start":   acc.MonthlyCycleStart,
		"monthly_cycle_end":     acc.MonthlyCycleEnd,
	}).Error
}

func SaveCreditSnapshot(id uint, snap CreditSnapshot) error {
	remain := snap.MonthlyRemain + snap.OnetimeRemain
	now := time.Now()
	updates := map[string]any{
		"monthly_credit_total":  snap.MonthlyTotal,
		"monthly_credit_remain": snap.MonthlyRemain,
		"onetime_credit_total":  snap.OnetimeTotal,
		"onetime_credit_remain": snap.OnetimeRemain,
		"credit_remain":         remain,
		"credit_synced_at":      now,
	}
	if snap.CycleStart != nil {
		updates["monthly_cycle_start"] = snap.CycleStart
	}
	if snap.CycleEnd != nil {
		updates["monthly_cycle_end"] = snap.CycleEnd
	}
	if snap.PackagesJSON != "" {
		updates["credit_packages"] = snap.PackagesJSON
	}
	updates["dosage_notify_code"] = snap.NotifyCode
	updates["dosage_notify_msg"] = snap.NotifyMsg
	return MustDB().Model(&Account{}).Where("id = ?", id).Updates(updates).Error
}

func SaveDosageNotify(id uint, code int, msg string) error {
	return MustDB().Model(&Account{}).Where("id = ?", id).Updates(map[string]any{
		"dosage_notify_code": code,
		"dosage_notify_msg":  msg,
		"last_checked_at":    time.Now(),
	}).Error
}

func AccountNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
