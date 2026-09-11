package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"codebuddy-gateway/global"
)

const (
	deviceAuthPendingCode = 10008
)

type DeviceAuthSession struct {
	State   string
	AuthURL string
}

type DeviceAuthToken struct {
	AccessToken      string
	RefreshToken     string
	ExpiresIn        int
	RefreshExpiresIn int
}

type deviceAuthEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Msg     string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

func (c *UpstreamClient) StartDeviceAuth(ctx context.Context, platform string) (*DeviceAuthSession, error) {
	platform = strings.TrimSpace(platform)
	if platform == "" {
		platform = "desktop"
	}
	query := url.Values{"platform": {platform}}
	path := "/v2/plugin/auth/state?" + query.Encode()
	raw, err := c.doNoAuthJSON(ctx, http.MethodPost, path, []byte("{}"))
	if err != nil {
		return nil, err
	}
	return parseAuthState(raw)
}

func (c *UpstreamClient) PollDeviceAuth(ctx context.Context, state string) (bool, *DeviceAuthToken, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return false, nil, fmt.Errorf("state is required")
	}
	path := "/v2/plugin/auth/token?state=" + url.QueryEscape(state)
	raw, err := c.doNoAuthJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, nil, err
	}
	return parseAuthToken(raw)
}

func (c *UpstreamClient) WaitDeviceAuth(ctx context.Context, state string, interval time.Duration) (*DeviceAuthToken, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		pending, token, err := c.PollDeviceAuth(ctx, state)
		if err != nil {
			return nil, err
		}
		if !pending {
			return token, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *UpstreamClient) doNoAuthJSON(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, joinURL(global.CORE_CONFIG.Gateway.UpstreamBase(), path), reader)
	if err != nil {
		return nil, err
	}
	applyNoAuthHeaders(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth http %d: %s", resp.StatusCode, clip(raw, 300))
	}
	return raw, nil
}

func applyNoAuthHeaders(req *http.Request) {
	cb := global.CORE_CONFIG.CodeBuddy
	version := cb.HeaderIDEVersion()
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-No-Authorization", "true")
	req.Header.Set("X-No-User-Id", "true")
	req.Header.Set("X-No-Enterprise-Id", "true")
	req.Header.Set("X-No-Department-Info", "true")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Domain", cb.HeaderDomain())
	req.Header.Set("X-Product", cb.HeaderProduct())
	req.Header.Set("X-IDE-Type", cb.HeaderIDEType())
	req.Header.Set("X-IDE-Version", version)
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s CodeBuddy/%s", cb.HeaderIDEType(), version, version))
}

func parseAuthState(raw []byte) (*DeviceAuthSession, error) {
	env, err := parseDeviceAuthEnvelope(raw)
	if err != nil {
		return nil, err
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("auth state code %d: %s", env.Code, env.message())
	}
	var data struct {
		State   string `json:"state"`
		AuthURL string `json:"authUrl"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, fmt.Errorf("auth state parse: %w", err)
	}
	if strings.TrimSpace(data.State) == "" || strings.TrimSpace(data.AuthURL) == "" {
		return nil, fmt.Errorf("auth state missing state/authUrl")
	}
	return &DeviceAuthSession{State: data.State, AuthURL: data.AuthURL}, nil
}

func parseAuthToken(raw []byte) (bool, *DeviceAuthToken, error) {
	env, err := parseDeviceAuthEnvelope(raw)
	if err != nil {
		return false, nil, err
	}
	if env.Code == deviceAuthPendingCode {
		return true, nil, nil
	}
	if env.Code != 0 {
		return false, nil, fmt.Errorf("auth token code %d: %s", env.Code, env.message())
	}
	var data struct {
		AccessToken      string `json:"accessToken"`
		RefreshToken     string `json:"refreshToken"`
		ExpiresIn        int    `json:"expiresIn"`
		RefreshExpiresIn int    `json:"refreshExpiresIn"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return false, nil, fmt.Errorf("auth token parse: %w", err)
	}
	if strings.TrimSpace(data.AccessToken) == "" {
		return false, nil, fmt.Errorf("auth token missing accessToken")
	}
	return false, &DeviceAuthToken{
		AccessToken:      data.AccessToken,
		RefreshToken:     data.RefreshToken,
		ExpiresIn:        data.ExpiresIn,
		RefreshExpiresIn: data.RefreshExpiresIn,
	}, nil
}

func parseDeviceAuthEnvelope(raw []byte) (*deviceAuthEnvelope, error) {
	var env deviceAuthEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("auth parse: %w", err)
	}
	return &env, nil
}

func (e deviceAuthEnvelope) message() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	if strings.TrimSpace(e.Msg) != "" {
		return e.Msg
	}
	return "unknown error"
}
