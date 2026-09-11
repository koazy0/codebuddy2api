package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codebuddy-gateway/model"
)

type ImportedAccount struct {
	Name          string
	JWT           string
	RefreshToken  string
	SessionCookie string
	Remark        string
}

func (a ImportedAccount) ToModel() *model.Account {
	return &model.Account{
		Name:          a.Name,
		JWT:           a.JWT,
		RefreshToken:  a.RefreshToken,
		SessionCookie: a.SessionCookie,
		Remark:        a.Remark,
		Status:        model.AccountStatusEnabled,
		Weight:        1,
	}
}

func LoadImportedAccounts(path string) ([]ImportedAccount, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		matches, err := filepath.Glob(filepath.Join(path, "*.json"))
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no json files in %s", path)
		}
		var all []ImportedAccount
		for _, file := range matches {
			items, err := LoadImportedAccounts(file)
			if err != nil {
				return nil, err
			}
			all = append(all, items...)
		}
		return dedupeImported(all), nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	items := extractImportedAccounts(data)
	if len(items) == 0 {
		return nil, fmt.Errorf("no jwt/accessToken found in %s", path)
	}
	return dedupeImported(items), nil
}

func extractImportedAccounts(v any) []ImportedAccount {
	var found []ImportedAccount
	index := map[string]int{}
	add := func(item *ImportedAccount) {
		if item == nil || item.JWT == "" {
			return
		}
		if i, ok := index[item.JWT]; ok {
			found[i] = mergeImported(found[i], *item)
			return
		}
		index[item.JWT] = len(found)
		found = append(found, *item)
	}
	switch obj := v.(type) {
	case []any:
		for _, item := range obj {
			for _, one := range extractImportedAccounts(item) {
				cp := one
				add(&cp)
			}
		}
	case map[string]any:
		for _, key := range []string{"accounts", "allAccounts", "data", "items"} {
			if child, ok := obj[key].([]any); ok {
				for _, item := range child {
					add(extractOneAccount(item))
					if m, ok := item.(map[string]any); ok {
						merged := map[string]any{}
						for k, val := range m {
							merged[k] = val
						}
						if auth, ok := m["auth"]; ok {
							merged["auth"] = auth
						}
						if account, ok := m["account"]; ok {
							merged["account"] = account
						}
						add(extractOneAccount(merged))
					}
				}
			}
		}
		add(extractOneAccount(obj))
	}
	return found
}

func extractOneAccount(v any) *ImportedAccount {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	auth, _ := obj["auth"].(map[string]any)
	account, _ := obj["account"].(map[string]any)
	jwt := pickString(
		obj["jwt"], obj["access_token"], obj["accessToken"],
		mapGet(auth, "accessToken"), mapGet(auth, "access_token"),
	)
	if jwt == "" {
		return nil
	}
	refresh := pickString(
		obj["refresh_token"], obj["refreshToken"],
		mapGet(auth, "refreshToken"), mapGet(auth, "refresh_token"),
	)
	name := pickString(obj["name"], obj["nickname"], mapGet(account, "nickname"))
	uid := pickString(obj["uid"], mapGet(account, "uid"), obj["username"])
	session := pickString(
		obj["session_cookie"], obj["sessionCookie"], obj["session"], obj["cookie"],
	)
	if name == "" {
		name = uid
	}
	if name == "" {
		name = "imported"
	}
	return &ImportedAccount{
		Name:          name,
		JWT:           jwt,
		RefreshToken:  refresh,
		SessionCookie: session,
		Remark:        uid,
	}
}

func pickString(values ...any) string {
	for _, v := range values {
		s, ok := v.(string)
		if ok {
			s = strings.TrimSpace(s)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func mapGet(m map[string]any, key string) any {
	if m == nil {
		return nil
	}
	return m[key]
}

func mergeImported(dst, src ImportedAccount) ImportedAccount {
	if dst.Name == "" || dst.Name == "imported" {
		if src.Name != "" && src.Name != "imported" {
			dst.Name = src.Name
		}
	}
	if dst.RefreshToken == "" {
		dst.RefreshToken = src.RefreshToken
	}
	if dst.SessionCookie == "" {
		dst.SessionCookie = src.SessionCookie
	}
	if dst.Remark == "" {
		dst.Remark = src.Remark
	}
	return dst
}

func dedupeImported(items []ImportedAccount) []ImportedAccount {
	seen := map[string]struct{}{}
	out := make([]ImportedAccount, 0, len(items))
	for _, item := range items {
		if item.JWT == "" {
			continue
		}
		if _, ok := seen[item.JWT]; ok {
			continue
		}
		seen[item.JWT] = struct{}{}
		out = append(out, item)
	}
	return out
}
