package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Ruleshift/server/internal/module"
	"github.com/Ruleshift/server/internal/roomcore"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RoomCoreStore struct{ platform *Platform }

func (p *Platform) RoomCoreStore() *RoomCoreStore { return &RoomCoreStore{platform: p} }

func (s *RoomCoreStore) Create(ctx context.Context, state roomcore.State, event roomcore.Event, snapshot roomcore.Snapshot) error {
	r := state.Route
	err := pgx.BeginFunc(ctx, s.platform.control, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO room_routes(room_id,developer_id,module_id,module_version,image_digest,module_database,seed,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, r.RoomID, r.Module.DeveloperID, r.Module.ModuleID, r.Module.Version, r.Module.ImageDigest, r.ModuleDatabase, r.Seed, r.CreatedAt)
		if isUniqueViolation(err) {
			return roomcore.ErrRoomExists
		}
		if err != nil {
			return fmt.Errorf("insert room route: %w", err)
		}
		if r.InviteCode == "" {
			return nil
		}
		_, err = tx.Exec(ctx, `INSERT INTO room_invite_codes(room_id,code,deadline) VALUES($1,$2,$3)`, r.RoomID, r.InviteCode, r.InviteDeadline)
		if isUniqueViolation(err) {
			return roomcore.ErrInviteCodeExists
		}
		if err != nil {
			return fmt.Errorf("insert room invite code: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	db, err := s.moduleDB(ctx, r.ModuleDatabase)
	if err != nil {
		return err
	}
	versionID := r.Module.DeveloperID + ":" + r.Module.ModuleID + ":" + r.Module.Version + ":" + r.Module.ImageDigest
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO rooms(id,module_version_id,revision,lifecycle_status,seed,state_type_url,state_payload,state_digest,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`, r.RoomID, versionID, uint64(0), state.Status, r.Seed, state.Opaque.TypeURL, state.Opaque.Payload, state.Opaque.Digest[:], state.CreatedAt); err != nil {
			return err
		}
		if err := insertOpaqueEvent(ctx, tx, event); err != nil {
			return err
		}
		return insertSnapshot(ctx, tx, snapshot)
	})
	if err != nil {
		_, _ = s.platform.control.Exec(context.Background(), `DELETE FROM room_routes WHERE room_id=$1`, r.RoomID)
		return fmt.Errorf("create module room: %w", err)
	}
	return nil
}

func (s *RoomCoreStore) Load(ctx context.Context, roomID string) (roomcore.Route, *roomcore.Snapshot, []roomcore.Event, error) {
	route, err := s.Route(ctx, roomID)
	if err != nil {
		return roomcore.Route{}, nil, nil, err
	}
	db, err := s.moduleDB(ctx, route.ModuleDatabase)
	if err != nil {
		return roomcore.Route{}, nil, nil, err
	}
	snapshot, err := loadLatestSnapshot(ctx, db, roomID)
	if err != nil {
		return roomcore.Route{}, nil, nil, err
	}
	revision := uint64(0)
	if snapshot != nil {
		revision = snapshot.Revision
	}
	rows, err := db.Query(ctx, `SELECT sequence,event_kind,player_id,previous_revision,new_revision,input_type_url,input_payload,delta_type_url,delta_payload,state_digest,occurred_at FROM room_events WHERE room_id=$1 AND new_revision>$2 ORDER BY sequence`, roomID, revision)
	if err != nil {
		return roomcore.Route{}, nil, nil, err
	}
	defer rows.Close()
	events := []roomcore.Event{}
	for rows.Next() {
		var event roomcore.Event
		var kind string
		var inputType, deltaType *string
		var inputPayload, deltaPayload, digest []byte
		if err = rows.Scan(&event.Sequence, &kind, &event.PlayerID, &event.PreviousRevision, &event.NewRevision, &inputType, &inputPayload, &deltaType, &deltaPayload, &digest, &event.OccurredAt); err != nil {
			return roomcore.Route{}, nil, nil, err
		}
		event.RoomID = roomID
		event.Kind = roomcore.EventKind(kind)
		if inputType != nil {
			event.Input, err = module.NewOpaque(*inputType, inputPayload, module.MaxMessageBytes)
			if err != nil {
				return roomcore.Route{}, nil, nil, err
			}
		}
		deltaURL := ""
		if deltaType != nil {
			deltaURL = *deltaType
		}
		event.Delta, err = module.NewOpaque(deltaURL, deltaPayload, module.MaxMessageBytes)
		if err != nil {
			return roomcore.Route{}, nil, nil, err
		}
		if len(digest) != 32 {
			return roomcore.Route{}, nil, nil, fmt.Errorf("event %d has invalid state digest", event.Sequence)
		}
		copy(event.StateDigest[:], digest)
		events = append(events, event)
	}
	if err = rows.Err(); err != nil {
		return roomcore.Route{}, nil, nil, err
	}
	return route, snapshot, events, nil
}

func (s *RoomCoreStore) Commit(ctx context.Context, state roomcore.State, event roomcore.Event, snapshot *roomcore.Snapshot) error {
	db, err := s.moduleDB(ctx, state.Route.ModuleDatabase)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `UPDATE rooms SET revision=$2,updated_at=$3,state_type_url=$4,state_payload=$5,state_digest=$6 WHERE id=$1 AND revision=$7`, state.Route.RoomID, state.Revision, state.UpdatedAt, state.Opaque.TypeURL, state.Opaque.Payload, state.Opaque.Digest[:], event.PreviousRevision)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return roomcore.ErrRevisionMismatch
		}
		if err = insertOpaqueEvent(ctx, tx, event); err != nil {
			return err
		}
		if snapshot != nil {
			return insertSnapshot(ctx, tx, *snapshot)
		}
		return nil
	})
}

func (s *RoomCoreStore) SaveSnapshot(ctx context.Context, value roomcore.Snapshot) error {
	route, err := s.Route(ctx, value.RoomID)
	if err != nil {
		return err
	}
	db, err := s.moduleDB(ctx, route.ModuleDatabase)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO room_snapshots(room_id,revision,state_type_url,state_payload,state_digest,saved_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(room_id,revision) DO UPDATE SET state_type_url=EXCLUDED.state_type_url,state_payload=EXCLUDED.state_payload,state_digest=EXCLUDED.state_digest,saved_at=EXCLUDED.saved_at`, value.RoomID, value.Revision, value.State.TypeURL, value.State.Payload, value.State.Digest[:], value.SavedAt)
	return err
}

func (s *RoomCoreStore) Route(ctx context.Context, roomID string) (roomcore.Route, error) {
	var value roomcore.Route
	var inviteCode *string
	var inviteDeadline *time.Time
	err := s.platform.control.QueryRow(ctx, `SELECT r.developer_id,r.module_id,r.module_version,r.image_digest,r.module_database,r.seed,r.created_at,i.code,i.deadline FROM room_routes r LEFT JOIN room_invite_codes i ON i.room_id=r.room_id WHERE r.room_id=$1`, roomID).Scan(&value.Module.DeveloperID, &value.Module.ModuleID, &value.Module.Version, &value.Module.ImageDigest, &value.ModuleDatabase, &value.Seed, &value.CreatedAt, &inviteCode, &inviteDeadline)
	if errors.Is(err, pgx.ErrNoRows) {
		return roomcore.Route{}, roomcore.ErrRoomNotFound
	}
	if err != nil {
		return roomcore.Route{}, err
	}
	value.RoomID = roomID
	if inviteCode != nil {
		value.InviteCode = *inviteCode
	}
	if inviteDeadline != nil {
		value.InviteDeadline = *inviteDeadline
	}
	return value, nil
}

func (s *RoomCoreStore) moduleDB(ctx context.Context, name string) (*pgxpool.Pool, error) {
	return s.platform.openModuleDatabase(ctx, name)
}
func insertOpaqueEvent(ctx context.Context, tx pgx.Tx, event roomcore.Event) error {
	var inputType any
	var inputPayload any
	if event.Input.TypeURL != "" {
		inputType = event.Input.TypeURL
		inputPayload = event.Input.Payload
	}
	_, err := tx.Exec(ctx, `INSERT INTO room_events(room_id,event_kind,player_id,previous_revision,new_revision,input_type_url,input_payload,delta_type_url,delta_payload,state_digest,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, event.RoomID, string(event.Kind), event.PlayerID, event.PreviousRevision, event.NewRevision, inputType, inputPayload, event.Delta.TypeURL, event.Delta.Payload, event.StateDigest[:], event.OccurredAt)
	return err
}
func insertSnapshot(ctx context.Context, tx pgx.Tx, value roomcore.Snapshot) error {
	_, err := tx.Exec(ctx, `INSERT INTO room_snapshots(room_id,revision,state_type_url,state_payload,state_digest,saved_at) VALUES($1,$2,$3,$4,$5,$6)`, value.RoomID, value.Revision, value.State.TypeURL, value.State.Payload, value.State.Digest[:], value.SavedAt)
	return err
}
func loadLatestSnapshot(ctx context.Context, db *pgxpool.Pool, roomID string) (*roomcore.Snapshot, error) {
	var value roomcore.Snapshot
	var payload, digest []byte
	err := db.QueryRow(ctx, `SELECT revision,state_type_url,state_payload,state_digest,saved_at FROM room_snapshots WHERE room_id=$1 ORDER BY revision DESC LIMIT 1`, roomID).Scan(&value.Revision, &value.State.TypeURL, &payload, &digest, &value.SavedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value.RoomID = roomID
	value.State, err = module.NewOpaque(value.State.TypeURL, payload, module.MaxStateBytes)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(value.State.Digest[:], digest) {
		return nil, fmt.Errorf("snapshot digest mismatch")
	}
	return &value, nil
}
func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == pgerrcode.UniqueViolation
}
