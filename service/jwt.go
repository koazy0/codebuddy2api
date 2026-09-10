package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type JWTClaims struct {
	Sub               string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	Exp               int64  `json:"exp"`
	Iat               int64  `json:"iat"`
	Iss               string `json:"iss"`
	Azp               string `json:"azp"`
}

func ParseJWTClaims(token string) (*JWTClaims, error) {
	token = strings.TrimSpace(token)
	token = strings.TrimPrefix(token, "Bearer ")
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid jwt format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, fmt.Errorf("decode jwt payload: %w", err)
		}
	}
	var claims JWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal jwt claims: %w", err)
	}
	return &claims, nil
}

func JWTExpiry(token string) (time.Time, error) {
	claims, err := ParseJWTClaims(token)
	if err != nil {
		return time.Time{}, err
	}
	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("jwt missing exp")
	}
	return time.Unix(claims.Exp, 0), nil
}

func ShouldRefresh(token string, threshold time.Duration) bool {
	exp, err := JWTExpiry(token)
	if err != nil {
		return true
	}
	return time.Until(exp) <= threshold
}

func MaskToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 16 {
		return "****"
	}
	return token[:8] + "..." + token[len(token)-6:]
}
