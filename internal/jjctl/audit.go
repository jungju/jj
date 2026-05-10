package jjctl

import (
	"context"
	"encoding/json"
)

func (a *App) Audit(ctx context.Context, userID, action, targetType, targetID string, metadata any) error {
	var metadataText any
	if metadata != nil {
		data, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		metadataText = string(data)
	}
	_, err := a.DB.ExecContext(ctx, `INSERT INTO audit_logs (
  id, user_id, action, target_type, target_id, metadata_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		newID("audit"), nullable(userID), action, nullable(targetType), nullable(targetID), metadataText, a.timestamp())
	return err
}
