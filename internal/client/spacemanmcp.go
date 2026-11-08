package client

import (
	"context"
	"time"
)

// ---- SpaceMan MCP servers (kind "spaceman", SpaceMan ADR-0063) ----

// MCPKindHTTP and MCPKindSpaceman are the kinds of gateway MCP servers.
const (
	MCPKindHTTP     = "http"
	MCPKindSpaceman = "spaceman"
)

// SpacemanMCPServer is a gateway MCP server of kind spaceman: SpaceMan's own API as MCP tools, served in central through
// a secretless internal API token. Nothing in it is a secret.
type SpacemanMCPServer struct {
	MCPServer
	Spaceman *SpacemanMCPInfo `json:"spaceman,omitempty"`
}

// SpacemanMCPInfo is a SpaceMan MCP server's tool choice, approval mode and internal token.
type SpacemanMCPInfo struct {
	Preset       string       `json:"preset,omitempty"`
	Tools        []string     `json:"tools,omitempty"`
	Approval     string       `json:"approval,omitempty"`
	TokenID      string       `json:"token_id"`
	OwnerUserID  string       `json:"owner_user_id"`
	Grants       []GrantInput `json:"grants"`
	ExpiresAt    *time.Time   `json:"expires_at,omitempty"`
	NeverExpires bool         `json:"never_expires"`
	TokenState   string       `json:"token_state"`
}

// SpacemanMCPInput is POST /mcp-servers/spaceman. The server's internal token is owned by the caller, so the provider
// must use a user-issued API token (not the root token).
type SpacemanMCPInput struct {
	Slug           string       `json:"slug"`
	Name           string       `json:"name"`
	Description    string       `json:"description,omitempty"`
	Preset         string       `json:"preset,omitempty"`
	Tools          []string     `json:"tools,omitempty"`
	Approval       string       `json:"approval,omitempty"`
	Grants         []GrantInput `json:"grants,omitempty"`
	ExpiresInDays  int64        `json:"expires_in_days,omitempty"`
	CallTimeoutMS  int64        `json:"call_timeout_ms,omitempty"`
	MaxConcurrency int64        `json:"max_concurrency,omitempty"`
	MaxResultBytes int64        `json:"max_result_bytes,omitempty"`
	Enabled        *bool        `json:"enabled,omitempty"`
}

// SpacemanMCPUpdate is PUT /mcp-servers/{id}/spaceman: settings and limits (the token is unchanged).
type SpacemanMCPUpdate struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Preset         string   `json:"preset,omitempty"`
	Tools          []string `json:"tools,omitempty"`
	Approval       string   `json:"approval,omitempty"`
	CallTimeoutMS  int64    `json:"call_timeout_ms,omitempty"`
	MaxConcurrency int64    `json:"max_concurrency,omitempty"`
	MaxResultBytes int64    `json:"max_result_bytes,omitempty"`
	Enabled        bool     `json:"enabled"`
}

// SpacemanMCPToken is POST /mcp-servers/{id}/reissue: the grants and expiry of the new internal token. ExpiresInDays 0
// means a token that never expires.
type SpacemanMCPToken struct {
	Grants        []GrantInput `json:"grants,omitempty"`
	ExpiresInDays int64        `json:"expires_in_days,omitempty"`
}

// MCPServerCheck is the result of POST /mcp-servers/{id}/check: for a SpaceMan MCP server, the tools it offers runs
// (empty when its token can no longer be used).
type MCPServerCheck struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Tools []struct {
		Name     string `json:"name"`
		ReadOnly bool   `json:"read_only"`
		Usable   bool   `json:"usable"`
	} `json:"tools"`
}

// GetSpacemanMCPServer is GET /mcp-servers/{id} for any kind (check Kind).
func (c *Client) GetSpacemanMCPServer(ctx context.Context, id string) (*SpacemanMCPServer, error) {
	var out SpacemanMCPServer
	return &out, c.get(ctx, "/mcp-servers/"+esc(id), &out)
}

// CreateSpacemanMCPServer is POST /mcp-servers/spaceman (it mints the server's internal token).
func (c *Client) CreateSpacemanMCPServer(ctx context.Context, in SpacemanMCPInput) (*SpacemanMCPServer, error) {
	var out SpacemanMCPServer
	return &out, c.create(ctx, "/mcp-servers/spaceman", in, &out)
}

// UpdateSpacemanMCPServer is PUT /mcp-servers/{id}/spaceman.
func (c *Client) UpdateSpacemanMCPServer(ctx context.Context, id string, version int64, in SpacemanMCPUpdate) (*SpacemanMCPServer, error) {
	var out SpacemanMCPServer
	return &out, c.put(ctx, "/mcp-servers/"+esc(id)+"/spaceman", version, in, &out)
}

// ReissueSpacemanMCPServer is POST /mcp-servers/{id}/reissue: it revokes the internal token and mints a new one.
func (c *Client) ReissueSpacemanMCPServer(ctx context.Context, id string, version int64, in SpacemanMCPToken) (*SpacemanMCPServer, error) {
	var out SpacemanMCPServer
	return &out, c.post(ctx, "/mcp-servers/"+esc(id)+"/reissue", version, in, &out)
}

// CheckMCPServer is POST /mcp-servers/{id}/check.
func (c *Client) CheckMCPServer(ctx context.Context, id string) (*MCPServerCheck, error) {
	var out MCPServerCheck
	return &out, c.post(ctx, "/mcp-servers/"+esc(id)+"/check", 0, nil, &out)
}
