package client

import (
	"context"
	"net/http"
)

// GitKey is a shared Git SSH key (GET /git-ssh-keys/{id}, SpaceMan ADR-0058); its private half is never returned.
type GitKey struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	PublicKey   string   `json:"public_key"`
	Fingerprint string   `json:"fingerprint"`
	AllSpaces   bool     `json:"all_spaces"`
	Spaces      []string `json:"spaces"`
	UsedBy      []struct {
		RepositoryID string `json:"repository_id"`
		SpaceID      string `json:"space_id"`
	} `json:"used_by"`
	Version int64 `json:"version"`
}

// GitKeyInput creates or edits a shared Git SSH key.
type GitKeyInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	AllSpaces   bool     `json:"all_spaces,omitempty"`
	Spaces      []string `json:"spaces,omitempty"`
}

func gitKeyPath(id string) string { return "/git-ssh-keys/" + esc(id) }

// GetGitKey is GET /git-ssh-keys/{id}.
func (c *Client) GetGitKey(ctx context.Context, id string) (*GitKey, error) {
	var out GitKey
	return &out, c.get(ctx, gitKeyPath(id), &out)
}

// CreateGitKey is POST /git-ssh-keys (SpaceMan generates the key; only the public half comes back).
func (c *Client) CreateGitKey(ctx context.Context, in GitKeyInput) (*GitKey, error) {
	var out GitKey
	return &out, c.do2(ctx, http.MethodPost, "/git-ssh-keys", 0, in, &out)
}

// UpdateGitKey is PUT /git-ssh-keys/{id} (If-Match required).
func (c *Client) UpdateGitKey(ctx context.Context, id string, version int64, in GitKeyInput) (*GitKey, error) {
	var out GitKey
	return &out, c.put(ctx, gitKeyPath(id), version, in, &out)
}

// RotateGitKey is POST /git-ssh-keys/{id}/rotate.
func (c *Client) RotateGitKey(ctx context.Context, id string, version int64) (*GitKey, error) {
	var out GitKey
	return &out, c.post(ctx, gitKeyPath(id)+"/rotate", version, nil, &out)
}

// DeleteGitKey is DELETE /git-ssh-keys/{id} (refused while a repository uses the key).
func (c *Client) DeleteGitKey(ctx context.Context, id string) error {
	return c.del(ctx, gitKeyPath(id))
}
