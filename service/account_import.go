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
	// UserID 是成长中心任务所需的 userId。导入 JSON 里常带（uid/userId），
	// 直接落库就省掉运行时再从 JWT 解析一遍。
	UserID string
}

func (a ImportedAccount) ToModel() *model.Account {
	return &model.Account{
		Name:          a.Name,
		JWT:           a.JWT,
		RefreshToken:  a.RefreshToken,
		SessionCookie: a.SessionCookie,
		Remark:        a.Remark,
		UserID:        a.UserID,
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
	items, err := ParseImportedJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return items, nil
}

func ParseImportedJSON(raw []byte) ([]ImportedAccount, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, fmt.Errorf("empty json")
	}
	var data any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	items := extractImportedAccounts(data)
	if len(items) == 0 {
		return nil, fmt.Errorf("no jwt/accessToken found")
	}
	return items, nil
}

func extractImportedAccounts(v any) []ImportedAccount {
	var found []ImportedAccount
	index := map[string]int{}
	add := func(item *ImportedAccount) {
		if item == nil {
			return
		}
		item.JWT = strings.TrimPrefix(strings.TrimSpace(item.JWT), "Bearer ")
		if item.JWT == "" {
			return
		}
		if i, ok := index[item.JWT]; ok {
			found[i] = mergeImported(found[i], *item)
			return
		}
		index[item.JWT] = len(found)
		found = append(found, *item)
	}
	var walk func(any, int)
	walk = func(v any, depth int) {
		if v == nil || depth > 12 {
			return
		}
		switch obj := v.(type) {
		case []any:
			for _, item := range obj {
				walk(item, depth+1)
			}
		case map[string]any:
			add(extractOneAccount(obj))
			for _, child := range obj {
				walk(child, depth+1)
			}
		}
	}
	walk(v, 0)
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
		obj["jwt"], obj["access_token"], obj["accessToken"], obj["AccessToken"],
		mapGet(auth, "accessToken"), mapGet(auth, "access_token"), mapGet(auth, "AccessToken"),
	)
	if jwt == "" {
		return nil
	}
	refresh := pickString(
		obj["refresh_token"], obj["refreshToken"], obj["RefreshToken"],
		mapGet(auth, "refreshToken"), mapGet(auth, "refresh_token"), mapGet(auth, "RefreshToken"),
	)
	name := pickString(
		obj["name"], obj["nickname"], obj["Nickname"],
		mapGet(account, "nickname"), mapGet(account, "Nickname"), mapGet(account, "name"),
	)
	uid := pickString(
		obj["uid"], obj["userId"], obj["user_id"], obj["username"], obj["preferred_username"],
		mapGet(account, "uid"), mapGet(account, "username"),
	)
	session := pickString(
		obj["session_cookie"], obj["sessionCookie"], obj["session"], obj["cookie"],
		mapGet(auth, "session"), mapGet(auth, "cookie"),
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
		UserID:        normalizeImportedUID(uid),
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
	if dst.UserID == "" {
		dst.UserID = src.UserID
	}
	return dst
}

// normalizeImportedUID 只接受形似 userId 的值（UUID 形态），
// 否则回空串交由运行时从 JWT 的 sub 解析。
//
// 原因：导入 JSON 里的 uid 字段语义不统一，实测既有真实 UUID，
// 也有邮箱/手机号/昵称。把手机号当 userId 上报会被上游当作错误身份
// 静默丢弃事件——那比"没有值"更糟，因为它看起来是成功的。
func normalizeImportedUID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// UUID 形态：8-4-4-4-12 的十六进制。
	if len(s) == 36 && s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-' {
		for i, r := range s {
			if i == 8 || i == 13 || i == 18 || i == 23 {
				continue
			}
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			if !isHex {
				return ""
			}
		}
		return s
	}
	return ""
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
