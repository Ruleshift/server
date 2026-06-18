package xiangqi

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/Ruleshift/server/internal/game"
	xq "github.com/hmgle/godogpaw/engine"
)

const initialFEN = "rnbakabnr/9/1c5c1/p1p1p1p1p/9/9/P1P1P1P1P/1C5C1/9/RNBAKABNR w - - 0 1"

type Module struct{}

type Side uint8

const (
	SideUnspecified Side = iota
	SideRed
	SideBlack
)

type Move struct {
	FromSquare uint32
	ToSquare   uint32
	UCI        string
}

type SquareUpdate struct {
	Square uint32
	Piece  uint32
}

type Snapshot struct {
	game.Snapshot
	FEN                   string
	Board                 []uint32
	SideToMove            Side
	RedPlayerID           string
	BlackPlayerID         string
	WinnerPlayerID        string
	DrawOfferedByPlayerID string
}

type Delta struct {
	game.Delta
	MoveUCI               string
	FromSquare            uint32
	ToSquare              uint32
	SquareUpdates         []SquareUpdate
	SideToMove            Side
	WinnerPlayerID        string
	DrawOfferedByPlayerID string
}

type State struct {
	position              *xq.PositionNG
	status                game.Status
	redPlayerID           string
	blackPlayerID         string
	winnerPlayerID        string
	drawOfferedByPlayerID string
	createdAt             time.Time
	updatedAt             time.Time
}

func NewModule() Module {
	return Module{}
}

func SnapshotPayload(snapshot game.Snapshot) (Snapshot, bool) {
	switch payload := snapshot.Payload.(type) {
	case Snapshot:
		return payload, true
	case *Snapshot:
		if payload == nil {
			return Snapshot{}, false
		}
		return *payload, true
	default:
		return Snapshot{}, false
	}
}

func DeltaPayload(delta game.Delta) (Delta, bool) {
	switch payload := delta.Payload.(type) {
	case Delta:
		return payload, true
	case *Delta:
		if payload == nil {
			return Delta{}, false
		}
		return *payload, true
	default:
		return Delta{}, false
	}
}

func (Module) Type() game.Type {
	return game.TypeXiangqi
}

func (Module) NewState(now time.Time) (any, error) {
	position := &xq.PositionNG{}
	position.Set(initialFEN)

	return &State{
		position:  position,
		status:    game.StatusActive,
		createdAt: now,
		updatedAt: now,
	}, nil
}

func (Module) PlayerJoined(raw any, playerID string) (any, error) {
	state, err := stateFrom(raw)
	if err != nil {
		return raw, err
	}
	if playerID == "" {
		return raw, fmt.Errorf("player id must not be empty")
	}

	switch {
	case state.redPlayerID == playerID || state.blackPlayerID == playerID:
		return state, nil
	case state.redPlayerID == "":
		state.redPlayerID = playerID
	case state.blackPlayerID == "":
		state.blackPlayerID = playerID
	}
	return state, nil
}

func (Module) Snapshot(raw any) (game.Snapshot, error) {
	state, err := stateFrom(raw)
	if err != nil {
		return game.Snapshot{}, err
	}
	return state.snapshot(), nil
}

func (Module) Apply(raw any, command game.Command) (any, game.Delta, error) {
	state, err := stateFrom(raw)
	if err != nil {
		return raw, game.Delta{}, err
	}
	if command.PlayerID == "" {
		return raw, game.Delta{}, fmt.Errorf("player id must not be empty")
	}

	switch command.Type {
	case game.CommandDoMove:
		return applyMove(state, command)
	case game.CommandResign:
		return applyResign(state, command)
	case game.CommandOfferDraw:
		return applyOfferDraw(state, command)
	default:
		return raw, game.Delta{}, fmt.Errorf("%w: %d", game.ErrInvalidCommand, command.Type)
	}
}

func applyMove(state *State, command game.Command) (any, game.Delta, error) {
	if state.finished() {
		return state, game.Delta{}, game.ErrGameFinished
	}
	if state.sideForPlayer(command.PlayerID) != state.sideToMove() {
		if state.sideForPlayer(command.PlayerID) == SideUnspecified {
			return state, game.Delta{}, game.ErrPlayerNotSeated
		}
		return state, game.Delta{}, game.ErrNotPlayersTurn
	}

	requestedMove, err := moveFromCommand(command)
	if err != nil {
		return state, game.Delta{}, err
	}

	move, err := resolveMove(state.position, requestedMove)
	if err != nil {
		return state, game.Delta{}, err
	}

	from := uint32(xq.FromSQ(move))
	to := uint32(xq.ToSQ(move))
	st := &xq.StateInfo{}
	state.position.DoMove(move, st)
	state.drawOfferedByPlayerID = ""
	state.status = game.StatusActive
	state.updatedAt = command.At

	appliedMove := Move{FromSquare: from, ToSquare: to, UCI: xq.Move2Str(move)}
	payload := Delta{
		MoveUCI:    appliedMove.UCI,
		FromSquare: from,
		ToSquare:   to,
		SquareUpdates: []SquareUpdate{
			{Square: from, Piece: uint32(state.position.Board[from])},
			{Square: to, Piece: uint32(state.position.Board[to])},
		},
		SideToMove: state.sideToMove(),
	}
	return state, newDelta(game.CommandDoMove, state.status, state.hash(), appliedMove, payload), nil
}

func applyResign(state *State, command game.Command) (any, game.Delta, error) {
	if state.finished() {
		return state, game.Delta{}, game.ErrGameFinished
	}

	side := state.sideForPlayer(command.PlayerID)
	if side == SideUnspecified {
		return state, game.Delta{}, game.ErrPlayerNotSeated
	}

	state.status = game.StatusResigned
	state.drawOfferedByPlayerID = ""
	state.winnerPlayerID = state.opponentPlayerID(side)
	state.updatedAt = command.At

	payload := Delta{
		SideToMove:     state.sideToMove(),
		WinnerPlayerID: state.winnerPlayerID,
	}
	return state, newDelta(game.CommandResign, state.status, state.hash(), nil, payload), nil
}

func applyOfferDraw(state *State, command game.Command) (any, game.Delta, error) {
	if state.finished() {
		return state, game.Delta{}, game.ErrGameFinished
	}
	if state.sideForPlayer(command.PlayerID) == SideUnspecified {
		return state, game.Delta{}, game.ErrPlayerNotSeated
	}

	if state.drawOfferedByPlayerID != "" && state.drawOfferedByPlayerID != command.PlayerID {
		state.status = game.StatusDrawn
		state.drawOfferedByPlayerID = ""
	} else {
		state.status = game.StatusDrawOffered
		state.drawOfferedByPlayerID = command.PlayerID
	}
	state.updatedAt = command.At

	payload := Delta{
		SideToMove:            state.sideToMove(),
		DrawOfferedByPlayerID: state.drawOfferedByPlayerID,
	}
	return state, newDelta(game.CommandOfferDraw, state.status, state.hash(), nil, payload), nil
}

func moveFromCommand(command game.Command) (Move, error) {
	switch payload := command.Payload.(type) {
	case Move:
		return payload, nil
	case *Move:
		if payload == nil {
			return Move{}, fmt.Errorf("%w: xiangqi move payload is nil", game.ErrInvalidCommand)
		}
		return *payload, nil
	default:
		return Move{}, fmt.Errorf("%w: expected xiangqi move payload, got %T", game.ErrInvalidCommand, command.Payload)
	}
}

func resolveMove(position *xq.PositionNG, move Move) (xq.MoveNG, error) {
	if strings.TrimSpace(move.UCI) != "" {
		resolved, err := xq.ParseUCIMove(position, move.UCI)
		if err != nil {
			return xq.MOVE_NONE, fmt.Errorf("%w: %w", game.ErrIllegalMove, err)
		}
		return resolved, nil
	}
	if move.FromSquare >= xq.SQUARE_NB || move.ToSquare >= xq.SQUARE_NB {
		return xq.MOVE_NONE, fmt.Errorf("%w: square out of range", game.ErrIllegalMove)
	}

	wantFrom := xq.Square(move.FromSquare)
	wantTo := xq.Square(move.ToSquare)
	var moves [xq.MAX_MOVES]xq.MoveNG
	count := position.GenerateLEGAL(moves[:])
	for i := uint8(0); i < count; i++ {
		candidate := moves[i]
		if xq.FromSQ(candidate) == wantFrom && xq.ToSQ(candidate) == wantTo {
			return candidate, nil
		}
	}
	return xq.MOVE_NONE, fmt.Errorf("%w: %d->%d", game.ErrIllegalMove, move.FromSquare, move.ToSquare)
}

func (s *State) snapshot() game.Snapshot {
	board := make([]uint32, len(s.position.Board))
	for i, piece := range s.position.Board {
		board[i] = uint32(piece)
	}

	payload := Snapshot{
		FEN:                   s.position.FEN(),
		Board:                 board,
		SideToMove:            s.sideToMove(),
		RedPlayerID:           s.redPlayerID,
		BlackPlayerID:         s.blackPlayerID,
		WinnerPlayerID:        s.winnerPlayerID,
		DrawOfferedByPlayerID: s.drawOfferedByPlayerID,
	}
	return newSnapshot(s.status, s.hash(), payload)
}

func newSnapshot(status game.Status, stateHash uint64, payload Snapshot) game.Snapshot {
	base := game.Snapshot{
		Type:      game.TypeXiangqi,
		Status:    status,
		StateHash: stateHash,
	}
	payload.Snapshot = base
	base.Payload = payload
	return base
}

func newDelta(commandType game.CommandType, status game.Status, stateHash uint64, commandPayload any, payload Delta) game.Delta {
	base := game.Delta{
		Type:           game.TypeXiangqi,
		CommandType:    commandType,
		Status:         status,
		StateHash:      stateHash,
		CommandPayload: commandPayload,
	}
	payload.Delta = base
	base.Payload = payload
	return base
}

func (s *State) sideToMove() Side {
	if s.position.SideToMove == xq.BLACK {
		return SideBlack
	}
	return SideRed
}

func (s *State) sideForPlayer(playerID string) Side {
	switch playerID {
	case s.redPlayerID:
		return SideRed
	case s.blackPlayerID:
		return SideBlack
	default:
		return SideUnspecified
	}
}

func (s *State) opponentPlayerID(side Side) string {
	switch side {
	case SideRed:
		return s.blackPlayerID
	case SideBlack:
		return s.redPlayerID
	default:
		return ""
	}
}

func (s *State) finished() bool {
	return s.status == game.StatusResigned || s.status == game.StatusDrawn
}

func (s *State) hash() uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s.position.FEN()))
	_, _ = h.Write([]byte{byte(s.status)})
	_, _ = h.Write([]byte(s.redPlayerID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(s.blackPlayerID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(s.winnerPlayerID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(s.drawOfferedByPlayerID))

	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(s.position.MinorHash()))
	_, _ = h.Write(buf[:])
	return h.Sum64()
}

func stateFrom(raw any) (*State, error) {
	state, ok := raw.(*State)
	if !ok || state == nil || state.position == nil {
		return nil, fmt.Errorf("%w: %T", game.ErrUnsupportedState, raw)
	}
	return state, nil
}
