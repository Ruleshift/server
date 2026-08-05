package gamejampromo

import (
	"crypto/rand"
	"encoding/base64"
	"regexp"
	"testing"
)

func testCodeManager(t *testing.T) *CodeManager {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	manager, err := NewCodeManager(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestCodeManagerRoundTrip(t *testing.T) {
	manager := testCodeManager(t)
	protected, err := manager.Protect("0000123456")
	if err != nil {
		t.Fatal(err)
	}
	if protected.LastFour != "3456" || len(protected.LookupHMAC) != 32 {
		t.Fatalf("protected code = %+v", protected)
	}
	revealed, err := manager.Reveal(protected)
	if err != nil {
		t.Fatal(err)
	}
	if revealed != "0000123456" {
		t.Fatalf("revealed = %q", revealed)
	}
	if string(manager.LookupHMAC("0000123456")) == string(manager.LookupHMAC("0000123457")) {
		t.Fatal("distinct codes produced the same lookup HMAC")
	}
}

func TestCodeManagerStableKeyAndWrongKey(t *testing.T) {
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(master)
	first, _ := NewCodeManager(encoded)
	protected, err := first.Protect("0123456789")
	if err != nil {
		t.Fatal(err)
	}
	restarted, _ := NewCodeManager(encoded)
	if code, err := restarted.Reveal(protected); err != nil || code != "0123456789" {
		t.Fatalf("reveal after restart = %q, %v", code, err)
	}
	wrongMaster := make([]byte, 32)
	if _, err := rand.Read(wrongMaster); err != nil {
		t.Fatal(err)
	}
	wrong, _ := NewCodeManager(base64.StdEncoding.EncodeToString(wrongMaster))
	if _, err := wrong.Reveal(protected); err == nil {
		t.Fatal("wrong key decrypted the promotion code")
	}
	if _, err := first.Protect("123"); err == nil {
		t.Fatal("invalid code format was accepted")
	}
}

func TestGenerateCodeHasTenDigits(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9]{10}$`)
	for range 100 {
		value, err := GenerateCode()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(value) {
			t.Fatalf("code = %q", value)
		}
	}
}

func TestCodeManagerRejectsInvalidKey(t *testing.T) {
	if _, err := NewCodeManager(base64.StdEncoding.EncodeToString(make([]byte, 31))); err == nil {
		t.Fatal("expected invalid key error")
	}
}
