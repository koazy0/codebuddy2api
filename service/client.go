package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"
)

type UpstreamClient struct {
	httpClient *http.Client
	// firstByteTimeout 只约束「上游是否在合理时间内开始响应」，
	// 不限制流式响应持续多久，避免长回答被总超时掐断。
	firstByteTimeout time.Duration
}

func NewUpstreamClient() *UpstreamClient {
	cfg := global.CORE_CONFIG.Gateway
	firstByteTimeout := time.Duration(cfg.Timeout()) * time.Second
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
		// 只限制「等到响应头」的时间，不限制流式 body 的持续时间。
		ResponseHeaderTimeout: firstByteTimeout,
		ForceAttemptHTTP2:     true,
	}
	if !cfg.TrustEnvProxy {
		transport.Proxy = nil
	}
	if strings.TrimSpace(cfg.Proxy) != "" {
		proxyURL, err := url.Parse(cfg.Proxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	// 关键：http.Client.Timeout 是「整个请求 + 读完 body」的总超时，
	// 流式对话一旦累计超过它就会被强制中断（表现就是「写着写着断了」）。
	// 所以这里只保留首包/响应头超时，总超时留空。
	return &UpstreamClient{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   0,
		},
		firstByteTimeout: firstByteTimeout,
	}
}

func (c *UpstreamClient) ChatCompletions(ctx context.Context, acc *model.Account, body []byte) (*http.Response, error) {
	return c.doJSON(ctx, http.MethodPost, "/v2/chat/completions", acc, body, true)
}

func (c *UpstreamClient) Completions(ctx context.Context, acc *model.Account, body []byte) (*http.Response, error) {
	return c.doJSON(ctx, http.MethodPost, "/v2/completions", acc, body, true)
}

func (c *UpstreamClient) RefreshToken(ctx context.Context, acc *model.Account) (*TokenRefreshResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(global.CORE_CONFIG.Gateway.UpstreamBase(), "/v2/plugin/auth/token/refresh"), strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	applyCodeBuddyHeaders(req, acc.JWT, "craft")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Refresh-Source", "plugin")
	if strings.TrimSpace(acc.RefreshToken) != "" {
		req.Header.Set("X-Refresh-Token", strings.TrimSpace(acc.RefreshToken))
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
		return nil, fmt.Errorf("refresh http %d: %s", resp.StatusCode, clip(raw, 300))
	}
	var parsed tokenRefreshResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("refresh parse: %w", err)
	}
	if parsed.Code != 0 {
		msg := parsed.Message
		if msg == "" {
			msg = parsed.Msg
		}
		return nil, fmt.Errorf("refresh api code %d: %s", parsed.Code, msg)
	}
	if strings.TrimSpace(parsed.Data.AccessToken) == "" {
		return nil, fmt.Errorf("refresh returned empty accessToken")
	}
	return &TokenRefreshResult{
		AccessToken:      parsed.Data.AccessToken,
		RefreshToken:     parsed.Data.RefreshToken,
		ExpiresIn:        parsed.Data.ExpiresIn,
		RefreshExpiresIn: parsed.Data.RefreshExpiresIn,
	}, nil
}

func (c *UpstreamClient) FetchConfig(ctx context.Context, acc *model.Account) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(global.CORE_CONFIG.Gateway.UpstreamBase(), "/v3/config"), nil)
	if err != nil {
		return nil, err
	}
	applyCodeBuddyHeaders(req, acc.JWT, global.CORE_CONFIG.CodeBuddy.HeaderAgentIntent())
	req.Header.Set("Accept", "application/json")
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
		return nil, fmt.Errorf("config http %d: %s", resp.StatusCode, clip(raw, 300))
	}
	return raw, nil
}

func (c *UpstreamClient) CheckAccount(ctx context.Context, acc *model.Account) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(global.CORE_CONFIG.Gateway.UpstreamBase(), "/v2/plugin/accounts"), nil)
	if err != nil {
		return err
	}
	applyCodeBuddyHeaders(req, acc.JWT, "craft")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health http %d: %s", resp.StatusCode, clip(raw, 300))
	}
	return nil
}

func (c *UpstreamClient) doJSON(ctx context.Context, method, path string, acc *model.Account, body []byte, sse bool) (*http.Response, error) {
	payload := body
	headersExtra := map[string]string{}
	if global.CORE_CONFIG.Gateway.Gzip {
		compressed, err := gzipBytes(body)
		if err != nil {
			return nil, err
		}
		payload = compressed
		headersExtra["Content-Encoding"] = "gzip"
	}
	req, err := http.NewRequestWithContext(ctx, method, joinURL(global.CORE_CONFIG.Gateway.UpstreamBase(), path), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	intent := global.CORE_CONFIG.CodeBuddy.HeaderAgentIntent()
	if path == "/v2/completions" {
		intent = "CodeCompletion"
	}
	applyCodeBuddyHeaders(req, acc.JWT, intent)
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	if sse {
		req.Header.Set("Accept", "text/event-stream")
	}
	for k, v := range headersExtra {
		req.Header.Set(k, v)
	}
	return c.httpClient.Do(req)
}

func applyCodeBuddyHeaders(req *http.Request, jwt, intent string) {
	cb := global.CORE_CONFIG.CodeBuddy
	version := cb.HeaderIDEVersion()
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(jwt))
	req.Header.Set("X-Agent-Intent", intent)
	req.Header.Set("X-IDE-Type", cb.HeaderIDEType())
	req.Header.Set("X-IDE-Version", version)
	req.Header.Set("X-Product-Version", cb.HeaderProductVersion())
	req.Header.Set("X-Env-ID", cb.HeaderEnvID())
	req.Header.Set("X-Domain", cb.HeaderDomain())
	req.Header.Set("X-Product", cb.HeaderProduct())
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s CodeBuddy/%s", cb.HeaderIDEType(), version, version))
	req.Header.Set("sec-fetch-mode", "cors")
}

func gzipBytes(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

func clip(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	return s[:n]
}

type TokenRefreshResult struct {
	AccessToken      string
	RefreshToken     string
	ExpiresIn        int
	RefreshExpiresIn int
}

type tokenRefreshResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Msg     string `json:"msg"`
	Data    struct {
		AccessToken      string `json:"accessToken"`
		RefreshToken     string `json:"refreshToken"`
		ExpiresIn        int    `json:"expiresIn"`
		RefreshExpiresIn int    `json:"refreshExpiresIn"`
	} `json:"data"`
}
