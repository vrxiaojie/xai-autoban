package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultManagementURL       = "http://127.0.0.1:8317"
	defaultManagementKeyEnv    = "CPA_MANAGEMENT_KEY"
	defaultDisableHours        = 24
	defaultRequestTimeout      = 10 * time.Second
	defaultRetryInterval       = time.Minute
	defaultAuthFailureCooldown = 10 * time.Minute
	defaultStateFile           = "xai-autoban-state.json"
)

type runtimeConfig struct {
	Enabled             bool
	ManagementURL       string
	ManagementKey       string
	ManagementKeyEnv    string
	DisableDuration     time.Duration
	StatusCodes         map[int]struct{}
	RequestTimeout      time.Duration
	RetryInterval       time.Duration
	AuthFailureCooldown time.Duration
	StateFile           string
}

type rawRuntimeConfig struct {
	Enabled                    *bool  `yaml:"enabled"`
	ManagementURL              string `yaml:"management-url"`
	ManagementKey              string `yaml:"management-key"`
	ManagementKeyEnv           string `yaml:"management-key-env"`
	DisableHours               int    `yaml:"disable-hours"`
	StatusCodes                []int  `yaml:"status-codes"`
	RequestTimeoutSeconds      int    `yaml:"request-timeout-seconds"`
	RetryIntervalSeconds       int    `yaml:"retry-interval-seconds"`
	AuthFailureCooldownSeconds int    `yaml:"auth-failure-cooldown-seconds"`
	StateFile                  string `yaml:"state-file"`
}

func defaultRuntimeConfig() runtimeConfig {
	return runtimeConfig{
		Enabled:             true,
		ManagementURL:       defaultManagementURL,
		ManagementKeyEnv:    defaultManagementKeyEnv,
		DisableDuration:     defaultDisableHours * time.Hour,
		StatusCodes:         statusCodeSet([]int{401, 402, 403}),
		RequestTimeout:      defaultRequestTimeout,
		RetryInterval:       defaultRetryInterval,
		AuthFailureCooldown: defaultAuthFailureCooldown,
		StateFile:           defaultStateFile,
	}
}

func parseRuntimeConfig(raw []byte) (runtimeConfig, error) {
	cfg := defaultRuntimeConfig()
	if len(strings.TrimSpace(string(raw))) == 0 {
		return cfg, nil
	}

	var input rawRuntimeConfig
	if err := yaml.Unmarshal(raw, &input); err != nil {
		return runtimeConfig{}, fmt.Errorf("解析插件配置失败: %w", err)
	}
	if input.Enabled != nil {
		cfg.Enabled = *input.Enabled
	}
	if value := strings.TrimSpace(input.ManagementURL); value != "" {
		cfg.ManagementURL = strings.TrimRight(value, "/")
	}
	parsedURL, err := url.Parse(cfg.ManagementURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return runtimeConfig{}, fmt.Errorf("management-url 必须是有效的 http/https 地址")
	}
	cfg.ManagementKey = strings.TrimSpace(input.ManagementKey)
	if value := strings.TrimSpace(input.ManagementKeyEnv); value != "" {
		cfg.ManagementKeyEnv = value
	}
	if input.DisableHours > 0 {
		cfg.DisableDuration = time.Duration(input.DisableHours) * time.Hour
	}
	if len(input.StatusCodes) > 0 {
		cfg.StatusCodes = statusCodeSet(input.StatusCodes)
	}
	if input.RequestTimeoutSeconds > 0 {
		cfg.RequestTimeout = time.Duration(input.RequestTimeoutSeconds) * time.Second
	}
	if input.RetryIntervalSeconds > 0 {
		cfg.RetryInterval = time.Duration(input.RetryIntervalSeconds) * time.Second
	}
	if input.AuthFailureCooldownSeconds > 0 {
		cfg.AuthFailureCooldown = time.Duration(input.AuthFailureCooldownSeconds) * time.Second
	}
	if value := strings.TrimSpace(input.StateFile); value != "" {
		cfg.StateFile = filepath.Clean(value)
	}
	return cfg, nil
}

func (c runtimeConfig) managementKey() string {
	if c.ManagementKey != "" {
		return c.ManagementKey
	}
	if c.ManagementKeyEnv == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(c.ManagementKeyEnv))
}

func (c runtimeConfig) handlesStatus(status int) bool {
	_, ok := c.StatusCodes[status]
	return ok
}

func (c runtimeConfig) statusCodeList() []int {
	out := make([]int, 0, len(c.StatusCodes))
	for status := range c.StatusCodes {
		out = append(out, status)
	}
	sort.Ints(out)
	return out
}

func statusCodeSet(statuses []int) map[int]struct{} {
	out := make(map[int]struct{}, len(statuses))
	for _, status := range statuses {
		if status >= 100 && status <= 599 {
			out[status] = struct{}{}
		}
	}
	return out
}
