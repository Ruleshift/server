package connecttoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const tokenVersion = "rst1"

var (
	ErrInvalidToken = errors.New("invalid connect token")
	ErrExpiredToken = errors.New("connect token expired")
)

type Claims struct {
	AssignmentID string
	MatchID      string
	ServerID     string
	PlayerID     string
	ExpiresAt    time.Time
}

type Manager struct {
	secret []byte
	clock  func() time.Time
}

func NewManager(secret []byte) (*Manager, error) {
	return NewManagerWithClock(secret, func() time.Time {
		return time.Now().UTC()
	})
}

func NewManagerWithClock(secret []byte, clock func() time.Time) (*Manager, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("connect token secret must be at least 32 bytes")
	}
	if clock == nil {
		return nil, fmt.Errorf("connect token clock must not be nil")
	}

	copied := make([]byte, len(secret))
	copy(copied, secret)
	return &Manager{secret: copied, clock: clock}, nil
}

func (m *Manager) Generate(claims Claims) (string, error) {
	if err := validateClaims(claims); err != nil {
		return "", err
	}
	if !claims.ExpiresAt.After(m.clock()) {
		return "", ErrExpiredToken
	}

	payload := encodePayload(claims)
	signature := m.sign([]byte(payload))
	return strings.Join([]string{
		tokenVersion,
		base64.RawURLEncoding.EncodeToString([]byte(payload)),
		base64.RawURLEncoding.EncodeToString(signature),
	}, "."), nil
}

func (m *Manager) Validate(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenVersion {
		return Claims{}, ErrInvalidToken
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: decode payload", ErrInvalidToken)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: decode signature", ErrInvalidToken)
	}
	if !hmac.Equal(signature, m.sign(payload)) {
		return Claims{}, fmt.Errorf("%w: signature mismatch", ErrInvalidToken)
	}

	claims, err := decodePayload(string(payload))
	if err != nil {
		return Claims{}, err
	}
	if !claims.ExpiresAt.After(m.clock()) {
		return Claims{}, ErrExpiredToken
	}
	return claims, nil
}

func (m *Manager) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func validateClaims(claims Claims) error {
	fields := []struct {
		name  string
		value string
	}{
		{name: "assignment_id", value: claims.AssignmentID},
		{name: "match_id", value: claims.MatchID},
		{name: "server_id", value: claims.ServerID},
		{name: "player_id", value: claims.PlayerID},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s must not be empty", field.name)
		}
		if strings.Contains(field.value, "\n") {
			return fmt.Errorf("%s must not contain newline", field.name)
		}
	}
	if claims.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at must not be zero")
	}
	return nil
}

func encodePayload(claims Claims) string {
	return strings.Join([]string{
		claims.AssignmentID,
		claims.MatchID,
		claims.ServerID,
		claims.PlayerID,
		strconv.FormatInt(claims.ExpiresAt.Unix(), 10),
	}, "\n")
}

func decodePayload(payload string) (Claims, error) {
	fields := strings.Split(payload, "\n")
	if len(fields) != 5 {
		return Claims{}, fmt.Errorf("%w: payload field count", ErrInvalidToken)
	}
	expiresUnix, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: parse expiry", ErrInvalidToken)
	}

	claims := Claims{
		AssignmentID: fields[0],
		MatchID:      fields[1],
		ServerID:     fields[2],
		PlayerID:     fields[3],
		ExpiresAt:    time.Unix(expiresUnix, 0).UTC(),
	}
	if err := validateClaims(claims); err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	return claims, nil
}
