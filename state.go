package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const stateSchemaVersion = 1

type banEntry struct {
	AuthIndex          string    `json:"auth_index,omitempty"`
	StatusCode         int       `json:"status_code"`
	Reason             string    `json:"reason"`
	BannedAt           time.Time `json:"banned_at"`
	ResetAt            time.Time `json:"reset_at"`
	ManagementDisabled bool      `json:"management_disabled"`
	LastAttemptAt      time.Time `json:"last_attempt_at,omitempty"`
	NextAttemptAt      time.Time `json:"next_attempt_at,omitempty"`
	LastError          string    `json:"last_error,omitempty"`
}

type persistedState struct {
	SchemaVersion int                 `json:"schema_version"`
	UpdatedAt     time.Time           `json:"updated_at"`
	Bans          map[string]banEntry `json:"bans"`
}

type banState struct {
	mu        sync.Mutex
	bans      map[string]banEntry
	stateFile string
}

type banAction struct {
	AuthID    string
	AuthIndex string
	Disabled  bool
}

func newBanState() *banState {
	return &banState{bans: make(map[string]banEntry)}
}

func (s *banState) configure(stateFile string) error {
	stateFile = strings.TrimSpace(stateFile)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bans == nil {
		s.bans = make(map[string]banEntry)
	}
	if stateFile == s.stateFile {
		return nil
	}
	s.stateFile = stateFile
	if stateFile == "" {
		return nil
	}
	raw, err := os.ReadFile(stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var snapshot persistedState
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	for authID, entry := range snapshot.Bans {
		authID = strings.TrimSpace(authID)
		if authID == "" || entry.ResetAt.IsZero() {
			continue
		}
		if current, ok := s.bans[authID]; !ok || current.ResetAt.Before(entry.ResetAt) || entry.ManagementDisabled {
			s.bans[authID] = entry
		}
	}
	return nil
}

func (s *banState) set(authID string, entry banEntry) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bans == nil {
		s.bans = make(map[string]banEntry)
	}
	if current, ok := s.bans[authID]; ok {
		if current.ResetAt.After(entry.ResetAt) {
			entry.ResetAt = current.ResetAt
		}
		entry.ManagementDisabled = current.ManagementDisabled
		entry.LastAttemptAt = current.LastAttemptAt
		entry.NextAttemptAt = current.NextAttemptAt
		entry.LastError = current.LastError
		if entry.AuthIndex == "" {
			entry.AuthIndex = current.AuthIndex
		}
	}
	s.bans[authID] = entry
	s.persistLocked()
}

func (s *banState) active(authID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.bans[authID]
	if !ok {
		return false
	}
	if !now.Before(entry.ResetAt) && !entry.ManagementDisabled {
		delete(s.bans, authID)
		s.persistLocked()
		return false
	}
	return true
}

func (s *banState) clear(authID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.bans[authID]
	delete(s.bans, authID)
	if ok {
		s.persistLocked()
	}
	return ok
}

func (s *banState) clearAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.bans)
	s.bans = make(map[string]banEntry)
	if n > 0 {
		s.persistLocked()
	}
	return n
}

func (s *banState) requestRelease(authIDs []string, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := 0
	for _, authID := range authIDs {
		authID = strings.TrimSpace(authID)
		entry, ok := s.bans[authID]
		if !ok {
			continue
		}
		changed++
		if !entry.ManagementDisabled {
			delete(s.bans, authID)
			continue
		}
		entry.ResetAt = now
		entry.NextAttemptAt = time.Time{}
		entry.LastError = ""
		s.bans[authID] = entry
	}
	if changed > 0 {
		s.persistLocked()
	}
	return changed
}

func (s *banState) requestReleaseStatus(status int, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := 0
	for authID, entry := range s.bans {
		if entry.StatusCode != status {
			continue
		}
		changed++
		if !entry.ManagementDisabled {
			delete(s.bans, authID)
			continue
		}
		entry.ResetAt = now
		entry.NextAttemptAt = time.Time{}
		entry.LastError = ""
		s.bans[authID] = entry
	}
	if changed > 0 {
		s.persistLocked()
	}
	return changed
}

func (s *banState) requestReleaseAll(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := len(s.bans)
	for authID, entry := range s.bans {
		if !entry.ManagementDisabled {
			delete(s.bans, authID)
			continue
		}
		entry.ResetAt = now
		entry.NextAttemptAt = time.Time{}
		entry.LastError = ""
		s.bans[authID] = entry
	}
	if changed > 0 {
		s.persistLocked()
	}
	return changed
}

func (s *banState) snapshot(now time.Time) map[string]banEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]banEntry)
	changed := false
	for authID, entry := range s.bans {
		if !now.Before(entry.ResetAt) && !entry.ManagementDisabled {
			delete(s.bans, authID)
			changed = true
			continue
		}
		out[authID] = entry
	}
	if changed {
		s.persistLocked()
	}
	return out
}

func (s *banState) pendingActions(now time.Time) []banAction {
	s.mu.Lock()
	defer s.mu.Unlock()
	actions := make([]banAction, 0)
	changed := false
	for authID, entry := range s.bans {
		if !entry.NextAttemptAt.IsZero() && now.Before(entry.NextAttemptAt) {
			continue
		}
		if !now.Before(entry.ResetAt) {
			if entry.ManagementDisabled {
				actions = append(actions, banAction{AuthID: authID, AuthIndex: entry.AuthIndex, Disabled: false})
			} else {
				delete(s.bans, authID)
				changed = true
			}
			continue
		}
		if !entry.ManagementDisabled {
			actions = append(actions, banAction{AuthID: authID, AuthIndex: entry.AuthIndex, Disabled: true})
		}
	}
	if changed {
		s.persistLocked()
	}
	return actions
}

func (s *banState) finishAction(action banAction, err error, now time.Time, retryInterval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.bans[action.AuthID]
	if !ok {
		return
	}
	entry.LastAttemptAt = now
	if err != nil {
		entry.LastError = err.Error()
		entry.NextAttemptAt = now.Add(retryInterval)
		s.bans[action.AuthID] = entry
		s.persistLocked()
		return
	}
	if action.Disabled {
		entry.ManagementDisabled = true
		entry.LastError = ""
		entry.NextAttemptAt = time.Time{}
		s.bans[action.AuthID] = entry
	} else {
		delete(s.bans, action.AuthID)
	}
	s.persistLocked()
}

func (s *banState) persistLocked() {
	if strings.TrimSpace(s.stateFile) == "" {
		return
	}
	dir := filepath.Dir(s.stateFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Error("xai-autoban: failed to create state directory", "error", err)
		return
	}
	raw, err := json.MarshalIndent(persistedState{SchemaVersion: stateSchemaVersion, UpdatedAt: time.Now(), Bans: s.bans}, "", "  ")
	if err != nil {
		slog.Error("xai-autoban: failed to encode state", "error", err)
		return
	}
	tmp := s.stateFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		slog.Error("xai-autoban: failed to write state", "error", err)
		return
	}
	if err := os.Rename(tmp, s.stateFile); err != nil {
		slog.Error("xai-autoban: failed to replace state", "error", err)
	}
}
