package service

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
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
