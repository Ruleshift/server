package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Ruleshift/server/internal/protocol"
	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
	"github.com/gorilla/websocket"
)

type options struct {
	addr, ticket, room string
	spectator          bool
	setSecret          int64
	watch              bool
}

func main() {
	var opts options
	flag.StringVar(&opts.addr, "addr", "ws://127.0.0.1:8080/ws", "demo websocket address")
	flag.StringVar(&opts.ticket, "ticket", "mock:player-1", "mock ticket; use mock:trusted:<id> for full spectator view")
	flag.StringVar(&opts.room, "room", "hidden-demo", "room id")
	flag.BoolVar(&opts.spectator, "spectator", false, "join without taking a player seat")
	flag.Int64Var(&opts.setSecret, "set-secret", -1, "set a secret in range 0..999999")
	flag.BoolVar(&opts.watch, "watch", false, "keep printing room updates")
	flag.Parse()
	if err := run(context.Background(), opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts options) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, opts.addr, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	sequence := uint64(1)
	if err := send(conn, sequence, &ruleshiftv1.ClientEnvelope{Payload: &ruleshiftv1.ClientEnvelope_AuthRequest{AuthRequest: &ruleshiftv1.AuthRequest{Ticket: opts.ticket}}}); err != nil {
		return err
	}
	sequence++
	if env, err := read(conn); err != nil || env.GetAuthOk() == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("authentication failed: %v", env.GetAuthFailed())
	}
	joinMode := ruleshiftv1.JoinMode_JOIN_MODE_PLAYER
	if opts.spectator {
		joinMode = ruleshiftv1.JoinMode_JOIN_MODE_SPECTATOR
	}
	if err := send(conn, sequence, &ruleshiftv1.ClientEnvelope{Payload: &ruleshiftv1.ClientEnvelope_JoinRoom{JoinRoom: &ruleshiftv1.JoinRoomRequest{RoomId: opts.room, JoinMode: joinMode}}}); err != nil {
		return err
	}
	sequence++
	join, err := read(conn)
	if err != nil {
		return err
	}
	if join.GetJoinRoomOk() == nil {
		return fmt.Errorf("join failed: %v", join.GetError())
	}
	printEnvelope(join)
	snapshot, err := read(conn)
	if err != nil {
		return err
	}
	printEnvelope(snapshot)
	if opts.setSecret >= 0 {
		if err := send(conn, sequence, &ruleshiftv1.ClientEnvelope{Payload: &ruleshiftv1.ClientEnvelope_GameCommand{GameCommand: &ruleshiftv1.GameCommand{
			RoomId: opts.room, Command: &ruleshiftv1.GameCommand_SetSecret{SetSecret: &ruleshiftv1.SetSecret{Value: opts.setSecret}},
		}}}); err != nil {
			return err
		}
		sequence++
		update, err := read(conn)
		if err != nil {
			return err
		}
		printEnvelope(update)
	}
	if !opts.watch {
		return nil
	}
	for {
		if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			return err
		}
		env, err := read(conn)
		if err != nil {
			return err
		}
		printEnvelope(env)
	}
}

func send(conn *websocket.Conn, sequence uint64, envelope *ruleshiftv1.ClientEnvelope) error {
	envelope.ProtocolVersion = protocol.CurrentVersion
	envelope.ClientSequence = sequence
	data, err := protocol.EncodeClientEnvelope(envelope)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

func read(conn *websocket.Conn) (*ruleshiftv1.ServerEnvelope, error) {
	messageType, data, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if messageType != websocket.BinaryMessage {
		return nil, fmt.Errorf("unexpected websocket message type %d", messageType)
	}
	return protocol.DecodeServerEnvelope(data)
}

func printEnvelope(env *ruleshiftv1.ServerEnvelope) {
	if join := env.GetJoinRoomOk(); join != nil {
		fmt.Printf("joined room=%s revision=%d mode=%s scope=%s\n", join.GetRoomId(), join.GetCurrentRevision(), join.GetJoinMode(), join.GetViewScope())
		return
	}
	if snapshot := env.GetStateSnapshot(); snapshot != nil {
		fmt.Printf("snapshot revision=%d view_hash=%d %s\n", snapshot.GetRevision(), snapshot.GetViewHash(), snapshot.GetHiddenNumber())
		return
	}
	if delta := env.GetStateDelta(); delta != nil {
		fmt.Printf("delta revision=%d->%d view_hash=%d hidden=%t %s\n", delta.GetPreviousRevision(), delta.GetNewRevision(), delta.GetViewHash(), delta.GetNoVisibleChange(), delta.GetHiddenNumber())
		return
	}
	if failure := env.GetError(); failure != nil {
		fmt.Printf("error code=%s message=%s\n", failure.GetCode(), failure.GetMessage())
	}
}
