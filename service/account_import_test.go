package service

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractImportedAccountsWorkBuddy(t *testing.T) {
	raw := []byte(`{
		"account": {"uid": "u-1", "nickname": "Ana"},
		"auth": {"accessToken": "jwt-aaa", "refreshToken": "rt-aaa"},
		"accounts": [{"auth": {"accessToken": "jwt-aaa", "refreshToken": "rt-aaa"}}],
		"access_token": "jwt-aaa",
		"refresh_token": "rt-aaa",
		"uid": "u-1",
		"nickname": "Ana"
	}`)
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	items := extractImportedAccounts(data)
	if len(items) != 1 {
		t.Fatalf("len=%d items=%+v", len(items), items)
	}
	if items[0].JWT != "jwt-aaa" || items[0].RefreshToken != "rt-aaa" || items[0].Name != "Ana" {
		t.Fatalf("item=%+v", items[0])
	}
}

func TestLoadImportedAccountsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")
	if err := os.WriteFile(path, []byte(`{"jwt":"token-1","refresh_token":"rt-1","name":"bob"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := LoadImportedAccounts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "bob" || items[0].JWT != "token-1" {
		t.Fatalf("items=%+v", items)
	}
}

func TestHydrateAccountFromJWT(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"preferred_username": "alice",
		"sub":                "sub-1",
		"exp":                2000000000,
	})
	token := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".x"
	acc := ImportedAccount{JWT: token, RefreshToken: ""}.ToModel()
	HydrateAccount(acc)
	if acc.Username != "alice" || acc.Name != "alice" {
		t.Fatalf("acc=%+v", acc)
	}
	if acc.JWTExpiresAt == nil || acc.JWTExpiresAt.Unix() != 2000000000 {
		t.Fatalf("exp=%v", acc.JWTExpiresAt)
	}
}

func TestParseImportedJSONConsoleDump(t *testing.T) {
	raw := []byte(`{
		"account": {"uid": "u-ana", "nickname": "Ana Renata", "type": "personal"},
		"auth": {"accessToken": "jwt-ana", "refreshToken": "rt-ana", "tokenType": "Bearer", "domain": "www.codebuddy.cn"},
		"accounts": [{"uid": "u-ana", "nickname": "Ana Renata", "type": "personal"}],
		"allAccounts": [{"uid": "u-ana", "nickname": "Ana Renata"}],
		"access_token": "jwt-ana",
		"refresh_token": "rt-ana",
		"uid": "u-ana",
		"nickname": "Ana Renata",
		"domain": "www.codebuddy.cn"
	}`)
	items, err := ParseImportedJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("len=%d items=%+v", len(items), items)
	}
	if items[0].JWT != "jwt-ana" || items[0].RefreshToken != "rt-ana" || items[0].Name != "Ana Renata" {
		t.Fatalf("item=%+v", items[0])
	}
}

func TestParseImportedJSONArrayAndMissingToken(t *testing.T) {
	items, err := ParseImportedJSON([]byte(`[{"name":"a","jwt":"jwt-1","refresh_token":"rt-1"},{"name":"b","accessToken":"jwt-2"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("len=%d", len(items))
	}
	if _, err := ParseImportedJSON([]byte(`{"accounts":[{"nickname":"no-token"}]}`)); err == nil {
		t.Fatal("expected missing token error")
	}
}

// TestNormalizeImportedUID 锁定一条容易踩的坑：导入 JSON 里的 uid 字段
// 语义不统一，实测既有真实 UUID，也有手机号/邮箱。
// 把手机号当 userId 上报会被上游当作错误身份**静默丢弃**事件——
// 比没有值更糟，因为它看起来是成功的。所以只接受 UUID 形态。
func TestNormalizeImportedUID(t *testing.T) {
	good := "2993afbb-00b2-4552-99c5-1f1311ff83fc"
	if got := normalizeImportedUID(good); got != good {
		t.Fatalf("合法 UUID 应保留，实际 %q", got)
	}
	if got := normalizeImportedUID("  " + strings.ToUpper(good) + " "); got != strings.ToUpper(good) {
		t.Fatalf("应 trim 并接受大写，实际 %q", got)
	}
	bad := []string{
		"", "19930182680", "user@example.com", "Ana Renata",
		"2993afbb-00b2-4552-99c5", "not-a-uuid",
		"2993afbbx00b2-4552-99c5-1f1311ff83fc",
	}
	for _, v := range bad {
		if got := normalizeImportedUID(v); got != "" {
			t.Errorf("%q 不是 UUID，应回空串，实际 %q", v, got)
		}
	}
}

// TestImportedUIDFallsBackToJWT 落库的 UserID 缺失时，UID() 必须能从 JWT 兜底。
func TestImportedUIDFallsBackToJWT(t *testing.T) {
	token := "eyJhbGciOiJSUzI1NiJ9." +
		"eyJzdWIiOiIyOTkzYWZiYi0wMGIyLTQ1NTItOTljNS0xZjEzMTFmZjgzZmMifQ.sig"
	acc := ImportedAccount{Name: "n", JWT: token, UserID: ""}.ToModel()
	if got := acc.UID(); got != "2993afbb-00b2-4552-99c5-1f1311ff83fc" {
		t.Fatalf("应从 JWT sub 兜底，实际 %q", got)
	}
	// 落库值优先
	acc2 := ImportedAccount{Name: "n", JWT: token, UserID: "explicit-uid"}.ToModel()
	if got := acc2.UID(); got != "explicit-uid" {
		t.Fatalf("落库 UserID 应优先，实际 %q", got)
	}
}
