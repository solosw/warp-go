package acp

import "aimuxterm/config"

// AgentLaunch describes how to start an ACP agent process.
type AgentLaunch struct {
	Name          string
	Command       string
	Args          []string
	Env           map[string]string
	RemoteCommand string
	RemoteArgs    []string
}

func LaunchFromConfig(cfg config.AcpAgentConfig) AgentLaunch {
	return AgentLaunch{
		Name:          cfg.Name,
		Command:       cfg.Command,
		Args:          append([]string(nil), cfg.Args...),
		Env:           cloneEnv(cfg.Env),
		RemoteCommand: cfg.RemoteCommand,
		RemoteArgs:    append([]string(nil), cfg.RemoteArgs...),
	}
}

func cloneEnv(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// SSHParams is used when the ACP agent is launched over SSH.
type SSHParams struct {
	Host     string
	Port     int
	User     string
	Password string
	KeyPath  string
}

// PermissionOption is one choice on a session/request_permission prompt.
// Agents use this both for allow/reject and for multi-select (e.g. /model list).
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`
}

// PlanEntry is one todo from sessionUpdate "plan".
type PlanEntry struct {
	Content  string `json:"content"`
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`
}

// SessionMode is an agent conversation mode (ask / code / plan …).
type SessionMode struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// MediaItem is a safe, UI-ready attachment from an ACP content block
// (image / audio / video / linked resource). URL is either https? or a data: URL.
type MediaItem struct {
	Kind     string `json:"kind"` // image | video | audio | file
	URL      string `json:"url"`  // displayable src
	MimeType string `json:"mimeType,omitempty"`
	Alt      string `json:"alt,omitempty"`
	Name     string `json:"name,omitempty"`
}

// Event is a UI-facing ACP session event.
type Event struct {
	SessionID string `json:"sessionId"`

	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
	Status  string `json:"status,omitempty"`

	// Media accompanies message/thought events (native ACP image/audio/resource blocks).
	Media []MediaItem `json:"media,omitempty"`

	RequestID string             `json:"requestId,omitempty"`
	ToolName  string             `json:"toolName,omitempty"`
	ToolInput string             `json:"toolInput,omitempty"`
	Options   []PermissionOption `json:"options,omitempty"`

	ToolCallID string `json:"toolCallId,omitempty"`
	ToolKind   string `json:"toolKind,omitempty"`

	Commands []AvailableCommand `json:"commands,omitempty"`

	// plan
	PlanEntries []PlanEntry `json:"planEntries,omitempty"`

	// agent session modes
	Modes         []SessionMode `json:"modes,omitempty"`
	CurrentModeID string        `json:"currentModeId,omitempty"`

	// usage / context window (sessionUpdate usage_update + prompt usage)
	UsageUsed     int64   `json:"usageUsed,omitempty"`
	UsageSize     int64   `json:"usageSize,omitempty"`
	UsageCost     float64 `json:"usageCost,omitempty"`
	UsageCurrency string  `json:"usageCurrency,omitempty"`
}

// AvailableCommand is a slash command advertised by the agent.
type AvailableCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputHint   string `json:"inputHint,omitempty"`
}

// PermissionDecision is kept for compatibility with older call sites.
type PermissionDecision struct {
	RequestID string `json:"requestId"`
	OptionID  string `json:"optionId,omitempty"`
	Allow     bool   `json:"allow,omitempty"`
}
