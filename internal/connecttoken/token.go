package connecttoken

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const tokenIssuer = "ruleshift-connect"

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

type tokenClaims struct {
	MatchID  string `json:"match_id"`
	ServerID string `json:"server_id"`
	jwt.RegisteredClaims
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
	return &Manager{secret: slices.Clone(secret), clock: clock}, nil
}

func (m *Manager) Generate(claims Claims) (string, error) {
	if err := validateClaims(claims); err != nil {
		return "", err
	}
	if !claims.ExpiresAt.After(m.clock()) {
		return "", ErrExpiredToken
	}

	value := tokenClaims{
		MatchID:  claims.MatchID,
		ServerID: claims.ServerID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   claims.PlayerID,
			ExpiresAt: jwt.NewNumericDate(claims.ExpiresAt.UTC()),
			ID:        claims.AssignmentID,
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, value).SignedString(m.secret)
}

func (m *Manager) Validate(token string) (Claims, error) {
	var value tokenClaims
	_, err := jwt.ParseWithClaims(token, &value, func(*jwt.Token) (any, error) {
		return m.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(m.clock),
	)
	if errors.Is(err, jwt.ErrTokenExpired) {
		return Claims{}, ErrExpiredToken
	}
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims := Claims{
		AssignmentID: value.ID,
		MatchID:      value.MatchID,
		ServerID:     value.ServerID,
		PlayerID:     value.Subject,
		ExpiresAt:    value.ExpiresAt.Time.UTC(),
	}
	if err = validateClaims(claims); err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	return claims, nil
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
	}
	if claims.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at must not be zero")
	}
	return nil
}
