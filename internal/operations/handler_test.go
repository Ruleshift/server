package operations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/roomcore"
)

type fakeRooms struct{ values []roomcore.Diagnostic }

func (f fakeRooms) Diagnostics() []roomcore.Diagnostic {
	return append([]roomcore.Diagnostic(nil), f.values...)
}

type fakeConnections int

func (f fakeConnections) ConnectionCount(string) int { return int(f) }

func TestRefCodecIsStableAndOpaque(t *testing.T) {
	codec, err := NewRefCodec(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	left := codec.Room("internal-room-id")
	if left != codec.Room("internal-room-id") || !ValidPublicRoomRef(left) {
		t.Fatalf("invalid stable ref %q", left)
	}
	if strings.Contains(left, "internal") || left == codec.Room("other-room") {
		t.Fatalf("reference is not opaque: %q", left)
	}
}

func TestHandlerNeverReturnsInternalRoomData(t *testing.T) {
	codec, _ := NewRefCodec(strings.Repeat("k", 32))
	now := time.Now().UTC()
	h := NewHandler(fakeRooms{values: []roomcore.Diagnostic{{RoomID: "secret-internal-id", ModuleID: "hiddennumber", Version: "1.0.0", Status: "active", Revision: 12, CreatedAt: now, UpdatedAt: now, QueueDepth: 8, QueueCapacity: 10, QueueSaturation: .8}}}, fakeConnections(3), codec, Config{})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/internal/v1/rooms", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if body := recorder.Body.String(); strings.Contains(body, "secret-internal-id") || strings.Contains(body, "player") || strings.Contains(body, "payload") {
		t.Fatalf("unsafe body: %s", body)
	}
	var page RoomsPage
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Status != "degraded" || page.Items[0].Revision != "12" {
		t.Fatalf("page = %+v", page)
	}
}

func TestHandlerRejectsUnboundedLimit(t *testing.T) {
	codec, _ := NewRefCodec(strings.Repeat("k", 32))
	h := NewHandler(fakeRooms{}, nil, codec, Config{})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/internal/v1/rooms?limit=1000", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
}
