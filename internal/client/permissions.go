package client

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

// ---- human-action type permissions (SM-HUI-008) ----

// HumanTypePermission says who may see, create and respond to one human-action type in a space. ActionType is a
// built-in type or a custom type's ID; Stored is false when no permission is stored and the defaults apply.
type HumanTypePermission struct {
	SpaceID     string   `json:"space_id"`
	ActionType  string   `json:"action_type"`
	TypeSlug    string   `json:"type_slug,omitempty"`
	ViewRole    string   `json:"view_role"`
	CreateRole  string   `json:"create_role"`
	RespondRole string   `json:"respond_role"`
	Agents      string   `json:"agents"`
	AgentRoles  []string `json:"agent_roles"`
	Stored      bool     `json:"stored"`
	Version     int64    `json:"version"`
}

// HumanTypePermissionInput is the body of PUT /spaces/{id}/human-type-permissions/{type}; empty fields take the defaults.
type HumanTypePermissionInput struct {
	ViewRole    string   `json:"view_role,omitempty"`
	CreateRole  string   `json:"create_role,omitempty"`
	RespondRole string   `json:"respond_role,omitempty"`
	Agents      string   `json:"agents,omitempty"`
	AgentRoles  []string `json:"agent_roles,omitempty"`
}

func humanTypePermissionPath(spaceID, actionType string) string {
	return "/spaces/" + esc(spaceID) + "/human-type-permissions/" + esc(actionType)
}

// GetHumanTypePermission is GET …/human-type-permissions/{type}: the permission in force (Stored false: the defaults).
func (c *Client) GetHumanTypePermission(ctx context.Context, spaceID, actionType string) (*HumanTypePermission, error) {
	var out HumanTypePermission
	return &out, c.get(ctx, humanTypePermissionPath(spaceID, actionType), &out)
}

// SetHumanTypePermission is PUT …/human-type-permissions/{type}: version 0 creates (refused with 409 when one is
// stored), otherwise it updates at that version.
func (c *Client) SetHumanTypePermission(ctx context.Context, spaceID, actionType string, version int64, in HumanTypePermissionInput) (*HumanTypePermission, error) {
	var out HumanTypePermission
	h, err := c.do(ctx, request{method: http.MethodPut, path: humanTypePermissionPath(spaceID, actionType), body: in, ifMatch: version}, &out)
	if err == nil && out.Version == 0 {
		if v, perr := strconv.ParseInt(strings.Trim(h.Get("ETag"), `"`), 10, 64); perr == nil {
			out.Version = v
		}
	}
	return &out, err
}

// DeleteHumanTypePermission is DELETE …/human-type-permissions/{type} (already gone counts as success).
func (c *Client) DeleteHumanTypePermission(ctx context.Context, spaceID, actionType string) error {
	return c.del(ctx, humanTypePermissionPath(spaceID, actionType))
}
