package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
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
	lastSeenRevision uint64
	strictRevision   bool
	timeout          time.Duration
	maxMessageBytes  int
	handshakeTimeout time.Duration
}

type consoleClient struct {
	conn            *websocket.Conn
	maxMessageBytes int

	writeMu sync.Mutex

	stateMu        sync.RWMutex
	clientSequence uint64
	playerID       string
	roomID         string
	currentValue   int64
	lastRevision   uint64
	strictRevision bool
}

func main() {
	opts := parseFlags()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "console error: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() options {
	opts := options{}
	flag.StringVar(&opts.addr, "addr", "ws://localhost:8080/ws", "gateway WebSocket address")
	flag.StringVar(&opts.ticket, "ticket", "mock:player-1", "mock auth ticket")
	flag.StringVar(&opts.roomID, "room", "demo", "room id to join")
	flag.Uint64Var(&opts.lastSeenRevision, "last-seen-revision", 0, "revision sent in JoinRoomRequest")
	flag.BoolVar(&opts.strictRevision, "strict-revision", false, "send the current room revision as IntCommand.expected_revision")
	flag.DurationVar(&opts.timeout, "timeout", 5*time.Second, "timeout for auth/join/commands")
	flag.IntVar(&opts.maxMessageBytes, "max-message-bytes", defaultMaxMessageBytes, "max protobuf payload size")
	flag.DurationVar(&opts.handshakeTimeout, "handshake-timeout", 10*time.Second, "websocket handshake timeout")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Ruleshift interactive console\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Example:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  go run ./cmd/console -addr ws://147.45.211.122:8080/ws -ticket mock:player-1 -room demo\n\n")
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

	client := &consoleClient{
		conn:            conn,
		maxMessageBytes: opts.maxMessageBytes,
		roomID:          opts.roomID,
		lastRevision:    opts.lastSeenRevision,
		strictRevision:  opts.strictRevision,
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
	if err := runWithTimeout(ctx, opts.timeout, func(stepCtx context.Context) error {
		return client.requestSnapshot(stepCtx)
	}); err != nil {
		return err
	}

	fmt.Println()
	printHelp()
	fmt.Println()

	readErr := make(chan error, 1)
	go func() {
		readErr <- client.readLoop(ctx)
	}()

	lines := make(chan string)
	go scanInput(ctx, client, lines)

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			if err == nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			shouldQuit, err := client.handleCommand(ctx, opts.timeout, line)
			if err != nil {
				fmt.Printf("command error: %v\n", err)
			}
			if shouldQuit {
				return nil
			}
		}
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

func scanInput(ctx context.Context, client *consoleClient, lines chan<- string) {
	defer close(lines)

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print(client.prompt())
		if !scanner.Scan() {
			return
		}

		select {
		case lines <- scanner.Text():
		case <-ctx.Done():
			return
		}
	}
}

func (c *consoleClient) prompt() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return fmt.Sprintf("ruleshift[%s r%d=%d]> ", c.roomID, c.lastRevision, c.currentValue)
}

func (c *consoleClient) authenticate(ctx context.Context, ticket string) error {
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
			c.stateMu.Lock()
			c.playerID = payload.AuthOk.GetPlayerId()
			c.stateMu.Unlock()
			fmt.Printf("auth ok player_id=%s display_name=%q\n", payload.AuthOk.GetPlayerId(), payload.AuthOk.GetDisplayName())
			return nil
		case *ruleshiftv1.ServerEnvelope_AuthFailed:
			return fmt.Errorf("auth failed: %s", payload.AuthFailed.GetReason())
		case *ruleshiftv1.ServerEnvelope_Error:
			return serverError(payload.Error)
		default:
			c.applyAndPrint(env)
		}
	}
}

func (c *consoleClient) joinRoom(ctx context.Context, roomID string, lastSeenRevision uint64) error {
	if err := c.sendJoinRoom(ctx, roomID, lastSeenRevision); err != nil {
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
			c.stateMu.Lock()
			c.roomID = payload.JoinRoomOk.GetRoomId()
			c.lastRevision = joinedRevision
			c.stateMu.Unlock()
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
			c.applyAndPrint(env)
		}
	}
}

func (c *consoleClient) requestSnapshot(ctx context.Context) error {
	c.stateMu.RLock()
	roomID := c.roomID
	lastRevision := c.lastRevision
	c.stateMu.RUnlock()

	if err := c.send(ctx, &ruleshiftv1.ClientEnvelope{
		Payload: &ruleshiftv1.ClientEnvelope_SnapshotRequest{SnapshotRequest: &ruleshiftv1.SnapshotRequest{
			RoomId:           roomID,
			LastSeenRevision: lastRevision,
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
			c.applyAndPrint(env)
		}
	}
}

func (c *consoleClient) readLoop(ctx context.Context) error {
	for {
		env, err := c.read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		c.applyAndPrint(env)
	}
}

func (c *consoleClient) handleCommand(ctx context.Context, timeout time.Duration, line string) (bool, error) {
	cmd, args := parseConsoleLine(line)
	switch cmd {
	case "":
		return false, nil
	case "q", "quit", "exit":
		return true, nil
	case "h", "help", "?":
		printHelp()
		return false, nil
	case "status":
		c.printStatus()
		return false, nil
	case "get":
		if len(args) != 0 {
			return false, fmt.Errorf("usage: get")
		}
		return false, runWithTimeout(ctx, timeout, c.sendSnapshotRequest)
	case "add":
		value, err := parseOneInt64(args, "add <value>")
		if err != nil {
			return false, err
		}
		return false, runWithTimeout(ctx, timeout, func(stepCtx context.Context) error {
			return c.sendIntCommand(stepCtx, ruleshiftv1.IntOperation_INT_OPERATION_ADD, value)
		})
	case "set":
		value, err := parseOneInt64(args, "set <value>")
		if err != nil {
			return false, err
		}
		return false, runWithTimeout(ctx, timeout, func(stepCtx context.Context) error {
			return c.sendIntCommand(stepCtx, ruleshiftv1.IntOperation_INT_OPERATION_SET, value)
		})
	case "room":
		if len(args) != 1 || args[0] == "" {
			return false, fmt.Errorf("usage: room <room-id>")
		}
		roomID := args[0]
		return false, runWithTimeout(ctx, timeout, func(stepCtx context.Context) error {
			c.stateMu.Lock()
			c.roomID = roomID
			c.currentValue = 0
			c.lastRevision = 0
			c.stateMu.Unlock()
			if err := c.sendJoinRoom(stepCtx, roomID, 0); err != nil {
				return err
			}
			return c.sendSnapshotRequest(stepCtx)
		})
	case "strict":
		if len(args) != 1 {
			return false, fmt.Errorf("usage: strict on|off")
		}
		switch strings.ToLower(args[0]) {
		case "on", "true", "1":
			c.setStrictRevision(true)
		case "off", "false", "0":
			c.setStrictRevision(false)
		default:
			return false, fmt.Errorf("usage: strict on|off")
		}
		c.printStatus()
		return false, nil
	case "ping":
		return false, runWithTimeout(ctx, timeout, c.sendPing)
	default:
		return false, fmt.Errorf("unknown command %q; type help", cmd)
	}
}

func parseConsoleLine(line string) (string, []string) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return "", nil
	}
	return strings.ToLower(fields[0]), fields[1:]
}

func parseOneInt64(args []string, usage string) (int64, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("usage: %s", usage)
	}
	value, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse value %q: %w", args[0], err)
	}
	return value, nil
}

func (c *consoleClient) sendJoinRoom(ctx context.Context, roomID string, lastSeenRevision uint64) error {
	return c.send(ctx, &ruleshiftv1.ClientEnvelope{
		Payload: &ruleshiftv1.ClientEnvelope_JoinRoom{JoinRoom: &ruleshiftv1.JoinRoomRequest{
			RoomId:           roomID,
			LastSeenRevision: lastSeenRevision,
		}},
	})
}

func (c *consoleClient) sendSnapshotRequest(ctx context.Context) error {
	c.stateMu.RLock()
	roomID := c.roomID
	lastRevision := c.lastRevision
	c.stateMu.RUnlock()

	return c.send(ctx, &ruleshiftv1.ClientEnvelope{
		Payload: &ruleshiftv1.ClientEnvelope_SnapshotRequest{SnapshotRequest: &ruleshiftv1.SnapshotRequest{
			RoomId:           roomID,
			LastSeenRevision: lastRevision,
		}},
	})
}

func (c *consoleClient) sendIntCommand(ctx context.Context, op ruleshiftv1.IntOperation, value int64) error {
	c.stateMu.RLock()
	roomID := c.roomID
	expectedRevision := uint64(0)
	if c.strictRevision {
		expectedRevision = c.lastRevision
	}
	c.stateMu.RUnlock()

	return c.send(ctx, &ruleshiftv1.ClientEnvelope{
		Payload: &ruleshiftv1.ClientEnvelope_IntCommand{IntCommand: &ruleshiftv1.IntCommand{
			RoomId:           roomID,
			Operation:        op,
			Value:            value,
			ExpectedRevision: expectedRevision,
		}},
	})
}

func (c *consoleClient) sendPing(ctx context.Context) error {
	return c.send(ctx, &ruleshiftv1.ClientEnvelope{
		Payload: &ruleshiftv1.ClientEnvelope_Ping{Ping: &ruleshiftv1.Ping{
			ClientTimeUnixMs: time.Now().UnixMilli(),
		}},
	})
}

func (c *consoleClient) send(ctx context.Context, env *ruleshiftv1.ClientEnvelope) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.stateMu.Lock()
	c.clientSequence++
	env.ProtocolVersion = protocol.CurrentVersion
	env.ClientSequence = c.clientSequence
	c.stateMu.Unlock()

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

func (c *consoleClient) read(ctx context.Context) (*ruleshiftv1.ServerEnvelope, error) {
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

func (c *consoleClient) applyAndPrint(env *ruleshiftv1.ServerEnvelope) {
	switch payload := env.GetPayload().(type) {
	case *ruleshiftv1.ServerEnvelope_AuthOk:
		c.stateMu.Lock()
		c.playerID = payload.AuthOk.GetPlayerId()
		c.stateMu.Unlock()
		fmt.Printf("auth ok player_id=%s display_name=%q\n", payload.AuthOk.GetPlayerId(), payload.AuthOk.GetDisplayName())
	case *ruleshiftv1.ServerEnvelope_AuthFailed:
		fmt.Printf("auth failed reason=%q\n", payload.AuthFailed.GetReason())
	case *ruleshiftv1.ServerEnvelope_JoinRoomOk:
		c.stateMu.Lock()
		c.roomID = payload.JoinRoomOk.GetRoomId()
		c.lastRevision = payload.JoinRoomOk.GetCurrentRevision()
		c.stateMu.Unlock()
		fmt.Printf("join ok room=%s revision=%d\n", payload.JoinRoomOk.GetRoomId(), payload.JoinRoomOk.GetCurrentRevision())
	case *ruleshiftv1.ServerEnvelope_StateSnapshot:
		c.applySnapshot(payload.StateSnapshot)
		printSnapshot(payload.StateSnapshot)
	case *ruleshiftv1.ServerEnvelope_StateDelta:
		c.applyDelta(payload.StateDelta)
		printDelta(payload.StateDelta)
	case *ruleshiftv1.ServerEnvelope_Error:
		fmt.Printf("server error code=%s message=%q\n", payload.Error.GetCode(), payload.Error.GetMessage())
	case *ruleshiftv1.ServerEnvelope_Pong:
		now := time.Now().UnixMilli()
		fmt.Printf("pong server_time=%d rtt_ms=%d\n", payload.Pong.GetServerTimeUnixMs(), now-payload.Pong.GetClientTimeUnixMs())
	default:
		fmt.Printf("server envelope sequence=%d payload=%T\n", env.GetServerSequence(), env.GetPayload())
	}
}

func (c *consoleClient) applySnapshot(snapshot *ruleshiftv1.StateSnapshot) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.roomID = snapshot.GetRoomId()
	c.currentValue = snapshot.GetValue()
	c.lastRevision = snapshot.GetRevision()
}

func (c *consoleClient) applyDelta(delta *ruleshiftv1.StateDelta) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if delta.GetNewRevision() >= c.lastRevision {
		c.roomID = delta.GetRoomId()
		c.currentValue = delta.GetNewValue()
		c.lastRevision = delta.GetNewRevision()
	}
}

func (c *consoleClient) setStrictRevision(enabled bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.strictRevision = enabled
}

func (c *consoleClient) printStatus() {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	fmt.Printf(
		"status player=%s room=%s value=%d revision=%d strict_revision=%t\n",
		c.playerID,
		c.roomID,
		c.currentValue,
		c.lastRevision,
		c.strictRevision,
	)
}

func serverError(message *ruleshiftv1.ErrorMessage) error {
	return fmt.Errorf("server error code=%s message=%q", message.GetCode(), message.GetMessage())
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

func printHelp() {
	fmt.Println("commands:")
	fmt.Println("  get              request current room snapshot")
	fmt.Println("  add <value>      add value to the shared integer")
	fmt.Println("  set <value>      set the shared integer")
	fmt.Println("  room <room-id>   join or create another room")
	fmt.Println("  strict on|off    toggle expected revision checks")
	fmt.Println("  ping             send app-level ping")
	fmt.Println("  status           print local state")
	fmt.Println("  help             show commands")
	fmt.Println("  quit             exit")
}
