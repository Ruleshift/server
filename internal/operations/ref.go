package operations

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"regexp"
	"strings"
)

var publicRoomRefPattern = regexp.MustCompile(`^rm_[a-z2-7]{26}$`)

type RefCodec struct{ key []byte }

func NewRefCodec(key string) (RefCodec, error) {
	if len(key) < 32 {
		return RefCodec{}, fmt.Errorf("public room reference key must be at least 32 bytes")
	}
	return RefCodec{key: []byte(key)}, nil
}

func (c RefCodec) Room(roomID string) string {
	digest := hmac.New(sha256.New, c.key)
	_, _ = digest.Write([]byte(roomID))
	value := digest.Sum(nil)[:16]
	return "rm_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(value))
}

func ValidPublicRoomRef(value string) bool { return publicRoomRefPattern.MatchString(value) }
