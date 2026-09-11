package service

import (
	"strings"
	"time"

	"codebuddy-gateway/model"
)

func HydrateAccount(acc *model.Account) {
	if acc == nil || acc.JWT == "" {
		return
	}
	acc.JWT = strings.TrimPrefix(strings.TrimSpace(acc.JWT), "Bearer ")
	if claims, err := ParseJWTClaims(acc.JWT); err == nil {
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
		if exp, err := JWTExpiry(acc.RefreshToken); err == nil {
			acc.RefreshExpiresAt = &exp
		}
	}
}

func UpsertAccount(acc *model.Account) (*model.Account, bool, error) {
	if acc == nil {
		return nil, false, nil
	}
	HydrateAccount(acc)
	if acc.JWT == "" {
		return nil, false, nil
	}
	if existing, err := model.GetAccountByJWT(acc.JWT); err == nil && existing != nil {
		return existing, false, nil
	} else if err != nil && !model.AccountNotFound(err) {
		return nil, false, err
	}
	if acc.Username != "" {
		if existing, err := model.GetAccountByUsername(acc.Username); err == nil && existing != nil {
			existing.JWT = acc.JWT
			if acc.RefreshToken != "" {
				existing.RefreshToken = acc.RefreshToken
			}
			if acc.SessionCookie != "" {
				existing.SessionCookie = acc.SessionCookie
			}
			if acc.Name != "" && (existing.Name == "" || existing.Name == existing.Username) {
				existing.Name = acc.Name
			}
			if acc.Remark != "" && existing.Remark == "" {
				existing.Remark = acc.Remark
			}
			existing.JWTExpiresAt = acc.JWTExpiresAt
			existing.RefreshExpiresAt = acc.RefreshExpiresAt
			existing.LastError = ""
			existing.FailCount = 0
			existing.Status = model.AccountStatusEnabled
			now := time.Now()
			existing.LastRefresh = &now
			if err := model.UpdateAccount(existing); err != nil {
				return nil, false, err
			}
			return existing, false, nil
		} else if err != nil && !model.AccountNotFound(err) {
			return nil, false, err
		}
	}
	if acc.Status == "" {
		acc.Status = model.AccountStatusEnabled
	}
	if acc.Weight <= 0 {
		acc.Weight = 1
	}
	if err := model.CreateAccount(acc); err != nil {
		return nil, false, err
	}
	return acc, true, nil
}
