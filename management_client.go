package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var errManagementKeyMissing = errors.New("未配置 CPA Management Key")

type managementHTTPError struct {
	StatusCode int
	Body       string
}

func (e *managementHTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("Management API 返回 HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("Management API 返回 HTTP %d: %s", e.StatusCode, e.Body)
}

type managementClient struct {
	baseURL string
	key     string
	client  *http.Client
}

type managementAuthFile struct {
	ID        string `json:"id"`
	AuthIndex string `json:"auth_index"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Type      string `json:"type"`
	Disabled  bool   `json:"disabled"`
}

func newManagementClient(cfg runtimeConfig) *managementClient {
	baseURL := strings.TrimRight(cfg.ManagementURL, "/")
	if !strings.HasSuffix(baseURL, "/v0/management") {
		baseURL += "/v0/management"
	}
	return &managementClient{
		baseURL: baseURL,
		key:     cfg.managementKey(),
		client:  &http.Client{Timeout: cfg.RequestTimeout},
	}
}

func (c *managementClient) setAuthDisabled(ctx context.Context, authID, authIndex string, disabled bool) error {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return errors.New("Auth ID 为空")
	}
	err := c.patchAuthStatus(ctx, authID, disabled)
	if err == nil {
		return nil
	}

	var httpErr *managementHTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound && strings.TrimSpace(authIndex) != "" {
		if file, found, listErr := c.findAuthFile(ctx, authID, authIndex); listErr == nil && found {
			name := strings.TrimSpace(file.Name)
			if name == "" {
				name = file.ID
			}
			if name != "" && name != authID {
				err = c.patchAuthStatus(ctx, name, disabled)
				if err == nil {
					return nil
				}
			}
		}
	}

	if isManagementAuthError(err) {
		return err
	}
	file, found, verifyErr := c.findAuthFile(ctx, authID, authIndex)
	if verifyErr == nil && found && file.Disabled == disabled {
		return nil
	}
	return err
}

func (c *managementClient) patchAuthStatus(ctx context.Context, name string, disabled bool) error {
	body, err := json.Marshal(map[string]any{"name": name, "disabled": disabled})
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodPatch, "/auth-files/status", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("调用 Management API 失败: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &managementHTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(responseBody))}
	}
	return nil
}

func (c *managementClient) findAuthFile(ctx context.Context, authID, authIndex string) (managementAuthFile, bool, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/auth-files", nil)
	if err != nil {
		return managementAuthFile{}, false, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return managementAuthFile{}, false, fmt.Errorf("查询 Management API 账号列表失败: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return managementAuthFile{}, false, &managementHTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(responseBody))}
	}
	var payload struct {
		Files []managementAuthFile `json:"files"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return managementAuthFile{}, false, fmt.Errorf("解析 Management API 账号列表失败: %w", err)
	}
	authID = strings.TrimSpace(authID)
	authIndex = strings.TrimSpace(authIndex)
	for _, file := range payload.Files {
		if strings.TrimSpace(file.ID) == authID || (authIndex != "" && strings.TrimSpace(file.AuthIndex) == authIndex) {
			return file, true, nil
		}
	}
	return managementAuthFile{}, false, nil
}

func (c *managementClient) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("Management API 客户端未初始化")
	}
	if strings.TrimSpace(c.key) == "" {
		return nil, errManagementKeyMissing
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func isManagementAuthError(err error) bool {
	if errors.Is(err, errManagementKeyMissing) {
		return true
	}
	var httpErr *managementHTTPError
	return errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden)
}
