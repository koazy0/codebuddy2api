package service

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func makeJWT(exp int64, username string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(map[string]any{
		"preferred_username": username,
		"sub":                "user-1",
		"exp":                exp,
	})
	body := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + body + ".sig"
}

func TestParseJWTClaims(t *testing.T) {
	exp := time.Now().Add(24 * time.Hour).Unix()
	token := makeJWT(exp, "60794483")
	claims, err := ParseJWTClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.PreferredUsername != "60794483" {
		t.Fatalf("username=%s", claims.PreferredUsername)
	}
	got, err := JWTExpiry(token)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unix() != exp {
		t.Fatalf("exp=%d want=%d", got.Unix(), exp)
	}
}

func TestShouldRefresh(t *testing.T) {
	soon := makeJWT(time.Now().Add(2*time.Hour).Unix(), "a")
	later := makeJWT(time.Now().Add(48*time.Hour).Unix(), "b")
	if !ShouldRefresh(soon, 24*time.Hour) {
		t.Fatal("expected refresh for soon-expiring token")
	}
	if ShouldRefresh(later, 24*time.Hour) {
		t.Fatal("did not expect refresh for later token")
	}
}

func TestMaskToken(t *testing.T) {
	if MaskToken("") != "" {
		t.Fatal("empty")
	}
	got := MaskToken("abcdefghijklmnopqrstuvwxyz")
	if got == "abcdefghijklmnopqrstuvwxyz" {
		t.Fatal("should mask")
	}
}
