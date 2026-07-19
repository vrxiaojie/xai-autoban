package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"xai-autoban/cpasdk/pluginapi"
)

type controllerStatus struct {
	ManagementURL string
	LastError     string
	BlockedUntil  time.Time
}

type autobanController struct {
	state *banState

	mu           sync.RWMutex
	cfg          runtimeConfig
	client       *managementClient
	lastError    string
	blockedUntil time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	wake      chan struct{}
	stop      chan struct{}
	done      chan struct{}
}

func newAutobanController(state *banState) *autobanController {
	return &autobanController{
		state: state,
		cfg:   defaultRuntimeConfig(),
		wake:  make(chan struct{}, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
}

func (c *autobanController) configure(raw []byte) error {
	cfg, err := parseRuntimeConfig(raw)
	if err != nil {
		return err
	}
	if err := c.state.configure(cfg.StateFile); err != nil {
		return fmt.Errorf("加载状态文件失败: %w", err)
	}
	c.mu.Lock()
	c.cfg = cfg
	c.client = newManagementClient(cfg)
	c.lastError = ""
	c.blockedUntil = time.Time{}
	c.mu.Unlock()
	c.startOnce.Do(func() { go c.run() })
	c.signal()
	return nil
}

func (c *autobanController) config() runtimeConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

func (c *autobanController) status() controllerStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return controllerStatus{ManagementURL: c.cfg.ManagementURL, LastError: c.lastError, BlockedUntil: c.blockedUntil}
}

func (c *autobanController) handleUsage(record pluginapi.UsageRecord) {
	cfg := c.config()
	if !cfg.Enabled || !record.Failed || record.AuthID == "" || !cfg.handlesStatus(record.Failure.StatusCode) {
		return
	}
	now := time.Now()
	entry := banEntry{
		AuthIndex:  record.AuthIndex,
		StatusCode: record.Failure.StatusCode,
		Reason:     failureReason(record.Failure.StatusCode),
		BannedAt:   now,
		ResetAt:    now.Add(cfg.DisableDuration),
	}
	c.state.set(record.AuthID, entry)
	slog.Warn("xai-autoban: credential queued for management disable",
		"auth_id", record.AuthID,
		"status", entry.StatusCode,
		"reset_at", entry.ResetAt.Format(time.RFC3339),
	)
	c.signal()
}

func failureReason(status int) string {
	switch status {
	case 401:
		return "unauthorized"
	case 402:
		return "payment_required"
	case 403:
		return "forbidden"
	case 429:
		return "rate_limited"
	default:
		return fmt.Sprintf("http_%d", status)
	}
}

func (c *autobanController) requestRelease(authIDs []string) int {
	n := c.state.requestRelease(authIDs, time.Now())
	if n > 0 {
		c.signal()
	}
	return n
}

func (c *autobanController) requestReleaseStatus(status int) int {
	n := c.state.requestReleaseStatus(status, time.Now())
	if n > 0 {
		c.signal()
	}
	return n
}

func (c *autobanController) requestReleaseAll() int {
	n := c.state.requestReleaseAll(time.Now())
	if n > 0 {
		c.signal()
	}
	return n
}

func (c *autobanController) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *autobanController) run() {
	defer close(c.done)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.wake:
			c.process()
		case <-ticker.C:
			c.process()
		case <-c.stop:
			return
		}
	}
}

func (c *autobanController) process() {
	now := time.Now()
	c.mu.RLock()
	blockedUntil := c.blockedUntil
	c.mu.RUnlock()
	if now.Before(blockedUntil) {
		return
	}

	actions := c.state.pendingActions(now)
	for _, action := range actions {
		c.mu.RLock()
		cfg := c.cfg
		client := c.client
		blockedUntil = c.blockedUntil
		c.mu.RUnlock()
		if time.Now().Before(blockedUntil) {
			return
		}
		if client == nil {
			return
		}

		err := client.setAuthDisabled(context.Background(), action.AuthID, action.AuthIndex, action.Disabled)
		attemptedAt := time.Now()
		retryInterval := cfg.RetryInterval
		if isManagementAuthError(err) {
			retryInterval = cfg.AuthFailureCooldown
			c.mu.Lock()
			c.blockedUntil = attemptedAt.Add(cfg.AuthFailureCooldown)
			c.lastError = err.Error()
			c.mu.Unlock()
		}
		c.state.finishAction(action, err, attemptedAt, retryInterval)
		if err != nil {
			slog.Error("xai-autoban: management status update failed",
				"auth_id", action.AuthID,
				"disabled", action.Disabled,
				"error", err,
			)
			if isManagementAuthError(err) {
				return
			}
			continue
		}
		c.mu.Lock()
		c.lastError = ""
		c.mu.Unlock()
		if action.Disabled {
			slog.Warn("xai-autoban: credential disabled through Management API", "auth_id", action.AuthID)
		} else {
			slog.Info("xai-autoban: credential re-enabled through Management API", "auth_id", action.AuthID)
		}
	}
}


// deleteCredentials permanently removes auth files for the given IDs via Management API.
// Only credentials currently tracked with statusCode are deleted when statusCode > 0.
// Unlike release/unban, this does not re-enable the credential.
func (c *autobanController) deleteCredentials(authIDs []string, statusCode int) (int, error) {
	ids := make([]string, 0, len(authIDs))
	for _, id := range authIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}

	c.mu.RLock()
	client := c.client
	blockedUntil := c.blockedUntil
	c.mu.RUnlock()
	if client == nil {
		return 0, errors.New("Management API 客户端未初始化")
	}
	if time.Now().Before(blockedUntil) {
		return 0, fmt.Errorf("Management API 冷却中，请稍后再试")
	}

	entries := c.state.lookup(ids)
	deleted := 0
	var firstErr error
	for _, id := range ids {
		entry, ok := entries[id]
		if !ok {
			continue
		}
		if statusCode > 0 && entry.StatusCode != statusCode {
			continue
		}
		err := client.deleteAuthFile(context.Background(), id, entry.AuthIndex)
		if err != nil {
			if isManagementAuthError(err) {
				c.mu.Lock()
				c.blockedUntil = time.Now().Add(c.cfg.AuthFailureCooldown)
				c.lastError = err.Error()
				c.mu.Unlock()
				if firstErr == nil {
					firstErr = err
				}
				break
			}
			slog.Error("xai-autoban: management delete failed", "auth_id", id, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		_ = c.state.clear(id)
		deleted++
		slog.Warn("xai-autoban: credential deleted through Management API", "auth_id", id, "status", entry.StatusCode)
		c.mu.Lock()
		c.lastError = ""
		c.mu.Unlock()
	}
	if deleted == 0 && firstErr != nil {
		return 0, firstErr
	}
	return deleted, firstErr
}

func (c *autobanController) deleteByStatus(statusCode int) (int, error) {
	if statusCode <= 0 {
		return 0, fmt.Errorf("invalid status code")
	}
	ids := c.state.authIDsByStatus(statusCode)
	return c.deleteCredentials(ids, statusCode)
}

func (c *autobanController) shutdown() {
	c.stopOnce.Do(func() { close(c.stop) })
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
}
