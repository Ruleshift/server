package developerapi

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/roomcore"
)

type RoomManager struct {
	Control      controlplane.Store
	Rooms        *roomcore.Registry
	Routes       roomcore.Store
	DatabaseName func(string, string) (string, error)
}

func (m RoomManager) CreateRoom(ctx context.Context, developerID, moduleID, version string) (roomcore.Route, error) {
	if m.Control == nil || m.Rooms == nil {
		return roomcore.Route{}, fmt.Errorf("room manager is not configured")
	}
	resolved, err := m.Control.ResolveForNewRoom(ctx, developerID, moduleID, version)
	if err != nil {
		return roomcore.Route{}, err
	}
	roomID, seed, err := newRoomIdentity()
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
	route := roomcore.Route{RoomID: roomID, Module: resolved.Ref, ModuleDatabase: database, Seed: seed}
	if _, err = m.Rooms.Create(ctx, route); err != nil {
		return roomcore.Route{}, err
	}
	return route, nil
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
