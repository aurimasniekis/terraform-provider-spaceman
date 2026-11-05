package client

import (
	"context"
	"net/http"
)

// SpaceRepository is a space's configured repository (GET /spaces/{id}/repositories/{repository}, ADR-0044). The node's
// report (status) is runtime state and is not decoded.
type SpaceRepository struct {
	ID            string `json:"id"`
	SpaceID       string `json:"space_id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	Description   string `json:"description"`
	URL           string `json:"url"`
	DefaultBranch string `json:"default_branch"`
	Role          string `json:"role"`
	ClonePolicy   string `json:"clone_policy"`
	KnownHosts    string `json:"known_hosts"`
	SSHKey        *struct {
		PublicKey   string `json:"public_key"`
		Fingerprint string `json:"fingerprint"`
	} `json:"ssh_key,omitempty"`
	Version int64 `json:"version"`
}

// SpaceRepositoryInput creates or edits a repository. Name and Path are sent on create only (they are fixed).
type SpaceRepositoryInput struct {
	Name          string `json:"name,omitempty"`
	Path          string `json:"path,omitempty"`
	Description   string `json:"description,omitempty"`
	URL           string `json:"url,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Role          string `json:"role,omitempty"`
	ClonePolicy   string `json:"clone_policy,omitempty"`
	KnownHosts    string `json:"known_hosts,omitempty"`
}

func repoPath(spaceID, id string) string {
	return "/spaces/" + esc(spaceID) + "/repositories/" + esc(id)
}

// GetSpaceRepository is GET /spaces/{id}/repositories/{repository}.
func (c *Client) GetSpaceRepository(ctx context.Context, spaceID, id string) (*SpaceRepository, error) {
	var out SpaceRepository
	return &out, c.get(ctx, repoPath(spaceID, id), &out)
}

// CreateSpaceRepository is POST /spaces/{id}/repositories. The operation takes no Idempotency-Key, so it is never
// retried (a repeat would be refused as a duplicate name).
func (c *Client) CreateSpaceRepository(ctx context.Context, spaceID string, in SpaceRepositoryInput) (*SpaceRepository, error) {
	var out SpaceRepository
	return &out, c.do2(ctx, http.MethodPost, "/spaces/"+esc(spaceID)+"/repositories", 0, in, &out)
}

// UpdateSpaceRepository is PUT /spaces/{id}/repositories/{repository} (If-Match required).
func (c *Client) UpdateSpaceRepository(ctx context.Context, spaceID, id string, version int64, in SpaceRepositoryInput) (*SpaceRepository, error) {
	var out SpaceRepository
	return &out, c.put(ctx, repoPath(spaceID, id), version, in, &out)
}

// GenerateRepositorySSHKey is POST /spaces/{id}/repositories/{repository}/ssh-key: a new SSH key (rotation when one
// exists); only the public key comes back (SpaceMan ADR-0057).
func (c *Client) GenerateRepositorySSHKey(ctx context.Context, spaceID, id string, version int64) (*SpaceRepository, error) {
	var out SpaceRepository
	return &out, c.post(ctx, repoPath(spaceID, id)+"/ssh-key", version, nil, &out)
}

// RemoveRepositorySSHKey is DELETE /spaces/{id}/repositories/{repository}/ssh-key.
func (c *Client) RemoveRepositorySSHKey(ctx context.Context, spaceID, id string, version int64) (*SpaceRepository, error) {
	var out SpaceRepository
	return &out, c.do2(ctx, http.MethodDelete, repoPath(spaceID, id)+"/ssh-key", version, nil, &out)
}

// DeleteSpaceRepository is DELETE /spaces/{id}/repositories/{repository}; the folder on the node stays.
func (c *Client) DeleteSpaceRepository(ctx context.Context, spaceID, id string) error {
	return c.del(ctx, repoPath(spaceID, id))
}
