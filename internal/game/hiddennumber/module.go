package hiddennumber

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/Ruleshift/server/internal/game"
)

const (
	MinSecret  int64 = 0
	MaxSecret  int64 = 999999
	maxPlayers       = 2
)

type Module struct{}

type SetSecret struct {
	Value int64 `json:"value"`
}

type PlayerState struct {
	PlayerID  string
	HasSecret bool
	Secret    int64
}

type State struct {
	Players   []PlayerState
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Snapshot struct {
	game.Snapshot
	Players []PlayerState
}

type Delta struct {
	game.Delta
	PlayerID  string
	HasSecret bool
	Secret    int64
}

type PlayerView struct {
	PlayerID  string
	HasSecret bool
	Secret    *int64
}

type SnapshotView struct {
	Players []PlayerView
}

type DeltaView struct {
	PlayerID  string
	HasSecret bool
	Secret    *int64
}

func NewModule() Module { return Module{} }

func (Module) Type() game.Type { return game.TypeHiddenNumber }

func (Module) NewState(now time.Time) (any, error) {
	return &State{CreatedAt: now, UpdatedAt: now}, nil
}

func (Module) PlayerJoined(raw any, playerID string) (any, bool, error) {
	state, err := stateFrom(raw)
	if err != nil {
		return raw, false, err
	}
	if playerID == "" {
		return raw, false, fmt.Errorf("player id must not be empty")
	}
	for _, player := range state.Players {
		if player.PlayerID == playerID {
			return state.clone(), false, nil
		}
	}
	if len(state.Players) >= maxPlayers {
		return raw, false, game.ErrRoomFull
	}
	next := state.clone()
	next.Players = append(next.Players, PlayerState{PlayerID: playerID})
	return next, true, nil
}

func (Module) Snapshot(raw any) (game.Snapshot, error) {
	state, err := stateFrom(raw)
	if err != nil {
		return game.Snapshot{}, err
	}
	base := game.Snapshot{Type: game.TypeHiddenNumber, Status: game.StatusActive, StateHash: state.hash()}
	payload := Snapshot{Snapshot: base, Players: append([]PlayerState(nil), state.Players...)}
	base.Payload = payload
	return base, nil
}

func (Module) ProjectSnapshot(raw any, viewer game.Viewer) (game.ViewSnapshot, error) {
	state, err := stateFrom(raw)
	if err != nil {
		return game.ViewSnapshot{}, err
	}
	payload := projectState(state, viewer)
	return game.ViewSnapshot{
		Type: game.TypeHiddenNumber, Status: game.StatusActive,
		ViewHash: hashView(payload), Payload: payload,
	}, nil
}

func (Module) Apply(raw any, command game.Command) (any, game.Delta, error) {
	state, err := stateFrom(raw)
	if err != nil {
		return raw, game.Delta{}, err
	}
	if command.Type != game.CommandSetSecret {
		return raw, game.Delta{}, game.ErrInvalidCommand
	}
	set, ok := setSecretFrom(command.Payload)
	if !ok || set.Value < MinSecret || set.Value > MaxSecret {
		return raw, game.Delta{}, game.ErrInvalidCommand
	}
	index := -1
	for i := range state.Players {
		if state.Players[i].PlayerID == command.PlayerID {
			index = i
			break
		}
	}
	if index < 0 {
		return raw, game.Delta{}, game.ErrPlayerNotSeated
	}
	next := state.clone()
	next.Players[index].HasSecret = true
	next.Players[index].Secret = set.Value
	next.UpdatedAt = command.At
	base := game.Delta{
		Type: game.TypeHiddenNumber, CommandType: game.CommandSetSecret,
		Status: game.StatusActive, StateHash: next.hash(), CommandPayload: set,
	}
	payload := Delta{Delta: base, PlayerID: command.PlayerID, HasSecret: true, Secret: set.Value}
	base.Payload = payload
	return next, base, nil
}

func (Module) ProjectDelta(beforeRaw any, afterRaw any, delta game.Delta, viewer game.Viewer) (game.ViewDelta, error) {
	before, err := stateFrom(beforeRaw)
	if err != nil {
		return game.ViewDelta{}, err
	}
	after, err := stateFrom(afterRaw)
	if err != nil {
		return game.ViewDelta{}, err
	}
	canonical, ok := DeltaPayload(delta)
	if !ok {
		return game.ViewDelta{}, game.ErrUnsupportedState
	}
	beforeView := projectState(before, viewer)
	afterView := projectState(after, viewer)
	result := game.ViewDelta{
		Type: game.TypeHiddenNumber, CommandType: game.CommandSetSecret,
		Status: game.StatusActive, ViewHash: hashView(afterView),
	}
	if hashView(beforeView) == result.ViewHash {
		result.NoVisibleChange = true
		return result, nil
	}
	view := DeltaView{PlayerID: canonical.PlayerID, HasSecret: canonical.HasSecret}
	if viewer.CanSeePrivateOf(canonical.PlayerID) {
		secret := canonical.Secret
		view.Secret = &secret
	}
	result.Payload = view
	return result, nil
}

func (Module) MarshalCommandPayload(_ context.Context, commandType game.CommandType, payload any) ([]byte, error) {
	if commandType != game.CommandSetSecret || payload == nil {
		return nil, nil
	}
	set, ok := setSecretFrom(payload)
	if !ok {
		return nil, game.ErrInvalidCommand
	}
	return json.Marshal(set)
}

func (Module) UnmarshalCommandPayload(_ context.Context, commandType game.CommandType, payload []byte) (any, error) {
	if commandType != game.CommandSetSecret || len(payload) == 0 || string(payload) == "null" {
		return nil, nil
	}
	var set SetSecret
	if err := json.Unmarshal(payload, &set); err != nil {
		return nil, err
	}
	return set, nil
}

func SnapshotPayload(snapshot game.Snapshot) (Snapshot, bool) {
	switch payload := snapshot.Payload.(type) {
	case Snapshot:
		return payload, true
	case *Snapshot:
		if payload != nil {
			return *payload, true
		}
	}
	return Snapshot{}, false
}

func DeltaPayload(delta game.Delta) (Delta, bool) {
	switch payload := delta.Payload.(type) {
	case Delta:
		return payload, true
	case *Delta:
		if payload != nil {
			return *payload, true
		}
	}
	return Delta{}, false
}

func stateFrom(raw any) (*State, error) {
	state, ok := raw.(*State)
	if !ok || state == nil {
		return nil, game.ErrUnsupportedState
	}
	return state, nil
}

func setSecretFrom(raw any) (SetSecret, bool) {
	switch value := raw.(type) {
	case SetSecret:
		return value, true
	case *SetSecret:
		if value != nil {
			return *value, true
		}
	}
	return SetSecret{}, false
}

func (s *State) clone() *State {
	return &State{Players: append([]PlayerState(nil), s.Players...), CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}

func (s *State) hash() uint64 {
	h := fnv.New64a()
	for _, player := range s.Players {
		writePlayer(h, player.PlayerID, player.HasSecret, player.Secret, player.HasSecret)
	}
	return h.Sum64()
}

func projectState(state *State, viewer game.Viewer) SnapshotView {
	view := SnapshotView{Players: make([]PlayerView, 0, len(state.Players))}
	for _, player := range state.Players {
		projected := PlayerView{PlayerID: player.PlayerID, HasSecret: player.HasSecret}
		if player.HasSecret && viewer.CanSeePrivateOf(player.PlayerID) {
			secret := player.Secret
			projected.Secret = &secret
		}
		view.Players = append(view.Players, projected)
	}
	return view
}

func hashView(view SnapshotView) uint64 {
	h := fnv.New64a()
	for _, player := range view.Players {
		writePlayer(h, player.PlayerID, player.HasSecret, valueOf(player.Secret), player.Secret != nil)
	}
	return h.Sum64()
}

type byteWriter interface{ Write([]byte) (int, error) }

func writePlayer(h byteWriter, playerID string, hasSecret bool, secret int64, secretVisible bool) {
	_, _ = h.Write([]byte(playerID))
	flags := byte(0)
	if hasSecret {
		flags |= 1
	}
	if secretVisible {
		flags |= 2
	}
	_, _ = h.Write([]byte{0, flags})
	if secretVisible {
		var encoded [8]byte
		binary.LittleEndian.PutUint64(encoded[:], uint64(secret))
		_, _ = h.Write(encoded[:])
	}
}

func valueOf(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
