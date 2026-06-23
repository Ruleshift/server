package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Ruleshift/server/internal/game"
	"github.com/Ruleshift/server/internal/room"
)

type EventStore struct {
	db    *sql.DB
	codec game.CommandPayloadCodec
}

func NewEventStore(db *sql.DB, module game.Module) *EventStore {
	codec, _ := module.(game.CommandPayloadCodec)
	return &EventStore{db: db, codec: codec}
}

func (s *EventStore) Append(ctx context.Context, event room.RoomEvent) (room.RoomEvent, error) {
	if err := ctx.Err(); err != nil {
		return room.RoomEvent{}, fmt.Errorf("append room event: %w", err)
	}
	if event.Type == "" {
		return room.RoomEvent{}, fmt.Errorf("room event type must not be empty")
	}
	if event.RoomID == "" {
		return room.RoomEvent{}, room.ErrEmptyRoomID
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}

	payload, err := s.marshalPayload(ctx, event.CommandType, event.CommandPayload)
	if err != nil {
		return room.RoomEvent{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return room.RoomEvent{}, fmt.Errorf("begin room event transaction: %w", err)
	}
	defer tx.Rollback()

	if err := projectEvent(ctx, tx, event); err != nil {
		return room.RoomEvent{}, err
	}
	var sequence int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO room_events(
    room_id, event_type, player_id, revision, previous_revision, new_revision,
    game_type, command_type, command_payload, state_hash, game_status, reason, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING sequence`,
		event.RoomID, string(event.Type), event.PlayerID,
		uintString(event.Revision), uintString(event.PreviousRevision), uintString(event.NewRevision),
		int16(event.GameType), int16(event.CommandType), nullableJSON(payload), uintString(event.StateHash),
		int16(event.Status), event.Reason, event.OccurredAt,
	).Scan(&sequence)
	if err != nil {
		return room.RoomEvent{}, fmt.Errorf("insert room event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return room.RoomEvent{}, fmt.Errorf("commit room event: %w", err)
	}
	event.Sequence = uint64(sequence)
	return event, nil
}

func (s *EventStore) List(ctx context.Context, roomID string) ([]room.RoomEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list room events: %w", err)
	}
	if roomID == "" {
		return nil, room.ErrEmptyRoomID
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT sequence, event_type, room_id, player_id,
       revision::text, previous_revision::text, new_revision::text,
       game_type, command_type, command_payload, state_hash::text, game_status, reason, occurred_at
FROM room_events
WHERE room_id = $1
ORDER BY sequence`, roomID)
	if err != nil {
		return nil, fmt.Errorf("query room events: %w", err)
	}
	defer rows.Close()

	events := make([]room.RoomEvent, 0)
	for rows.Next() {
		var event room.RoomEvent
		var sequence int64
		var eventType string
		var revision, previousRevision, newRevision, stateHash string
		var gameType, commandType, status int16
		var payload []byte
		if err := rows.Scan(
			&sequence, &eventType, &event.RoomID, &event.PlayerID,
			&revision, &previousRevision, &newRevision,
			&gameType, &commandType, &payload, &stateHash, &status, &event.Reason, &event.OccurredAt,
		); err != nil {
			return nil, fmt.Errorf("scan room event: %w", err)
		}
		event.Sequence = uint64(sequence)
		event.Type = room.EventType(eventType)
		event.GameType = game.Type(gameType)
		event.CommandType = game.CommandType(commandType)
		event.Status = game.Status(status)
		if event.Revision, err = parseUint("revision", revision); err != nil {
			return nil, err
		}
		if event.PreviousRevision, err = parseUint("previous revision", previousRevision); err != nil {
			return nil, err
		}
		if event.NewRevision, err = parseUint("new revision", newRevision); err != nil {
			return nil, err
		}
		if event.StateHash, err = parseUint("state hash", stateHash); err != nil {
			return nil, err
		}
		if event.CommandPayload, err = s.unmarshalPayload(ctx, event.CommandType, payload); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate room events: %w", err)
	}
	return events, nil
}

func projectEvent(ctx context.Context, tx *sql.Tx, event room.RoomEvent) error {
	switch event.Type {
	case room.EventTypeRoomCreated:
		_, err := tx.ExecContext(ctx, `
INSERT INTO rooms(id, game_type, revision, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (id) DO NOTHING`, event.RoomID, int16(event.GameType), uintString(event.Revision), event.OccurredAt)
		if err != nil {
			return fmt.Errorf("project room created: %w", err)
		}
	case room.EventTypePlayerJoined:
		_, err := tx.ExecContext(ctx, `
INSERT INTO room_players(room_id, player_id, joined_at)
VALUES ($1, $2, $3)
ON CONFLICT (room_id, player_id) DO UPDATE SET
    last_disconnected_at = NULL,
    last_disconnect_reason = ''`, event.RoomID, event.PlayerID, event.OccurredAt)
		if err != nil {
			return fmt.Errorf("project player joined: %w", err)
		}
	case room.EventTypePlayerDisconnected:
		_, err := tx.ExecContext(ctx, `
UPDATE room_players SET last_disconnected_at = $3, last_disconnect_reason = $4
WHERE room_id = $1 AND player_id = $2`, event.RoomID, event.PlayerID, event.OccurredAt, event.Reason)
		if err != nil {
			return fmt.Errorf("project player disconnected: %w", err)
		}
	case room.EventTypeGameMoveApplied, room.EventTypePlayerResigned, room.EventTypeDrawOffered:
		result, err := tx.ExecContext(ctx, `
UPDATE rooms SET revision = $2, updated_at = $3 WHERE id = $1 AND revision = $4`,
			event.RoomID, uintString(event.NewRevision), event.OccurredAt, uintString(event.PreviousRevision))
		if err != nil {
			return fmt.Errorf("project room revision: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect projected room revision: %w", err)
		}
		if changed != 1 {
			return fmt.Errorf("project room revision conflict: room=%q previous=%d new=%d", event.RoomID, event.PreviousRevision, event.NewRevision)
		}
	}
	return nil
}

func (s *EventStore) marshalPayload(ctx context.Context, commandType game.CommandType, payload any) ([]byte, error) {
	if payload == nil {
		return nil, nil
	}
	if s.codec != nil {
		encoded, err := s.codec.MarshalCommandPayload(ctx, commandType, payload)
		if err != nil {
			return nil, fmt.Errorf("encode module command payload: %w", err)
		}
		return encoded, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode command payload: %w", err)
	}
	return encoded, nil
}

func (s *EventStore) unmarshalPayload(ctx context.Context, commandType game.CommandType, payload []byte) (any, error) {
	if len(payload) == 0 || string(payload) == "null" {
		return nil, nil
	}
	if s.codec != nil {
		decoded, err := s.codec.UnmarshalCommandPayload(ctx, commandType, payload)
		if err != nil {
			return nil, fmt.Errorf("decode module command payload: %w", err)
		}
		return decoded, nil
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("decode command payload: %w", err)
	}
	return decoded, nil
}

func nullableJSON(payload []byte) any {
	if len(payload) == 0 {
		return nil
	}
	return string(payload)
}

func uintString(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func parseUint(field, value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse room event %s: %w", field, err)
	}
	return parsed, nil
}
