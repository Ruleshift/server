package developerapi

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/roomcore"
)

const (
	inviteAlphabet          = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	maxInviteCodeAttempts   = 8
	inviteRandomAcceptLimit = 252
)

type RoomManager struct {
	Control      controlplane.Store
	Rooms        *roomcore.Registry
	Routes       roomcore.Store
	DatabaseName func(string, string) (string, error)
	Clock        func() time.Time
}

func (m RoomManager) CreateRoom(ctx context.Context, developerID, moduleID, version string) (roomcore.Route, error) {
	if m.Control == nil || m.Rooms == nil {
		return roomcore.Route{}, fmt.Errorf("room manager is not configured")
	}
	resolved, err := m.Control.ResolveForNewRoom(ctx, developerID, moduleID, version)
	if err != nil {
		return roomcore.Route{}, err
	}
	database := developerID + "_" + moduleID
	if m.DatabaseName != nil {
		database, err = m.DatabaseName(developerID, moduleID)
		if err != nil {
			return roomcore.Route{}, err
		}
	}
	clock := m.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	for range maxInviteCodeAttempts {
		roomID, seed, identityErr := newRoomIdentity()
		if identityErr != nil {
			return roomcore.Route{}, identityErr
		}
		inviteCode, inviteErr := newInviteCode()
		if inviteErr != nil {
			return roomcore.Route{}, inviteErr
		}
		createdAt := clock().UTC()
		route := roomcore.Route{
			RoomID:         roomID,
			Module:         resolved.Ref,
			ModuleDatabase: database,
			Seed:           seed,
			CreatedAt:      createdAt,
			InviteCode:     inviteCode,
			InviteDeadline: createdAt.Add(roomcore.InviteCodeTTL),
		}
		if _, err = m.Rooms.Create(ctx, route); err == nil {
			return route, nil
		}
		if !errors.Is(err, roomcore.ErrInviteCodeExists) {
			return roomcore.Route{}, err
		}
	}
	return roomcore.Route{}, fmt.Errorf("allocate unique room invite code after %d attempts: %w", maxInviteCodeAttempts, roomcore.ErrInviteCodeExists)
}
func (m RoomManager) GetRoom(ctx context.Context, developerID, roomID string) (roomcore.Route, error) {
	if m.Routes == nil {
		return roomcore.Route{}, fmt.Errorf("room route store is not configured")
	}
	route, err := m.Routes.Route(ctx, roomID)
	if err != nil {
		return roomcore.Route{}, err
	}
	if route.Module.DeveloperID != developerID {
		return roomcore.Route{}, controlplane.ErrUnauthorized
	}
	return route, nil
}
func newRoomIdentity() (string, uint64, error) {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", 0, err
	}
	id := hex.EncodeToString(value[:16])
	seed := binary.LittleEndian.Uint64(value[16:])
	return id, seed, nil
}

func newInviteCode() (string, error) {
	var code [roomcore.InviteCodeLength]byte
	var random [roomcore.InviteCodeLength]byte
	for offset := 0; offset < len(code); {
		if _, err := rand.Read(random[:]); err != nil {
			return "", fmt.Errorf("generate room invite code: %w", err)
		}
		for _, value := range random {
			// Reject the top four byte values so modulo 36 remains uniform.
			if value >= inviteRandomAcceptLimit {
				continue
			}
			code[offset] = inviteAlphabet[int(value)%len(inviteAlphabet)]
			offset++
			if offset == len(code) {
				break
			}
		}
	}
	return string(code[:]), nil
}
