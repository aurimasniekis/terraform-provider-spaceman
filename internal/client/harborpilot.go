package client

import (
	"context"
	"net/http"
	"time"
)

// ---- Harbor Pilot gateway (SpaceMan ADR-0060) ----

// HarborPilotConnection is central's Harbor Pilot connection. Its tokens are never returned: only whether one is
// stored and a counter SpaceMan increments each time one is written.
type HarborPilotConnection struct {
	ID                  string     `json:"id"`
	BaseURL             string     `json:"base_url"`
	RunBaseURL          string     `json:"run_base_url"`
	AllowInsecureHTTP   bool       `json:"allow_insecure_http"`
	HasParentToken      bool       `json:"has_parent_token"`
	ParentTokenVersion  int64      `json:"parent_token_version"`
	HasServiceToken     bool       `json:"has_service_token"`
	ServiceTokenVersion int64      `json:"service_token_version"`
	BindingID           string     `json:"binding_id"`
	ParentExpiresAt     *time.Time `json:"parent_expires_at,omitempty"`
	ValidatedAt         time.Time  `json:"validated_at"`
	EnabledSpaces       []string   `json:"enabled_spaces"`
	FailedRevocations   int64      `json:"failed_revocations"`
	Warnings            []string   `json:"warnings"`
	Version             int64      `json:"version"`
}

// HarborPilotConnectionInput is PUT /harbor-pilot/connection. An empty token keeps the stored one, except that a
// changed gateway address needs the tokens sent again.
type HarborPilotConnectionInput struct {
	BaseURL            string `json:"base_url"`
	RunBaseURL         string `json:"run_base_url,omitempty"`
	AllowInsecureHTTP  bool   `json:"allow_insecure_http,omitempty"`
	ParentToken        string `json:"parent_token,omitempty"`
	ServiceToken       string `json:"service_token,omitempty"`
	RemoveServiceToken bool   `json:"remove_service_token,omitempty"`
}

const harborPilotConnectionPath = "/harbor-pilot/connection"

// GetHarborPilotConnection is GET /harbor-pilot/connection (404 when none is configured).
func (c *Client) GetHarborPilotConnection(ctx context.Context) (*HarborPilotConnection, error) {
	var out HarborPilotConnection
	return &out, c.get(ctx, harborPilotConnectionPath, &out)
}

// PutHarborPilotConnection is PUT /harbor-pilot/connection: version 0 creates it (no If-Match), otherwise it replaces
// the connection at that version. SpaceMan validates every write against Harbor Pilot first.
func (c *Client) PutHarborPilotConnection(ctx context.Context, version int64, in HarborPilotConnectionInput) (*HarborPilotConnection, error) {
	var out HarborPilotConnection
	return &out, c.put(ctx, harborPilotConnectionPath, version, in, &out)
}

// DeleteHarborPilotConnection is DELETE /harbor-pilot/connection with If-Match; a 404 counts as success.
func (c *Client) DeleteHarborPilotConnection(ctx context.Context, version int64) error {
	err := c.do2(ctx, http.MethodDelete, harborPilotConnectionPath, version, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// SpaceHarborPilot is a space's Harbor Pilot opt-in. Stored is false while the space has none (SpaceMan's defaults).
type SpaceHarborPilot struct {
	SpaceID                   string   `json:"space_id"`
	Stored                    bool     `json:"stored"`
	Enabled                   bool     `json:"enabled"`
	ProjectID                 string   `json:"project_id"`
	Harnesses                 []string `json:"harnesses"`
	TTLSeconds                int64    `json:"ttl_seconds"`
	OnBudgetStatusUnavailable string   `json:"on_budget_status_unavailable"`
	AllowedModelAliases       []string `json:"allowed_model_aliases"`
	Connected                 bool     `json:"connected"`
	Warnings                  []string `json:"warnings"`
	Version                   int64    `json:"version"`
}

// SpaceHarborPilotInput is PUT /spaces/{id}/harbor-pilot (zero values take SpaceMan's defaults; an empty project_id
// on an update keeps the stored project).
type SpaceHarborPilotInput struct {
	Enabled                   bool     `json:"enabled"`
	ProjectID                 string   `json:"project_id,omitempty"`
	Harnesses                 []string `json:"harnesses,omitempty"`
	TTLSeconds                int64    `json:"ttl_seconds,omitempty"`
	OnBudgetStatusUnavailable string   `json:"on_budget_status_unavailable,omitempty"`
	AllowedModelAliases       []string `json:"allowed_model_aliases"`
}

func spaceHarborPilotPath(spaceID string) string { return "/spaces/" + esc(spaceID) + "/harbor-pilot" }

// GetSpaceHarborPilot is GET /spaces/{id}/harbor-pilot (the defaults, Stored false, when never saved).
func (c *Client) GetSpaceHarborPilot(ctx context.Context, spaceID string) (*SpaceHarborPilot, error) {
	var out SpaceHarborPilot
	return &out, c.get(ctx, spaceHarborPilotPath(spaceID), &out)
}

// PutSpaceHarborPilot is PUT /spaces/{id}/harbor-pilot: version 0 creates the settings, otherwise it replaces them.
func (c *Client) PutSpaceHarborPilot(ctx context.Context, spaceID string, version int64, in SpaceHarborPilotInput) (*SpaceHarborPilot, error) {
	if in.AllowedModelAliases == nil {
		in.AllowedModelAliases = []string{}
	}
	var out SpaceHarborPilot
	return &out, c.put(ctx, spaceHarborPilotPath(spaceID), version, in, &out)
}

// DeleteSpaceHarborPilot is DELETE /spaces/{id}/harbor-pilot with If-Match; a 404 counts as success.
func (c *Client) DeleteSpaceHarborPilot(ctx context.Context, spaceID string, version int64) error {
	err := c.do2(ctx, http.MethodDelete, spaceHarborPilotPath(spaceID), version, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
