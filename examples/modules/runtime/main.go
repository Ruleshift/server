package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	modulev2 "github.com/Ruleshift/server/internal/moduleruntime/generated/moduleruntimev2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/types/known/anypb"
)

type state struct {
	Players     []player `json:"players,omitempty"`
	FEN         string   `json:"fen,omitempty"`
	Side        string   `json:"side,omitempty"`
	Status      string   `json:"status"`
	LastMove    string   `json:"last_move,omitempty"`
	CurrentSeat *uint32  `json:"current_seat,omitempty"`
	Deck        []string `json:"deck,omitempty"`
	Discard     []string `json:"discard,omitempty"`
}

type player struct {
	SeatIndex uint32   `json:"seat_index"`
	Secret    *int64   `json:"secret,omitempty"`
	Hand      []string `json:"hand,omitempty"`
}

type command struct {
	Kind         string  `json:"kind"`
	Value        int64   `json:"value,omitempty"`
	Move         string  `json:"move,omitempty"`
	CardID       string  `json:"card_id,omitempty"`
	TargetSeat   *uint32 `json:"target_seat,omitempty"`
	TargetCardID string  `json:"target_card_id,omitempty"`
}

type viewPlayer struct {
	SeatIndex   uint32   `json:"seat_index"`
	HasSecret   bool     `json:"has_secret,omitempty"`
	Secret      *int64   `json:"secret,omitempty"`
	HandCount   int      `json:"hand_count,omitempty"`
	PrivateHand []string `json:"private_hand,omitempty"`
}

type view struct {
	Players     []viewPlayer `json:"players,omitempty"`
	FEN         string       `json:"fen,omitempty"`
	Side        string       `json:"side,omitempty"`
	Status      string       `json:"status"`
	LastMove    string       `json:"last_move,omitempty"`
	CurrentSeat *uint32      `json:"current_seat,omitempty"`
	DeckCount   int          `json:"deck_count,omitempty"`
	Discard     []string     `json:"discard,omitempty"`
}

type server struct {
	modulev2.UnimplementedModuleRuntimeServer
	id, version, prefix, token string
	descriptorDigest           []byte
}

func main() {
	id := env("RULESHIFT_MODULE_ID", "")
	if id != "xiangqi" && id != "hiddennumber" && id != "cardgame" {
		log.Fatal("RULESHIFT_MODULE_ID must be xiangqi, hiddennumber, or cardgame")
	}
	listener, err := net.Listen("tcp", env("RULESHIFT_MODULE_ADDR", ":50051"))
	if err != nil {
		log.Fatal(err)
	}
	service := &server{id: id, version: env("RULESHIFT_MODULE_VERSION", "2.0.0"), prefix: "type.googleapis.com/ruleshift.examples." + id + ".v1.", token: os.Getenv("RULESHIFT_MODULE_RPC_TOKEN"), descriptorDigest: descriptorSHA()}
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(service.authorize))
	modulev2.RegisterModuleRuntimeServer(grpcServer, service)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(grpcServer, healthServer)
	log.Printf("%s module listening on %s", id, listener.Addr())
	log.Fatal(grpcServer.Serve(listener))
}

func (s *server) authorize(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	// Kubernetes' native gRPC probe cannot attach module authorization metadata.
	if info.FullMethod == healthv1.Health_Check_FullMethodName {
		return handler(ctx, request)
	}
	if s.token == "" {
		return handler(ctx, request)
	}
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) != 1 || values[0] != "Bearer "+s.token {
		return nil, status.Error(codes.Unauthenticated, "invalid module RPC token")
	}
	return handler(ctx, request)
}

func (s *server) Describe(context.Context, *modulev2.DescribeRequest) (*modulev2.DescribeResponse, error) {
	return &modulev2.DescribeResponse{
		ModuleId:            s.id,
		Version:             s.version,
		AbiVersion:          2,
		StateTypeUrl:        s.prefix + "State",
		CommandTypeUrls:     []string{s.prefix + "Command"},
		DescriptorSetSha256: s.descriptorDigest,
	}, nil
}

func (s *server) CreateState(_ context.Context, request *modulev2.CreateStateRequest) (*modulev2.TransitionResponse, error) {
	if err := validContext(request.GetContext()); err != nil {
		return nil, err
	}
	if request.GetSetup() == nil {
		return nil, status.Error(codes.InvalidArgument, "setup is required")
	}
	playerCount := request.GetSetup().GetPlayerCount()
	if !s.supportsPlayerCount(playerCount) {
		return nil, status.Error(codes.InvalidArgument, "unsupported player_count")
	}
	value := state{Status: "active", Players: make([]player, playerCount)}
	for seat := range playerCount {
		value.Players[seat].SeatIndex = seat
	}
	switch s.id {
	case "xiangqi":
		value.FEN = "rnbakabnr/9/1c5c1/p1p1p1p1p/9/9/P1P1P1P1P/1C5C1/9/RNBAKABNR w - - 0 1"
		value.Side = "red"
	case "cardgame":
		value.Status = "waiting"
	}
	return s.transition(value, map[string]any{"kind": "created", "player_count": playerCount}, true)
}

func (s *server) Apply(_ context.Context, request *modulev2.ApplyRequest) (*modulev2.TransitionResponse, error) {
	if err := validContext(request.GetContext()); err != nil {
		return nil, err
	}
	if request.Command == nil || request.Command.TypeUrl != s.prefix+"Command" {
		return nil, status.Error(codes.InvalidArgument, "wrong command type")
	}
	value, err := s.decodeState(request.State)
	if err != nil {
		return nil, err
	}
	actor := request.GetActor()
	if actor == nil || actor.GetPlayerId() == "" || int(actor.GetSeatIndex()) >= len(value.Players) {
		return nil, status.Error(codes.FailedPrecondition, "actor is not seated")
	}
	var cmd command
	if err = decodeWireJSON(request.Command.Value, &cmd); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	seat := actor.GetSeatIndex()
	delta := map[string]any{"kind": cmd.Kind, "seat_index": seat}
	switch s.id {
	case "hiddennumber":
		if cmd.Kind != "set_secret" || cmd.Value < 0 || cmd.Value > 999999 {
			return nil, status.Error(codes.InvalidArgument, "invalid secret")
		}
		secret := cmd.Value
		value.Players[seat].Secret = &secret
	case "xiangqi":
		if err = applyXiangqi(&value, seat, cmd); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	case "cardgame":
		if err = applyCardgame(&value, seat, cmd, request.Context.Seed); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	return s.transition(value, delta, true)
}

func (s *server) ProjectSnapshot(_ context.Context, request *modulev2.ProjectRequest) (*modulev2.ProjectionResponse, error) {
	value, err := s.decodeState(request.State)
	if err != nil {
		return nil, err
	}
	payload, err := s.project(value, request.Viewer)
	if err != nil {
		return nil, err
	}
	return &modulev2.ProjectionResponse{Payload: &anypb.Any{TypeUrl: s.prefix + "View", Value: payload}}, nil
}

func (s *server) ProjectDelta(_ context.Context, request *modulev2.ProjectDeltaRequest) (*modulev2.ProjectionResponse, error) {
	before, err := s.decodeState(request.BeforeState)
	if err != nil {
		return nil, err
	}
	after, err := s.decodeState(request.AfterState)
	if err != nil {
		return nil, err
	}
	beforeView, err := s.project(before, request.Viewer)
	if err != nil {
		return nil, err
	}
	afterView, err := s.project(after, request.Viewer)
	if err != nil {
		return nil, err
	}
	return &modulev2.ProjectionResponse{Payload: &anypb.Any{TypeUrl: s.prefix + "View", Value: afterView}, NoVisibleChange: bytes.Equal(beforeView, afterView)}, nil
}

func (s *server) transition(value state, delta any, changed bool) (*modulev2.TransitionResponse, error) {
	stateJSON, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	deltaJSON, err := json.Marshal(delta)
	if err != nil {
		return nil, err
	}
	return &modulev2.TransitionResponse{
		Changed:   changed,
		NextState: &anypb.Any{TypeUrl: s.prefix + "State", Value: encodeWireJSON(stateJSON)},
		Delta:     &anypb.Any{TypeUrl: s.prefix + "Delta", Value: encodeWireJSON(deltaJSON)},
	}, nil
}

func (s *server) decodeState(value *anypb.Any) (state, error) {
	if value == nil || value.TypeUrl != s.prefix+"State" {
		return state{}, status.Error(codes.InvalidArgument, "wrong state type")
	}
	var result state
	if err := decodeWireJSON(value.Value, &result); err != nil {
		return state{}, status.Error(codes.InvalidArgument, err.Error())
	}
	return result, nil
}

func (s *server) project(value state, viewer *modulev2.Viewer) ([]byte, error) {
	result := view{
		FEN:         value.FEN,
		Side:        value.Side,
		Status:      value.Status,
		LastMove:    value.LastMove,
		CurrentSeat: cloneSeat(value.CurrentSeat),
		DeckCount:   len(value.Deck),
		Discard:     append([]string(nil), value.Discard...),
	}
	for _, p := range value.Players {
		projected := viewPlayer{SeatIndex: p.SeatIndex, HasSecret: p.Secret != nil, HandCount: len(p.Hand)}
		canSee := viewer != nil && (viewer.Scope == modulev2.ViewScope_VIEW_SCOPE_FULL ||
			(viewer.Scope == modulev2.ViewScope_VIEW_SCOPE_PLAYER && viewer.SeatIndex != nil && viewer.GetSeatIndex() == p.SeatIndex))
		if canSee {
			if p.Secret != nil {
				secret := *p.Secret
				projected.Secret = &secret
			}
			projected.PrivateHand = append([]string(nil), p.Hand...)
		}
		result.Players = append(result.Players, projected)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return encodeWireJSON(raw), nil
}

func (s *server) supportsPlayerCount(count uint32) bool {
	switch s.id {
	case "xiangqi", "hiddennumber":
		return count == 2
	case "cardgame":
		return count >= 2 && count <= 6
	default:
		return false
	}
}

func applyXiangqi(value *state, seat uint32, cmd command) error {
	expectedSeat := uint32(0)
	if value.Side == "black" {
		expectedSeat = 1
	}
	if seat != expectedSeat {
		return errors.New("not actor's turn")
	}
	switch cmd.Kind {
	case "move":
		if cmd.Move == "" {
			return errors.New("move is required")
		}
		value.LastMove = cmd.Move
		if value.Side == "red" {
			value.Side = "black"
		} else {
			value.Side = "red"
		}
	case "resign":
		value.Status = "resigned"
	case "offer_draw":
		value.Status = "draw_offered"
	default:
		return errors.New("unknown command")
	}
	return nil
}

func applyCardgame(value *state, seat uint32, cmd command, seed uint64) error {
	switch cmd.Kind {
	case "start":
		if value.Status != "waiting" || seat != 0 {
			return errors.New("cannot start")
		}
		value.Status = "active"
		value.CurrentSeat = seatPointer(0)
		value.Deck = make([]string, 0, 40)
		for i := 0; i < 40; i++ {
			value.Deck = append(value.Deck, fmt.Sprintf("card-%d-%d", seed%997, uint64(i)))
		}
		for round := 0; round < 3; round++ {
			for index := range value.Players {
				value.Players[index].Hand = append(value.Players[index].Hand, value.Deck[0])
				value.Deck = value.Deck[1:]
			}
		}
	case "play_card":
		if value.Status != "active" || value.CurrentSeat == nil || *value.CurrentSeat != seat {
			return errors.New("not actor's turn")
		}
		cardIndex := -1
		for i, card := range value.Players[seat].Hand {
			if card == cmd.CardID {
				cardIndex = i
				break
			}
		}
		if cardIndex < 0 {
			return errors.New("card not in hand")
		}
		value.Players[seat].Hand = append(value.Players[seat].Hand[:cardIndex:cardIndex], value.Players[seat].Hand[cardIndex+1:]...)
		value.Discard = append(value.Discard, cmd.CardID)
	case "end_turn":
		if value.Status != "active" || value.CurrentSeat == nil || *value.CurrentSeat != seat {
			return errors.New("not actor's turn")
		}
		value.CurrentSeat = seatPointer((seat + 1) % uint32(len(value.Players)))
	case "attach_modifier":
		if value.Status != "active" || value.CurrentSeat == nil || *value.CurrentSeat != seat {
			return errors.New("not actor's turn")
		}
		if cmd.CardID == "" || cmd.TargetCardID == "" || cmd.TargetSeat == nil || int(*cmd.TargetSeat) >= len(value.Players) {
			return errors.New("modifier, target seat, and target card are required")
		}
	default:
		return errors.New("unknown card command")
	}
	return nil
}

func validContext(value *modulev2.DeterministicContext) error {
	if value == nil {
		return status.Error(codes.InvalidArgument, "deterministic context is required")
	}
	return nil
}

func seatPointer(value uint32) *uint32 { return &value }

func cloneSeat(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	return seatPointer(*value)
}

func encodeWireJSON(raw []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), raw)
}

func decodeWireJSON(raw []byte, target any) error {
	number, kind, n := protowire.ConsumeTag(raw)
	if n < 0 || number != 1 || kind != protowire.BytesType {
		return errors.New("invalid module protobuf")
	}
	value, m := protowire.ConsumeBytes(raw[n:])
	if m < 0 {
		return errors.New("invalid module protobuf payload")
	}
	return json.Unmarshal(value, target)
}

func descriptorSHA() []byte {
	raw, err := os.ReadFile(env("RULESHIFT_DESCRIPTOR_PATH", "/app/descriptor.pb"))
	if err != nil {
		value := os.Getenv("RULESHIFT_DESCRIPTOR_SHA256")
		decoded, _ := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
		return decoded
	}
	sum := sha256.Sum256(raw)
	return sum[:]
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
