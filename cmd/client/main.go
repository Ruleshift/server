package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/Ruleshift/server/internal/protocol"
	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
	"github.com/gorilla/websocket"
)

const defaultMaxMessageBytes = 64 * 1024

type options struct {
	addr             string
	ticket           string
	roomID           string
	op               string
	value            int64
	lastSeenRevision uint64
	strictRevision   bool
	timeout          time.Duration
	watchDuration    time.Duration
	maxMessageBytes  int
	handshakeTimeout time.Duration
}

type cliClient struct {
	conn            *websocket.Conn
	maxMessageBytes int
	clientSequence  uint64
	playerID        string
	lastRevision    uint64
	currentValue    int64
}

func main() {
	opts := parseFlags()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "client error: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() options {
	opts := options{}
	flag.StringVar(&opts.addr, "addr", "ws://localhost:8080/ws", "gateway WebSocket address")
	flag.StringVar(&opts.ticket, "ticket", "mock:player-1", "mock auth ticket")
	flag.StringVar(&opts.roomID, "room", "room-1", "room id to join")
	flag.StringVar(&opts.op, "op", "get", "operation: get, add, set, watch")
	flag.Int64Var(&opts.value, "value", 1, "value for add/set operations")
	flag.Uint64Var(&opts.lastSeenRevision, "last-seen-revision", 0, "revision sent in JoinRoomRequest")
	flag.BoolVar(&opts.strictRevision, "strict-revision", false, "send the joined room revision as IntCommand.expected_revision instead of blind revision 0")
	flag.DurationVar(&opts.timeout, "timeout", 5*time.Second, "timeout for auth/join/get/add/set")
	flag.DurationVar(&opts.watchDuration, "watch-duration", 0, "watch duration; 0 means until Ctrl+C")
	flag.IntVar(&opts.maxMessageBytes, "max-message-bytes", defaultMaxMessageBytes, "max protobuf payload size")
	flag.DurationVar(&opts.handshakeTimeout, "handshake-timeout", 10*time.Second, "websocket handshake timeout")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Ruleshift CLI client\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Examples:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  go run ./cmd/client -addr ws://192.168.1.50:8080/ws -ticket mock:player-1 -room demo -op get\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  go run ./cmd/client -addr ws://192.168.1.50:8080/ws -ticket mock:player-1 -room demo -op add -value 5\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  go run ./cmd/client -addr ws://192.168.1.50:8080/ws -ticket mock:player-2 -room demo -op set -value 42\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  go run ./cmd/client -addr ws://192.168.1.50:8080/ws -ticket mock:watcher -room demo -op watch\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	return opts
}

func run(ctx context.Context, opts options) error {
	if err := opts.validate(); err != nil {
		return err
	}

	dialer := websocket.Dialer{HandshakeTimeout: opts.handshakeTimeout}
	conn, response, err := dialer.DialContext(ctx, opts.addr, nil)
	if err != nil {
		if response != nil {
			return fmt.Errorf("dial gateway %s: status=%s: %w", opts.addr, response.Status, err)
		}
		return fmt.Errorf("dial gateway %s: %w", opts.addr, err)
	}
	defer conn.Close()
	conn.SetReadLimit(int64(opts.maxMessageBytes))

	client := &cliClient{
		conn:            conn,
		maxMessageBytes: opts.maxMessageBytes,
		lastRevision:    opts.lastSeenRevision,
	}

	if err := runWithTimeout(ctx, opts.timeout, func(stepCtx context.Context) error {
		return client.authenticate(stepCtx, opts.ticket)
	}); err != nil {
		return err
	}
	if err := runWithTimeout(ctx, opts.timeout, func(stepCtx context.Context) error {
		return client.joinRoom(stepCtx, opts.roomID, opts.lastSeenRevision)
	}); err != nil {
		return err
	}

	switch strings.ToLower(opts.op) {
	case "get":
		return runWithTimeout(ctx, opts.timeout, func(stepCtx context.Context) error {
			return client.requestSnapshot(stepCtx, opts.roomID)
		})
	case "add":
		return runWithTimeout(ctx, opts.timeout, func(stepCtx context.Context) error {
			return client.sendIntCommand(stepCtx, opts.roomID, ruleshiftv1.IntOperation_INT_OPERATION_ADD, opts.value, opts.strictRevision)
		})
	case "set":
		return runWithTimeout(ctx, opts.timeout, func(stepCtx context.Context) error {
			return client.sendIntCommand(stepCtx, opts.roomID, ruleshiftv1.IntOperation_INT_OPERATION_SET, opts.value, opts.strictRevision)
		})
	case "watch":
		watchCtx := ctx
		if opts.watchDuration > 0 {
			var watchCancel context.CancelFunc
			watchCtx, watchCancel = context.WithTimeout(ctx, opts.watchDuration)
			defer watchCancel()
		}
		if err := runWithTimeout(ctx, opts.timeout, func(stepCtx context.Context) error {
			return client.requestSnapshot(stepCtx, opts.roomID)
		}); err != nil {
			return err
		}
		return client.watch(watchCtx)
	default:
		return fmt.Errorf("unsupported -op %q; expected get, add, set, or watch", opts.op)
	}
}

func runWithTimeout(ctx context.Context, timeout time.Duration, fn func(context.Context) error) error {
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return fn(stepCtx)
}

func (o options) validate() error {
	if o.addr == "" {
		return fmt.Errorf("-addr must not be empty")
	}
	if o.ticket == "" {
		return fmt.Errorf("-ticket must not be empty")
	}
	if o.roomID == "" {
		return fmt.Errorf("-room must not be empty")
	}
	if o.timeout <= 0 {
		return fmt.Errorf("-timeout must be positive")
	}
	if o.maxMessageBytes <= 0 {
		return fmt.Errorf("-max-message-bytes must be positive")
	}
	if o.handshakeTimeout <= 0 {
		return fmt.Errorf("-handshake-timeout must be positive")
	}
	return nil
}

func (c *cliClient) authenticate(ctx context.Context, ticket string) error {
	if err := c.send(ctx, &ruleshiftv1.ClientEnvelope{
		Payload: &ruleshiftv1.ClientEnvelope_AuthRequest{AuthRequest: &ruleshiftv1.AuthRequest{Ticket: ticket}},
	}); err != nil {
		return err
	}

	for {
		env, err := c.read(ctx)
		if err != nil {
			return err
		}
		switch payload := env.GetPayload().(type) {
		case *ruleshiftv1.ServerEnvelope_AuthOk:
			c.playerID = payload.AuthOk.GetPlayerId()
			fmt.Printf("auth ok player_id=%s display_name=%q\n", payload.AuthOk.GetPlayerId(), payload.AuthOk.GetDisplayName())
			return nil
		case *ruleshiftv1.ServerEnvelope_AuthFailed:
			return fmt.Errorf("auth failed: %s", payload.AuthFailed.GetReason())
		case *ruleshiftv1.ServerEnvelope_Error:
			return serverError(payload.Error)
		default:
			printEnvelope(env)
		}
	}
}

func (c *cliClient) joinRoom(ctx context.Context, roomID string, lastSeenRevision uint64) error {
	if err := c.send(ctx, &ruleshiftv1.ClientEnvelope{
		Payload: &ruleshiftv1.ClientEnvelope_JoinRoom{JoinRoom: &ruleshiftv1.JoinRoomRequest{
			RoomId:           roomID,
			LastSeenRevision: lastSeenRevision,
		}},
	}); err != nil {
		return err
	}

	var joinedRevision uint64
	for {
		env, err := c.read(ctx)
		if err != nil {
			return err
		}
		switch payload := env.GetPayload().(type) {
		case *ruleshiftv1.ServerEnvelope_JoinRoomOk:
			joinedRevision = payload.JoinRoomOk.GetCurrentRevision()
			c.lastRevision = joinedRevision
			fmt.Printf("join ok room=%s revision=%d\n", payload.JoinRoomOk.GetRoomId(), joinedRevision)
			if lastSeenRevision == joinedRevision {
				return nil
			}
		case *ruleshiftv1.ServerEnvelope_StateSnapshot:
			c.applySnapshot(payload.StateSnapshot)
			printSnapshot(payload.StateSnapshot)
			return nil
		case *ruleshiftv1.ServerEnvelope_Error:
			return serverError(payload.Error)
		default:
			printEnvelope(env)
		}
	}
}

func (c *cliClient) requestSnapshot(ctx context.Context, roomID string) error {
	if err := c.send(ctx, &ruleshiftv1.ClientEnvelope{
		Payload: &ruleshiftv1.ClientEnvelope_SnapshotRequest{SnapshotRequest: &ruleshiftv1.SnapshotRequest{
			RoomId:           roomID,
			LastSeenRevision: c.lastRevision,
		}},
	}); err != nil {
		return err
	}

	for {
		env, err := c.read(ctx)
		if err != nil {
			return err
		}
		switch payload := env.GetPayload().(type) {
		case *ruleshiftv1.ServerEnvelope_StateSnapshot:
			c.applySnapshot(payload.StateSnapshot)
			printSnapshot(payload.StateSnapshot)
			return nil
		case *ruleshiftv1.ServerEnvelope_StateDelta:
			c.applyDelta(payload.StateDelta)
			printDelta(payload.StateDelta)
		case *ruleshiftv1.ServerEnvelope_Error:
			return serverError(payload.Error)
		default:
			printEnvelope(env)
		}
	}
}

func (c *cliClient) sendIntCommand(ctx context.Context, roomID string, op ruleshiftv1.IntOperation, value int64, strictRevision bool) error {
	expectedRevision := uint64(0)
	if strictRevision {
		expectedRevision = c.lastRevision
	}

	if err := c.send(ctx, &ruleshiftv1.ClientEnvelope{
		Payload: &ruleshiftv1.ClientEnvelope_IntCommand{IntCommand: &ruleshiftv1.IntCommand{
			RoomId:           roomID,
			Operation:        op,
			Value:            value,
			ExpectedRevision: expectedRevision,
		}},
	}); err != nil {
		return err
	}

	for {
		env, err := c.read(ctx)
		if err != nil {
			return err
		}
		switch payload := env.GetPayload().(type) {
		case *ruleshiftv1.ServerEnvelope_StateSnapshot:
			c.applySnapshot(payload.StateSnapshot)
			printSnapshot(payload.StateSnapshot)
		case *ruleshiftv1.ServerEnvelope_StateDelta:
			c.applyDelta(payload.StateDelta)
			printDelta(payload.StateDelta)
			if payload.StateDelta.GetChangedByPlayerId() == c.playerID &&
				payload.StateDelta.GetOperation() == op &&
				payload.StateDelta.GetOperand() == value {
				return nil
			}
		case *ruleshiftv1.ServerEnvelope_Error:
			return serverError(payload.Error)
		default:
			printEnvelope(env)
		}
	}
}

func (c *cliClient) watch(ctx context.Context) error {
	fmt.Println("watching room events; press Ctrl+C to stop")
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	for {
		env, err := c.read(ctx)
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil
			}
			return err
		}
		switch payload := env.GetPayload().(type) {
		case *ruleshiftv1.ServerEnvelope_StateSnapshot:
			c.applySnapshot(payload.StateSnapshot)
			printSnapshot(payload.StateSnapshot)
		case *ruleshiftv1.ServerEnvelope_StateDelta:
			c.applyDelta(payload.StateDelta)
			printDelta(payload.StateDelta)
		case *ruleshiftv1.ServerEnvelope_Error:
			fmt.Printf("server error code=%s message=%q\n", payload.Error.GetCode(), payload.Error.GetMessage())
		default:
			printEnvelope(env)
		}
	}
}

func (c *cliClient) send(ctx context.Context, env *ruleshiftv1.ClientEnvelope) error {
	c.clientSequence++
	env.ProtocolVersion = protocol.CurrentVersion
	env.ClientSequence = c.clientSequence

	payload, err := protocol.EncodeClientEnvelope(env)
	if err != nil {
		return fmt.Errorf("encode client envelope: %w", err)
	}
	if len(payload) > c.maxMessageBytes {
		return fmt.Errorf("client envelope exceeds max message size: %d > %d", len(payload), c.maxMessageBytes)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetWriteDeadline(deadline); err != nil {
			return fmt.Errorf("set write deadline: %w", err)
		}
	}
	if err := c.conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		return fmt.Errorf("write websocket message: %w", err)
	}
	return nil
}

func (c *cliClient) read(ctx context.Context) (*ruleshiftv1.ServerEnvelope, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return nil, fmt.Errorf("set read deadline: %w", err)
		}
	} else if err := c.conn.SetReadDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("clear read deadline: %w", err)
	}

	messageType, payload, err := c.conn.ReadMessage()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("read websocket message: %w", err)
	}
	if messageType != websocket.BinaryMessage {
		return nil, fmt.Errorf("unsupported websocket message type %d", messageType)
	}
	if len(payload) > c.maxMessageBytes {
		return nil, fmt.Errorf("server envelope exceeds max message size: %d > %d", len(payload), c.maxMessageBytes)
	}

	env, err := protocol.DecodeServerEnvelope(payload)
	if err != nil {
		return nil, err
	}
	if env.GetProtocolVersion() != protocol.CurrentVersion {
		return nil, fmt.Errorf("unsupported server protocol version: got=%d want=%d", env.GetProtocolVersion(), protocol.CurrentVersion)
	}
	return env, nil
}

func (c *cliClient) applySnapshot(snapshot *ruleshiftv1.StateSnapshot) {
	c.currentValue = snapshot.GetValue()
	c.lastRevision = snapshot.GetRevision()
}

func (c *cliClient) applyDelta(delta *ruleshiftv1.StateDelta) {
	if delta.GetNewRevision() >= c.lastRevision {
		c.currentValue = delta.GetNewValue()
		c.lastRevision = delta.GetNewRevision()
	}
}

func serverError(message *ruleshiftv1.ErrorMessage) error {
	return fmt.Errorf("server error code=%s message=%q", message.GetCode(), message.GetMessage())
}

func printEnvelope(env *ruleshiftv1.ServerEnvelope) {
	fmt.Printf("server envelope sequence=%d payload=%T\n", env.GetServerSequence(), env.GetPayload())
}

func printSnapshot(snapshot *ruleshiftv1.StateSnapshot) {
	fmt.Printf("snapshot room=%s value=%d revision=%d\n", snapshot.GetRoomId(), snapshot.GetValue(), snapshot.GetRevision())
}

func printDelta(delta *ruleshiftv1.StateDelta) {
	fmt.Printf(
		"delta room=%s previous=%d new=%d revision=%d->%d changed_by=%s operation=%s operand=%d\n",
		delta.GetRoomId(),
		delta.GetPreviousValue(),
		delta.GetNewValue(),
		delta.GetPreviousRevision(),
		delta.GetNewRevision(),
		delta.GetChangedByPlayerId(),
		shortOperation(delta.GetOperation()),
		delta.GetOperand(),
	)
}

func shortOperation(op ruleshiftv1.IntOperation) string {
	return strings.TrimPrefix(op.String(), "INT_OPERATION_")
}
