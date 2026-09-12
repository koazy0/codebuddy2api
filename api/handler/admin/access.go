package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"sync"

	"codebuddy-gateway/api/response"
	"codebuddy-gateway/config"
	"codebuddy-gateway/global"

	"github.com/gin-gonic/gin"
)

// 控制台默认走 admin-key 校验；想给 dashboard 单独配一个密码，
// 就在 config.yaml 的 dashboard 段落里填一次，保存后热生效。
var dashboardSettingsMu sync.Mutex

type accessSettings struct {
	Configured       bool   `json:"configured"`
	Title            string `json:"title"`
	RequireAdminKey  bool   `json:"require_admin_key"`
	PasswordSet      bool   `json:"password_set"`
	PasswordStrength string `json:"password_strength"`
}

type accessSettingsReq struct {
	Title           *string `json:"title"`
	Password        *string `json:"password"`
	RequireAdminKey *bool   `json:"require_admin_key"`
}

type accessVerifyReq struct {
	Password string `json:"password"`
	AdminKey string `json:"admin_key"`
}

func dashboardConfig() config.Dashboard {
	return global.CORE_CONFIG.Dashboard
}

func currentAccessSettings() accessSettings {
	cfg := dashboardConfig()
	out := accessSettings{
		Configured:      true,
		Title:           cfg.TitleOrDefault(),
		RequireAdminKey: cfg.RequireAdminKey(),
		PasswordSet:     strings.TrimSpace(cfg.Password) != "",
	}
	if out.PasswordSet {
		out.PasswordStrength = passwordStrength(cfg.Password)
	}
	return out
}

// VerifyAccess 是控制台的统一登录口。密码和 admin-key 任意一项通过即可，
// 两者都配了就都要对——这样既能单独设面板密码，也不会绕过原有 admin-key。
func VerifyAccess(c *gin.Context) {
	var req accessVerifyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	cfg := dashboardConfig()
	if want := strings.TrimSpace(cfg.Password); want != "" {
		if !matchDashboardPassword(req.Password, want) {
			response.Unauthorized(c, "控制台密码不正确")
			return
		}
	}
	if cfg.RequireAdminKey() {
		if !secureEqual(req.AdminKey, global.CORE_CONFIG.Gateway.AdminKey) {
			response.Unauthorized(c, "admin key 不正确")
			return
		}
	}
	if strings.TrimSpace(cfg.Password) == "" && !cfg.RequireAdminKey() {
		response.Fail(c, "dashboard has no password and admin key check is disabled; set dashboard.password in config.yaml")
		return
	}
	response.Success(c, currentAccessSettings())
}

func GetAccessSettings(c *gin.Context) {
	response.Success(c, currentAccessSettings())
}

func UpdateAccessSettings(c *gin.Context) {
	var req accessSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	dashboardSettingsMu.Lock()
	defer dashboardSettingsMu.Unlock()

	cfg := dashboardConfig()
	if req.Title != nil {
		cfg.Title = strings.TrimSpace(*req.Title)
	}
	if req.Password != nil {
		pwd := *req.Password
		if pwd != "" && len(pwd) < 6 {
			response.BadRequest(c, "控制台密码至少 6 位")
			return
		}
		if strings.TrimSpace(cfg.Password) == "" && !cfg.RequireAdminKey() {
			response.BadRequest(c, "关掉 admin key 校验前必须先设置面板密码")
			return
		}
		cfg.Password = pwd
	}
	if req.RequireAdminKey != nil {
		require := *req.RequireAdminKey
		if !require && strings.TrimSpace(cfg.Password) == "" {
			response.BadRequest(c, "关掉 admin key 校验前必须先设置面板密码")
			return
		}
		cfg.RequireAdminKeyFlag = &require
	}

	if err := persistDashboardConfig(cfg); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, currentAccessSettings())
}

func secureEqual(got, want string) bool {
	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)
	if got == "" || want == "" {
		return false
	}
	gotHash := sha256.Sum256([]byte(got))
	wantHash := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) == 1
}

// passwordStrength 只做提示，不参与校验，避免把密码本身回给前端。
func passwordStrength(pwd string) string {
	var classes int
	switch {
	case len(pwd) >= 14:
		classes++
	}
	if strings.IndexFunc(pwd, func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0 {
		classes++
	}
	if strings.IndexFunc(pwd, func(r rune) bool { return r >= 'A' && r <= 'Z' }) >= 0 {
		classes++
	}
	if strings.IndexFunc(pwd, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0 {
		classes++
	}
	if strings.IndexFunc(pwd, func(r rune) bool { return !isAlphaNum(r) }) >= 0 {
		classes++
	}
	switch {
	case classes >= 4:
		return "strong"
	case classes == 3:
		return "good"
	default:
		return "weak"
	}
}

func isAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func persistDashboardConfig(cfg config.Dashboard) error {
	if strings.TrimSpace(cfg.Password) != "" && !strings.HasPrefix(strings.TrimSpace(cfg.Password), "sha256:") {
		sum := sha256.Sum256([]byte(cfg.Password))
		cfg.Password = "sha256:" + hex.EncodeToString(sum[:])
	}
	require := cfg.RequireAdminKey()
	if global.CORE_VP != nil {
		global.CORE_VP.Set("dashboard.title", cfg.Title)
		global.CORE_VP.Set("dashboard.password", cfg.Password)
		global.CORE_VP.Set("dashboard.require-admin-key", require)
	}
	body := "    title: " + yamlQuote(cfg.TitleOrDefault()) + "\n" +
		"    password: " + yamlQuote(cfg.Password) + "\n" +
		"    require-admin-key: " + boolString(require) + "\n"
	if err := writeYAMLSection("dashboard", body); err != nil {
		return err
	}
	global.CORE_CONFIG.Dashboard = cfg
	return nil
}

func yamlQuote(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`
}

func matchDashboardPassword(got, stored string) bool {
	got = strings.TrimSpace(got)
	stored = strings.TrimSpace(stored)
	if got == "" || stored == "" {
		return false
	}
	gotHash := sha256.Sum256([]byte(got))
	var want []byte
	if strings.HasPrefix(stored, "sha256:") {
		decoded, err := hex.DecodeString(strings.TrimPrefix(stored, "sha256:"))
		if err != nil {
			return false
		}
		want = decoded
	} else {
		sum := sha256.Sum256([]byte(stored))
		want = sum[:]
	}
	if len(want) != len(gotHash) {
		return false
	}
	return subtle.ConstantTimeCompare(gotHash[:], want) == 1
}
