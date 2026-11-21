package client

import (
	"context"
)

func fleetScopePath(spaceID string) string { return "/event-fleet-scope-grants/" + esc(spaceID) }

// GetFleetScopeGrant is GET /event-fleet-scope-grants/{space_id} (404 when the space holds none).
func (c *Client) GetFleetScopeGrant(ctx context.Context, spaceID string) (*FleetScopeGrant, error) {
	var out FleetScopeGrant
	return &out, c.get(ctx, fleetScopePath(spaceID), &out)
}

// PutFleetScopeGrant is PUT /event-fleet-scope-grants/{space_id}: idempotent, granting again replaces the reason. It
// needs system:write (an operator-level token) and is refused with 412 for an archived space.
func (c *Client) PutFleetScopeGrant(ctx context.Context, spaceID string, in FleetScopeInput) (*FleetScopeGrant, error) {
	var out FleetScopeGrant
	return &out, c.put(ctx, fleetScopePath(spaceID), 0, in, &out)
}

// DeleteFleetScopeGrant is DELETE /event-fleet-scope-grants/{space_id}; a space that holds none counts as revoked.
func (c *Client) DeleteFleetScopeGrant(ctx context.Context, spaceID string) error {
	return c.del(ctx, fleetScopePath(spaceID))
}
