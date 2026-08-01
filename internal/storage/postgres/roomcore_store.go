package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Ruleshift/server/internal/module"
	"github.com/Ruleshift/server/internal/roomcore"
)

type RoomCoreStore struct{ platform *Platform }

func (p *Platform) RoomCoreStore() *RoomCoreStore { return &RoomCoreStore{platform: p} }

func (s *RoomCoreStore) Create(ctx context.Context, state roomcore.State, event roomcore.Event, snapshot roomcore.Snapshot) error {
	r := state.Route
	controlTx, err := s.platform.control.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin room route transaction: %w", err)
	}
	defer controlTx.Rollback()
	_, err = controlTx.ExecContext(ctx, `INSERT INTO room_routes(room_id,developer_id,module_id,module_version,image_digest,module_database,seed,player_count,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, r.RoomID, r.Module.DeveloperID, r.Module.ModuleID, r.Module.Version, r.Module.ImageDigest, r.ModuleDatabase, strconv.FormatUint(r.Seed, 10), r.PlayerCount, r.CreatedAt)
	if isUniqueViolation(err) {
		return roomcore.ErrRoomExists
	}
	if err != nil {
		return fmt.Errorf("insert room route: %w", err)
	}
	if r.InviteCode != "" {
		_, err = controlTx.ExecContext(ctx, `INSERT INTO room_invite_codes(room_id,code,deadline) VALUES($1,$2,$3)`, r.RoomID, r.InviteCode, r.InviteDeadline)
		if isUniqueViolation(err) {
			return roomcore.ErrInviteCodeExists
		}
		if err != nil {
			return fmt.Errorf("insert room invite code: %w", err)
		}
	}
	if err = controlTx.Commit(); err != nil {
		return fmt.Errorf("commit room route transaction: %w", err)
	}
	db, err := s.moduleDB(ctx, r.ModuleDatabase)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	versionID := r.Module.DeveloperID + ":" + r.Module.ModuleID + ":" + r.Module.Version + ":" + r.Module.ImageDigest
	_, err = tx.ExecContext(ctx, `INSERT INTO rooms(id,module_version_id,revision,lifecycle_status,seed,required_players,state_type_url,state_payload,state_digest,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`, r.RoomID, versionID, "0", state.Status, strconv.FormatUint(r.Seed, 10), r.PlayerCount, state.Opaque.TypeURL, state.Opaque.Payload, state.Opaque.Digest[:], state.CreatedAt)
	if err == nil {
		err = insertOpaqueEvent(ctx, tx, event)
	}
	if err == nil {
		err = insertSnapshot(ctx, tx, snapshot)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_, _ = s.platform.control.ExecContext(context.Background(), `DELETE FROM room_routes WHERE room_id=$1`, r.RoomID)
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
	rows, err := db.QueryContext(ctx, `SELECT sequence,event_kind,player_id,previous_revision::text,new_revision::text,input_type_url,input_payload,delta_type_url,delta_payload,state_digest,occurred_at FROM room_events WHERE room_id=$1 AND new_revision>$2 ORDER BY sequence`, roomID, strconv.FormatUint(revision, 10))
	if err != nil {
		return roomcore.Route{}, nil, nil, err
	}
	defer rows.Close()
	events := []roomcore.Event{}
	for rows.Next() {
		var event roomcore.Event
		var kind string
		var previous, next string
		var inputType, deltaType sql.NullString
		var inputPayload, deltaPayload, digest []byte
		if err = rows.Scan(&event.Sequence, &kind, &event.PlayerID, &previous, &next, &inputType, &inputPayload, &deltaType, &deltaPayload, &digest, &event.OccurredAt); err != nil {
			return roomcore.Route{}, nil, nil, err
		}
		event.RoomID = roomID
		event.Kind = roomcore.EventKind(kind)
		event.PreviousRevision, err = strconv.ParseUint(previous, 10, 64)
		if err != nil {
			return roomcore.Route{}, nil, nil, err
		}
		event.NewRevision, err = strconv.ParseUint(next, 10, 64)
		if err != nil {
			return roomcore.Route{}, nil, nil, err
		}
		if inputType.Valid {
			event.Input, err = module.NewOpaque(inputType.String, inputPayload, module.MaxMessageBytes)
			if err != nil {
				return roomcore.Route{}, nil, nil, err
			}
		}
		event.Delta, err = module.NewOpaque(deltaType.String, deltaPayload, module.MaxMessageBytes)
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
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE rooms SET revision=$2,updated_at=$3,state_type_url=$4,state_payload=$5,state_digest=$6 WHERE id=$1 AND revision=$7`, state.Route.RoomID, strconv.FormatUint(state.Revision, 10), state.UpdatedAt, state.Opaque.TypeURL, state.Opaque.Payload, state.Opaque.Digest[:], strconv.FormatUint(event.PreviousRevision, 10))
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return roomcore.ErrRevisionMismatch
	}
	if err = insertOpaqueEvent(ctx, tx, event); err != nil {
		return err
	}
	if snapshot != nil {
		if err = insertSnapshot(ctx, tx, *snapshot); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *RoomCoreStore) LoadMembership(ctx context.Context, roomID string) (string, []roomcore.Participant, error) {
	route, err := s.Route(ctx, roomID)
	if err != nil {
		return "", nil, err
	}
	db, err := s.moduleDB(ctx, route.ModuleDatabase)
	if err != nil {
		return "", nil, err
	}
	var status string
	if err = db.QueryRowContext(ctx, `SELECT lifecycle_status FROM rooms WHERE id=$1`, roomID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, roomcore.ErrRoomNotFound
		}
		return "", nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT player_id,seat_index,joined_at FROM room_participants WHERE room_id=$1 ORDER BY seat_index`, roomID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	participants := make([]roomcore.Participant, 0, route.PlayerCount)
	for rows.Next() {
		var participant roomcore.Participant
		if err = rows.Scan(&participant.PlayerID, &participant.SeatIndex, &participant.JoinedAt); err != nil {
			return "", nil, err
		}
		participants = append(participants, participant)
	}
	if err = rows.Err(); err != nil {
		return "", nil, err
	}
	return status, participants, nil
}

func (s *RoomCoreStore) SaveMembership(ctx context.Context, state roomcore.State) error {
	if len(state.Participants) > int(state.Route.PlayerCount) {
		return roomcore.ErrRoomFull
	}
	db, err := s.moduleDB(ctx, state.Route.ModuleDatabase)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE rooms SET lifecycle_status=$2,updated_at=$3 WHERE id=$1`, state.Route.RoomID, state.Status, state.UpdatedAt)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return roomcore.ErrRoomNotFound
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM room_participants WHERE room_id=$1`, state.Route.RoomID); err != nil {
		return err
	}
	for _, participant := range state.Participants {
		if _, err = tx.ExecContext(ctx, `INSERT INTO room_participants(room_id,player_id,seat_index,joined_at) VALUES($1,$2,$3,$4)`, state.Route.RoomID, participant.PlayerID, participant.SeatIndex, participant.JoinedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	_, err = db.ExecContext(ctx, `INSERT INTO room_snapshots(room_id,revision,state_type_url,state_payload,state_digest,saved_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(room_id,revision) DO UPDATE SET state_type_url=EXCLUDED.state_type_url,state_payload=EXCLUDED.state_payload,state_digest=EXCLUDED.state_digest,saved_at=EXCLUDED.saved_at`, value.RoomID, strconv.FormatUint(value.Revision, 10), value.State.TypeURL, value.State.Payload, value.State.Digest[:], value.SavedAt)
	return err
}

func (s *RoomCoreStore) Route(ctx context.Context, roomID string) (roomcore.Route, error) {
	var value roomcore.Route
	var seed string
	var inviteCode sql.NullString
	var inviteDeadline sql.NullTime
	err := s.platform.control.QueryRowContext(ctx, `SELECT r.developer_id,r.module_id,r.module_version,r.image_digest,r.module_database,r.seed::text,r.player_count,r.created_at,i.code,i.deadline FROM room_routes r LEFT JOIN room_invite_codes i ON i.room_id=r.room_id WHERE r.room_id=$1`, roomID).Scan(&value.Module.DeveloperID, &value.Module.ModuleID, &value.Module.Version, &value.Module.ImageDigest, &value.ModuleDatabase, &seed, &value.PlayerCount, &value.CreatedAt, &inviteCode, &inviteDeadline)
	if errors.Is(err, sql.ErrNoRows) {
		return roomcore.Route{}, roomcore.ErrRoomNotFound
	}
	if err != nil {
		return roomcore.Route{}, err
	}
	value.RoomID = roomID
	if inviteCode.Valid {
		value.InviteCode = inviteCode.String
	}
	if inviteDeadline.Valid {
		value.InviteDeadline = inviteDeadline.Time
	}
	value.Seed, err = strconv.ParseUint(seed, 10, 64)
	return value, err
}

func (s *RoomCoreStore) RouteByInviteCode(ctx context.Context, inviteCode string) (roomcore.Route, error) {
	var roomID string
	err := s.platform.control.QueryRowContext(ctx, `SELECT room_id FROM room_invite_codes WHERE code=$1 AND deadline>CURRENT_TIMESTAMP`, inviteCode).Scan(&roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return roomcore.Route{}, roomcore.ErrInviteCodeNotFound
	}
	if err != nil {
		return roomcore.Route{}, fmt.Errorf("resolve room invite code: %w", err)
	}
	return s.Route(ctx, roomID)
}

func (s *RoomCoreStore) moduleDB(ctx context.Context, name string) (*sql.DB, error) {
	url, err := databaseURL(s.platform.controlURL, name)
	if err != nil {
		return nil, err
	}
	return s.platform.openModuleDatabase(ctx, name, url)
}
func insertOpaqueEvent(ctx context.Context, tx *sql.Tx, event roomcore.Event) error {
	var inputType any
	var inputPayload any
	if event.Input.TypeURL != "" {
		inputType = event.Input.TypeURL
		inputPayload = event.Input.Payload
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO room_events(room_id,event_kind,player_id,previous_revision,new_revision,input_type_url,input_payload,delta_type_url,delta_payload,state_digest,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, event.RoomID, string(event.Kind), event.PlayerID, strconv.FormatUint(event.PreviousRevision, 10), strconv.FormatUint(event.NewRevision, 10), inputType, inputPayload, event.Delta.TypeURL, event.Delta.Payload, event.StateDigest[:], event.OccurredAt)
	return err
}
func insertSnapshot(ctx context.Context, tx *sql.Tx, value roomcore.Snapshot) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO room_snapshots(room_id,revision,state_type_url,state_payload,state_digest,saved_at) VALUES($1,$2,$3,$4,$5,$6)`, value.RoomID, strconv.FormatUint(value.Revision, 10), value.State.TypeURL, value.State.Payload, value.State.Digest[:], value.SavedAt)
	return err
}
func loadLatestSnapshot(ctx context.Context, db *sql.DB, roomID string) (*roomcore.Snapshot, error) {
	var value roomcore.Snapshot
	var revision string
	var payload, digest []byte
	err := db.QueryRowContext(ctx, `SELECT revision::text,state_type_url,state_payload,state_digest,saved_at FROM room_snapshots WHERE room_id=$1 ORDER BY revision DESC LIMIT 1`, roomID).Scan(&revision, &value.State.TypeURL, &payload, &digest, &value.SavedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value.RoomID = roomID
	value.Revision, err = strconv.ParseUint(revision, 10, 64)
	if err != nil {
		return nil, err
	}
	value.State, err = module.NewOpaque(value.State.TypeURL, payload, module.MaxStateBytes)
	if err != nil {
		return nil, err
	}
	if len(digest) != 32 || !equalDigest(value.State.Digest, digest) {
		return nil, fmt.Errorf("snapshot digest mismatch")
	}
	return &value, nil
}
func equalDigest(left [32]byte, right []byte) bool {
	if len(right) != 32 {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key")
}
