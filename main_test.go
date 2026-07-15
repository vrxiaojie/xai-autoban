package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xai-autoban/cpasdk/pluginapi"
)

func TestParseRuntimeConfig(t *testing.T) {
	cfg, err := parseRuntimeConfig([]byte("management-url: http://127.0.0.1:9000\ndisable-hours: 24\nstatus-codes: [401, 402, 429]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ManagementURL != "http://127.0.0.1:9000" || cfg.DisableDuration != 24*time.Hour {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if !cfg.handlesStatus(401) || !cfg.handlesStatus(402) || !cfg.handlesStatus(429) || cfg.handlesStatus(500) {
		t.Fatalf("unexpected status codes: %#v", cfg.statusCodeList())
	}
}

func TestDefaultRuntimeConfigQueues429(t *testing.T) {
	state := newBanState()
	controller := newAutobanController(state)
	controller.handleUsage(pluginapi.UsageRecord{
		Provider:  "xai",
		AuthID:    "rate-limited-auth",
		AuthIndex: "idx-429",
		Failed:    true,
		Failure:   pluginapi.UsageFailure{StatusCode: http.StatusTooManyRequests},
	})

	state.mu.Lock()
	entry, ok := state.bans["rate-limited-auth"]
	state.mu.Unlock()
	if !ok {
		t.Fatal("429 response should queue the credential for disabling by default")
	}
	if entry.StatusCode != http.StatusTooManyRequests || entry.Reason != "rate_limited" {
		t.Fatalf("unexpected 429 ban entry: %#v", entry)
	}
}

func TestPublicStatusPageHasNoManagementAuthentication(t *testing.T) {
	page := statusPage()
	for _, forbidden := range []string{"Authorization: Bearer", "Management key", "/v0/management/plugins/xai-autoban", "/v0/resource/plugins/xai-autoban"} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("page still contains authenticated management flow: %q", forbidden)
		}
	}
	for _, required := range []string{"window.location.pathname", "unbanSelected", "unbanStatus", "autoRefresh"} {
		if !strings.Contains(page, required) {
			t.Fatalf("page is missing %q", required)
		}
	}
}

func TestResourceRoutesUseHostPluginID(t *testing.T) {
	prefix := "/v0/resource/plugins/xai-autoban-linux-arm64"
	tests := []struct {
		name       string
		request    pluginapi.ManagementRequest
		wantStatus int
	}{
		{name: "status", request: pluginapi.ManagementRequest{Method: http.MethodGet, Path: prefix + "/status"}, wantStatus: http.StatusOK},
		{name: "data", request: pluginapi.ManagementRequest{Method: http.MethodGet, Path: prefix + "/data"}, wantStatus: http.StatusOK},
		{name: "action", request: pluginapi.ManagementRequest{Method: http.MethodGet, Path: prefix + "/action"}, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := dispatchManagement(tt.request)
			if response.StatusCode != tt.wantStatus {
				t.Fatalf("unexpected status for %s: got %d want %d body=%s", tt.request.Path, response.StatusCode, tt.wantStatus, response.Body)
			}
		})
	}
}

func TestPublicUnbanByStatus(t *testing.T) {
	bans.clearAll()
	now := time.Now()
	bans.set("payment", banEntry{StatusCode: 402, ResetAt: now.Add(time.Hour)})
	bans.set("forbidden", banEntry{StatusCode: 403, ResetAt: now.Add(time.Hour)})
	response := publicAction(pluginapi.ManagementRequest{Query: url.Values{"op": {"unban-status"}, "status": {"402"}}})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.StatusCode)
	}
	status := currentStatus()
	if status.Count != 1 || status.Bans[0].StatusCode != 403 {
		t.Fatalf("unexpected bans after action: %#v", status)
	}
}

func TestImportSnapshot(t *testing.T) {
	bans.clearAll()
	now := time.Now()
	snapshot := statusInfo{Bans: []banInfo{{AuthID: "restored", StatusCode: 429, Reason: "rate_limited", BannedAt: now.Format(time.RFC3339), ResetAt: now.Add(time.Hour).Format(time.RFC3339)}}}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	response := importSnapshot(raw)
	if response.StatusCode != http.StatusOK || currentStatus().Count != 1 {
		t.Fatalf("snapshot was not restored: response=%d status=%#v", response.StatusCode, currentStatus())
	}
}

func TestSchedulerFiltersBannedXAI(t *testing.T) {
	bans.clearAll()
	now := time.Now()
	bans.set("bad", banEntry{StatusCode: 402, ResetAt: now.Add(time.Hour)})
	req := pluginapi.SchedulerPickRequest{Candidates: []pluginapi.SchedulerAuthCandidate{
		{ID: "bad", Provider: "xai", Priority: 100},
		{ID: "good", Provider: "xai", Priority: 10},
	}}
	raw, _ := jsonMarshal(req)
	responseRaw, err := handleSchedulerPick(raw)
	if err != nil {
		t.Fatal(err)
	}
	var response envelope
	if err := jsonUnmarshal(responseRaw, &response); err != nil {
		t.Fatal(err)
	}
	var picked pluginapi.SchedulerPickResponse
	if err := jsonUnmarshal(response.Result, &picked); err != nil {
		t.Fatal(err)
	}
	if !picked.Handled || picked.AuthID != "good" {
		t.Fatalf("unexpected pick: %#v", picked)
	}
}

func TestManagementClientFallsBackToAuthIndex(t *testing.T) {
	var mu sync.Mutex
	patchNames := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v0/management/auth-files/status":
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			patchNames = append(patchNames, body.Name)
			mu.Unlock()
			if body.Name == "runtime-id" {
				http.Error(w, `{"error":"auth file not found"}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/auth-files":
			_, _ = w.Write([]byte(`{"files":[{"id":"physical-id","auth_index":"idx-1","name":"xai-account.json","provider":"xai","disabled":false}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := defaultRuntimeConfig()
	cfg.ManagementURL = server.URL
	cfg.ManagementKey = "secret"
	client := newManagementClient(cfg)
	if err := client.setAuthDisabled(context.Background(), "runtime-id", "idx-1", true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(patchNames) != 2 || patchNames[0] != "runtime-id" || patchNames[1] != "xai-account.json" {
		t.Fatalf("unexpected patch targets: %#v", patchNames)
	}
}

func TestControllerDisablesAndReenablesAfterExpiry(t *testing.T) {
	var mu sync.Mutex
	disabled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v0/management/auth-files/status":
			var body struct {
				Disabled bool `json:"disabled"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			disabled = body.Disabled
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/auth-files":
			mu.Lock()
			value := disabled
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{{"id": "xai-auth", "auth_index": "idx", "name": "xai.json", "provider": "xai", "disabled": value}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	state := newBanState()
	controller := newAutobanController(state)
	t.Cleanup(controller.shutdown)
	configYAML := "management-url: " + server.URL + "\nmanagement-key: secret\nstate-file: " + filepath.Join(t.TempDir(), "state.json") + "\n"
	if err := controller.configure([]byte(configYAML)); err != nil {
		t.Fatal(err)
	}
	controller.handleUsage(pluginapi.UsageRecord{Provider: "xai", AuthID: "xai-auth", AuthIndex: "idx", Failed: true, Failure: pluginapi.UsageFailure{StatusCode: 401}})
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return disabled
	})

	state.mu.Lock()
	entry := state.bans["xai-auth"]
	entry.ResetAt = time.Now().Add(-time.Second)
	entry.NextAttemptAt = time.Time{}
	state.bans["xai-auth"] = entry
	state.mu.Unlock()
	controller.signal()
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return !disabled
	})
	if state.active("xai-auth", time.Now()) {
		t.Fatal("credential should be removed from local ban state after re-enable")
	}
}

func TestStateReloadKeepsPendingReenable(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state.json")
	first := newBanState()
	if err := first.configure(stateFile); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first.set("persisted-auth", banEntry{AuthIndex: "idx-persisted", StatusCode: 402, Reason: "payment_required", BannedAt: now, ResetAt: now.Add(time.Hour)})
	first.finishAction(banAction{AuthID: "persisted-auth", AuthIndex: "idx-persisted", Disabled: true}, nil, now, time.Minute)

	second := newBanState()
	if err := second.configure(stateFile); err != nil {
		t.Fatal(err)
	}
	second.mu.Lock()
	entry := second.bans["persisted-auth"]
	entry.ResetAt = now.Add(-time.Second)
	second.bans["persisted-auth"] = entry
	second.mu.Unlock()
	actions := second.pendingActions(now)
	if len(actions) != 1 || actions[0].AuthID != "persisted-auth" || actions[0].Disabled {
		t.Fatalf("unexpected recovery actions: %#v", actions)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

var jsonMarshal = func(v any) ([]byte, error) { return json.Marshal(v) }
var jsonUnmarshal = func(data []byte, v any) error { return json.Unmarshal(data, v) }
