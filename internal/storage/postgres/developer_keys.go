package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrInvalidDeveloperAPIKey = errors.New("invalid developer API key")

func (p *Platform) EnsureDeveloperAPIKey(ctx context.Context, developerID, displayName, apiKey string) error {
	if !definitionNamePattern.MatchString(developerID) {
		return fmt.Errorf("developer id %q must match %s", developerID, definitionNamePattern)
	}
	if strings.TrimSpace(displayName) == "" {
		return fmt.Errorf("developer API key display name must not be empty")
	}
	if len(apiKey) < 16 {
		return fmt.Errorf("developer API key must contain at least 16 characters")
	}
	hash := sha256.Sum256([]byte(apiKey))
	keyID := developerID + ":" + displayName
	_, err := p.control.Exec(ctx, `
INSERT INTO developer_api_keys(id, developer_id, display_name, key_hash)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    key_hash = EXCLUDED.key_hash,
    revoked_at = NULL`, keyID, developerID, displayName, hash[:])
	if err != nil {
		return fmt.Errorf("upsert developer API key: %w", err)
	}
	return nil
}

func (p *Platform) AuthenticateDeveloper(ctx context.Context, apiKey string) (string, error) {
	if apiKey == "" {
		return "", ErrInvalidDeveloperAPIKey
	}
	hash := sha256.Sum256([]byte(apiKey))
	var developerID string
	err := p.control.QueryRow(ctx, `
UPDATE developer_api_keys
SET last_used_at = NOW()
WHERE key_hash = $1 AND revoked_at IS NULL
RETURNING developer_id`, hash[:]).Scan(&developerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalidDeveloperAPIKey
	}
	if err != nil {
		return "", fmt.Errorf("authenticate developer API key: %w", err)
	}
	return developerID, nil
}
