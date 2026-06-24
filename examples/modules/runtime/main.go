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

	modulev1 "github.com/Ruleshift/server/internal/moduleruntime/generated/moduleruntimev1"
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
	Players       []player `json:"players,omitempty"`
	FEN           string   `json:"fen,omitempty"`
	Side          string   `json:"side,omitempty"`
	Status        string   `json:"status"`
	LastMove      string   `json:"last_move,omitempty"`
	CurrentPlayer string   `json:"current_player,omitempty"`
	Deck          []string `json:"deck,omitempty"`
	Discard       []string `json:"discard,omitempty"`
}
type player struct {
	ID     string   `json:"id"`
	Secret *int64   `json:"secret,omitempty"`
	Hand   []string `json:"hand,omitempty"`
}
type command struct {
	Kind           string `json:"kind"`
	Value          int64  `json:"value,omitempty"`
	Move           string `json:"move,omitempty"`
	CardID         string `json:"card_id,omitempty"`
	TargetPlayerID string `json:"target_player_id,omitempty"`
	TargetCardID   string `json:"target_card_id,omitempty"`
}
type viewPlayer struct {
	ID          string   `json:"id"`
	HasSecret   bool     `json:"has_secret,omitempty"`
	Secret      *int64   `json:"secret,omitempty"`
	HandCount   int      `json:"hand_count,omitempty"`
	PrivateHand []string `json:"private_hand,omitempty"`
}
type view struct {
	Players       []viewPlayer `json:"players,omitempty"`
	FEN           string       `json:"fen,omitempty"`
	Side          string       `json:"side,omitempty"`
	Status        string       `json:"status"`
	LastMove      string       `json:"last_move,omitempty"`
	CurrentPlayer string       `json:"current_player,omitempty"`
	DeckCount     int          `json:"deck_count,omitempty"`
	Discard       []string     `json:"discard,omitempty"`
}

type server struct {
	modulev1.UnimplementedModuleRuntimeServer
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
	service := &server{id: id, version: env("RULESHIFT_MODULE_VERSION", "1.0.0"), prefix: "type.googleapis.com/ruleshift.examples." + id + ".v1.", token: os.Getenv("RULESHIFT_MODULE_RPC_TOKEN"), descriptorDigest: descriptorSHA()}
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(service.authorize))
	modulev1.RegisterModuleRuntimeServer(grpcServer, service)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(grpcServer, healthServer)
	log.Printf("%s module listening on %s", id, listener.Addr())
	log.Fatal(grpcServer.Serve(listener))
}

func (s *server) authorize(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if s.token == "" {
		return handler(ctx, request)
	}
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) != 1 || values[0] != "Bearer "+s.token {
		return nil, status.Error(codes.Unauthenticated, "invalid module RPC token")
	}
	return handler(ctx, request)
}
func (s *server) Describe(context.Context, *modulev1.DescribeRequest) (*modulev1.DescribeResponse, error) {
	return &modulev1.DescribeResponse{ModuleId: s.id, Version: s.version, AbiVersion: 1, StateTypeUrl: s.prefix + "State", CommandTypeUrls: []string{s.prefix + "Command"}, DescriptorSetSha256: s.descriptorDigest, SupportsPlayerLeft: true}, nil
}
func (s *server) NewState(_ context.Context, request *modulev1.NewStateRequest) (*modulev1.TransitionResponse, error) {
	if err := validContext(request.GetContext()); err != nil {
		return nil, err
	}
	value := state{Status: "waiting"}
	if s.id == "xiangqi" {
		value.FEN = "rnbakabnr/9/1c5c1/p1p1p1p1p/9/9/P1P1P1P1P/1C5C1/9/RNBAKABNR w - - 0 1"
		value.Side = "red"
		value.Status = "active"
	}
	return s.transition(value, map[string]any{"kind": "created"}, true)
}
func (s *server) PlayerJoined(_ context.Context, request *modulev1.PlayerTransitionRequest) (*modulev1.TransitionResponse, error) {
	value, err := s.decodeState(request.GetState())
	if err != nil {
		return nil, err
	}
	for _, existing := range value.Players {
		if existing.ID == request.PlayerId {
			return s.transition(value, map[string]any{"kind": "already_joined"}, false)
		}
	}
	limit := 2
	if s.id == "cardgame" {
		limit = 6
	}
	if len(value.Players) >= limit {
		return nil, status.Error(codes.FailedPrecondition, "room full")
	}
	value.Players = append(value.Players, player{ID: request.PlayerId})
	return s.transition(value, map[string]any{"kind": "player_joined", "player_id": request.PlayerId}, true)
}
func (s *server) PlayerLeft(_ context.Context, request *modulev1.PlayerTransitionRequest) (*modulev1.TransitionResponse, error) {
	value, err := s.decodeState(request.GetState())
	if err != nil {
		return nil, err
	}
	if s.id == "cardgame" && value.Status == "active" {
		return s.transition(value, map[string]any{"kind": "player_remains_seated"}, false)
	}
	for index, existing := range value.Players {
		if existing.ID == request.PlayerId {
			value.Players = append(value.Players[:index:index], value.Players[index+1:]...)
			return s.transition(value, map[string]any{"kind": "player_left", "player_id": request.PlayerId}, true)
		}
	}
	return s.transition(value, map[string]any{"kind": "not_joined"}, false)
}
func (s *server) Apply(_ context.Context, request *modulev1.ApplyRequest) (*modulev1.TransitionResponse, error) {
	if request.Command == nil || request.Command.TypeUrl != s.prefix+"Command" {
		return nil, status.Error(codes.InvalidArgument, "wrong command type")
	}
	value, err := s.decodeState(request.State)
	if err != nil {
		return nil, err
	}
	var cmd command
	if err = decodeWireJSON(request.Command.Value, &cmd); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if !hasPlayer(value, request.PlayerId) {
		return nil, status.Error(codes.FailedPrecondition, "player not seated")
	}
	delta := map[string]any{"kind": cmd.Kind, "player_id": request.PlayerId}
	switch s.id {
	case "hiddennumber":
		if cmd.Kind != "set_secret" || cmd.Value < 0 || cmd.Value > 999999 {
			return nil, status.Error(codes.InvalidArgument, "invalid secret")
		}
		for index := range value.Players {
			if value.Players[index].ID == request.PlayerId {
				secret := cmd.Value
				value.Players[index].Secret = &secret
			}
		}
	case "xiangqi":
		if cmd.Kind == "move" {
			if cmd.Move == "" {
				return nil, status.Error(codes.InvalidArgument, "move is required")
			}
			value.LastMove = cmd.Move
			if value.Side == "red" {
				value.Side = "black"
			} else {
				value.Side = "red"
			}
		} else if cmd.Kind == "resign" {
			value.Status = "resigned"
		} else if cmd.Kind == "offer_draw" {
			value.Status = "draw_offered"
		} else {
			return nil, status.Error(codes.InvalidArgument, "unknown command")
		}
	case "cardgame":
		if err = applyCardgame(&value, request.PlayerId, cmd, request.Context.Seed); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	return s.transition(value, delta, true)
}
func (s *server) ProjectSnapshot(_ context.Context, request *modulev1.ProjectRequest) (*modulev1.ProjectionResponse, error) {
	value, err := s.decodeState(request.State)
	if err != nil {
		return nil, err
	}
	payload, err := s.project(value, request.Viewer)
	if err != nil {
		return nil, err
	}
	return &modulev1.ProjectionResponse{Payload: &anypb.Any{TypeUrl: s.prefix + "View", Value: payload}}, nil
}
func (s *server) ProjectDelta(_ context.Context, request *modulev1.ProjectDeltaRequest) (*modulev1.ProjectionResponse, error) {
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
	return &modulev1.ProjectionResponse{Payload: &anypb.Any{TypeUrl: s.prefix + "View", Value: afterView}, NoVisibleChange: bytes.Equal(beforeView, afterView)}, nil
}

func (s *server) transition(value state, delta any, changed bool) (*modulev1.TransitionResponse, error) {
	stateJSON, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	deltaJSON, err := json.Marshal(delta)
	if err != nil {
		return nil, err
	}
	return &modulev1.TransitionResponse{Changed: changed, NextState: &anypb.Any{TypeUrl: s.prefix + "State", Value: encodeWireJSON(stateJSON)}, Delta: &anypb.Any{TypeUrl: s.prefix + "Delta", Value: encodeWireJSON(deltaJSON)}}, nil
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
func (s *server) project(value state, viewer *modulev1.Viewer) ([]byte, error) {
	result := view{FEN: value.FEN, Side: value.Side, Status: value.Status, LastMove: value.LastMove, CurrentPlayer: value.CurrentPlayer, DeckCount: len(value.Deck), Discard: append([]string(nil), value.Discard...)}
	for _, p := range value.Players {
		projected := viewPlayer{ID: p.ID, HasSecret: p.Secret != nil, HandCount: len(p.Hand)}
		canSee := viewer != nil && (viewer.Scope == modulev1.ViewScope_VIEW_SCOPE_FULL || (viewer.Scope == modulev1.ViewScope_VIEW_SCOPE_PLAYER && viewer.PlayerId == p.ID))
		if canSee {
			if p.Secret != nil {
				x := *p.Secret
				projected.Secret = &x
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
func applyCardgame(value *state, playerID string, cmd command, seed uint64) error {
	switch cmd.Kind {
	case "start":
		if value.Status != "waiting" || len(value.Players) < 2 || value.Players[0].ID != playerID {
			return errors.New("cannot start")
		}
		value.Status = "active"
		value.CurrentPlayer = value.Players[0].ID
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
		index := playerIndex(*value, playerID)
		cardIndex := -1
		for i, card := range value.Players[index].Hand {
			if card == cmd.CardID {
				cardIndex = i
				break
			}
		}
		if cardIndex < 0 {
			return errors.New("card not in hand")
		}
		value.Players[index].Hand = append(value.Players[index].Hand[:cardIndex:cardIndex], value.Players[index].Hand[cardIndex+1:]...)
		value.Discard = append(value.Discard, cmd.CardID)
	case "end_turn":
		index := playerIndex(*value, playerID)
		value.CurrentPlayer = value.Players[(index+1)%len(value.Players)].ID
	case "attach_modifier":
		if cmd.CardID == "" || cmd.TargetCardID == "" {
			return errors.New("modifier and target are required")
		}
	default:
		return errors.New("unknown card command")
	}
	return nil
}
func playerIndex(value state, id string) int {
	for index, p := range value.Players {
		if p.ID == id {
			return index
		}
	}
	return -1
}
func hasPlayer(value state, id string) bool { return playerIndex(value, id) >= 0 }
func validContext(value *modulev1.RoomContext) error {
	if value == nil || value.OperationId == "" || value.RoomId == "" {
		return status.Error(codes.InvalidArgument, "operation_id and room_id are required")
	}
	return nil
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
