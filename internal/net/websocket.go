package netx

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const WebSocketPath = "/ws"

const (
	OpcodeContinuation byte = 0x0
	OpcodeText         byte = 0x1
	OpcodeBinary       byte = 0x2
	OpcodeClose        byte = 0x8
	OpcodePing         byte = 0x9
	OpcodePong         byte = 0xa
)

const websocketAcceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

var (
	ErrWebSocketUpgradeRequired  = errors.New("websocket upgrade required")
	ErrWebSocketFrameTooLarge    = errors.New("websocket frame too large")
	ErrWebSocketClosed           = errors.New("websocket closed")
	ErrUnsupportedWebSocketFrame = errors.New("unsupported websocket frame")
)

type WebSocketGatewayConfig struct {
	Path            string
	MaxMessageBytes int
}

func DefaultWebSocketGatewayConfig(maxMessageBytes int) WebSocketGatewayConfig {
	return WebSocketGatewayConfig{
		Path:            WebSocketPath,
		MaxMessageBytes: maxMessageBytes,
	}
}

type WebSocketConn struct {
	conn       net.Conn
	reader     *bufio.Reader
	writeMu    sync.Mutex
	maxPayload int
}

func AcceptWebSocket(w http.ResponseWriter, r *http.Request, maxPayload int) (*WebSocketConn, error) {
	if maxPayload <= 0 {
		return nil, fmt.Errorf("max websocket payload must be positive")
	}
	if strings.ToLower(r.Header.Get("Upgrade")) != "websocket" || !headerContains(r.Header.Get("Connection"), "upgrade") {
		return nil, ErrWebSocketUpgradeRequired
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, fmt.Errorf("%w: unsupported websocket version", ErrWebSocketUpgradeRequired)
	}

	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, fmt.Errorf("%w: missing Sec-WebSocket-Key", ErrWebSocketUpgradeRequired)
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("http response writer does not support hijacking")
	}

	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, fmt.Errorf("hijack websocket connection: %w", err)
	}

	accept := websocketAccept(key)
	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"

	if _, err := rw.WriteString(response); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write websocket handshake: %w", err)
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("flush websocket handshake: %w", err)
	}

	return &WebSocketConn{
		conn:       conn,
		reader:     rw.Reader,
		maxPayload: maxPayload,
	}, nil
}

func (c *WebSocketConn) ReadMessage() (byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.reader, header); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
			return 0, nil, ErrWebSocketClosed
		}
		return 0, nil, fmt.Errorf("read websocket frame header: %w", err)
	}

	fin := header[0]&0x80 != 0
	opcode := header[0] & 0x0f
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7f)

	if !fin {
		return 0, nil, fmt.Errorf("%w: fragmented frames are not supported", ErrUnsupportedWebSocketFrame)
	}
	if !masked {
		return 0, nil, fmt.Errorf("%w: client frames must be masked", ErrUnsupportedWebSocketFrame)
	}

	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(c.reader, extended); err != nil {
			return 0, nil, fmt.Errorf("read websocket extended length: %w", err)
		}
		length = uint64(binary.BigEndian.Uint16(extended))
	case 127:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(c.reader, extended); err != nil {
			return 0, nil, fmt.Errorf("read websocket extended length: %w", err)
		}
		length = binary.BigEndian.Uint64(extended)
	}

	if length > uint64(c.maxPayload) {
		return 0, nil, fmt.Errorf("%w: got=%d max=%d", ErrWebSocketFrameTooLarge, length, c.maxPayload)
	}

	maskKey := make([]byte, 4)
	if _, err := io.ReadFull(c.reader, maskKey); err != nil {
		return 0, nil, fmt.Errorf("read websocket mask key: %w", err)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return 0, nil, fmt.Errorf("read websocket payload: %w", err)
	}
	for i := range payload {
		payload[i] ^= maskKey[i%4]
	}

	if opcode == OpcodeClose {
		return opcode, payload, ErrWebSocketClosed
	}
	return opcode, payload, nil
}

func (c *WebSocketConn) WriteBinary(payload []byte) error {
	return c.writeFrame(OpcodeBinary, payload)
}

func (c *WebSocketConn) WritePong(payload []byte) error {
	return c.writeFrame(OpcodePong, payload)
}

func (c *WebSocketConn) WriteClose() error {
	return c.writeFrame(OpcodeClose, nil)
}

func (c *WebSocketConn) Close() error {
	return c.conn.Close()
}

func (c *WebSocketConn) SetReadDeadline(deadline time.Time) error {
	return c.conn.SetReadDeadline(deadline)
}

func (c *WebSocketConn) writeFrame(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if len(payload) > c.maxPayload {
		return fmt.Errorf("%w: got=%d max=%d", ErrWebSocketFrameTooLarge, len(payload), c.maxPayload)
	}

	header := []byte{0x80 | opcode}
	switch {
	case len(payload) <= 125:
		header = append(header, byte(len(payload)))
	case len(payload) <= 65535:
		header = append(header, 126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(len(payload)))
	default:
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(len(payload)))
	}

	if _, err := c.conn.Write(header); err != nil {
		return fmt.Errorf("write websocket frame header: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}
	if _, err := c.conn.Write(payload); err != nil {
		return fmt.Errorf("write websocket frame payload: %w", err)
	}
	return nil
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + websocketAcceptGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerContains(header string, value string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), value) {
			return true
		}
	}
	return false
}
