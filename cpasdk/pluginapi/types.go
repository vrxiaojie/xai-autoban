package pluginapi

import (
	"net/http"
	"net/url"
	"time"
)

const (
	SchedulerBuiltinRoundRobin = "round-robin"
	SchedulerBuiltinFillFirst  = "fill-first"
)

type Metadata struct {
	Name             string
	Version          string
	Author           string
	GitHubRepository string
	Logo             string
	ConfigFields     []ConfigField
}

type ConfigFieldType string

const (
	ConfigFieldTypeString  ConfigFieldType = "string"
	ConfigFieldTypeInteger ConfigFieldType = "integer"
	ConfigFieldTypeArray   ConfigFieldType = "array"
)

type ConfigField struct {
	Name        string
	Type        ConfigFieldType
	EnumValues  []string
	Description string
}

type SchedulerPickRequest struct {
	Plugin     Metadata
	Provider   string
	Providers  []string
	Model      string
	Stream     bool
	Options    SchedulerOptions
	Candidates []SchedulerAuthCandidate
}

type SchedulerOptions struct {
	Headers  map[string][]string
	Metadata map[string]any
}

type SchedulerAuthCandidate struct {
	ID         string
	Provider   string
	Priority   int
	Status     string
	Attributes map[string]string
	Metadata   map[string]any
}

type SchedulerPickResponse struct {
	AuthID          string
	DelegateBuiltin string
	Handled         bool
}

type ManagementRegistrationResponse struct {
	Routes    []ManagementRoute
	Resources []ResourceRoute
}

type ManagementRoute struct {
	Method      string
	Path        string
	Menu        string
	Description string
}

type ResourceRoute struct {
	Path        string
	Menu        string
	Description string
}

type ManagementRequest struct {
	Method  string
	Path    string
	Headers http.Header
	Query   url.Values
	Body    []byte
}

type ManagementResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

type UsageRecord struct {
	Provider        string
	ExecutorType    string
	Model           string
	Alias           string
	APIKey          string
	AuthID          string
	AuthIndex       string
	AuthType        string
	Source          string
	ReasoningEffort string
	ServiceTier     string
	RequestedAt     time.Time
	Latency         time.Duration
	TTFT            time.Duration
	Failed          bool
	Failure         UsageFailure
	Detail          UsageDetail
	ResponseHeaders http.Header
}

type UsageFailure struct {
	StatusCode int
	Body       string
}

type UsageDetail struct {
	InputTokens         int64
	OutputTokens        int64
	ReasoningTokens     int64
	CachedTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	TotalTokens         int64
}
