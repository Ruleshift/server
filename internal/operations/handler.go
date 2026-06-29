package operations

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ruleshift/server/internal/roomcore"
)

const maxPageSize = 100

type RoomSource interface{ Diagnostics() []roomcore.Diagnostic }
type ConnectionSource interface{ ConnectionCount(roomID string) int }

type Config struct {
	QueueDegradedRatio float64
}

type Handler struct {
	rooms       RoomSource
	connections ConnectionSource
	refs        RefCodec
	cfg         Config
	mux         *http.ServeMux
}

type Room struct {
	PublicRoomRef  string    `json:"public_room_ref"`
	Status         string    `json:"status"`
	Revision       string    `json:"revision"`
	ModuleID       string    `json:"module_id"`
	ModuleVersion  string    `json:"module_version"`
	CreatedAt      time.Time `json:"created_at"`
	LastActivityAt time.Time `json:"last_activity_at"`
	Connections    int       `json:"connections"`
	QueueDepth     int       `json:"queue_depth"`
	QueueCapacity  int       `json:"queue_capacity"`
	QueueRatio     float64   `json:"queue_saturation_ratio"`
}

type RoomsPage struct {
	Items      []Room `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func NewHandler(rooms RoomSource, connections ConnectionSource, refs RefCodec, cfg Config) *Handler {
	if cfg.QueueDegradedRatio <= 0 || cfg.QueueDegradedRatio > 1 {
		cfg.QueueDegradedRatio = .8
	}
	h := &Handler{rooms: rooms, connections: connections, refs: refs, cfg: cfg, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /healthz", h.health)
	h.mux.HandleFunc("GET /internal/v1/rooms", h.listRooms)
	h.mux.HandleFunc("GET /internal/v1/rooms/{public_room_ref}", h.getRoom)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) listRooms(w http.ResponseWriter, r *http.Request) {
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxPageSize {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_limit"})
			return
		}
		limit = parsed
	}
	cursor := r.URL.Query().Get("cursor")
	statusFilter := r.URL.Query().Get("status")
	moduleFilter := r.URL.Query().Get("module")
	query := r.URL.Query().Get("q")
	if len(cursor) > 64 || len(statusFilter) > 32 || len(moduleFilter) > 64 || len(query) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_filter"})
		return
	}

	rooms := h.snapshot()
	filtered := rooms[:0]
	for _, room := range rooms {
		if room.PublicRoomRef <= cursor || statusFilter != "" && room.Status != statusFilter || moduleFilter != "" && room.ModuleID != moduleFilter || query != "" && !strings.HasPrefix(room.PublicRoomRef, query) {
			continue
		}
		filtered = append(filtered, room)
	}
	page := RoomsPage{Items: filtered}
	if len(page.Items) > limit {
		page.NextCursor = page.Items[limit-1].PublicRoomRef
		page.Items = page.Items[:limit]
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) getRoom(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("public_room_ref")
	if !ValidPublicRoomRef(ref) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "room_not_found"})
		return
	}
	for _, room := range h.snapshot() {
		if room.PublicRoomRef == ref {
			writeJSON(w, http.StatusOK, room)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"code": "room_not_found"})
}

func (h *Handler) snapshot() []Room {
	diagnostics := h.rooms.Diagnostics()
	values := make([]Room, 0, len(diagnostics))
	for _, value := range diagnostics {
		status := value.Status
		if value.QueueSaturation >= h.cfg.QueueDegradedRatio {
			status = "degraded"
		}
		connections := 0
		if h.connections != nil {
			connections = h.connections.ConnectionCount(value.RoomID)
		}
		values = append(values, Room{PublicRoomRef: h.refs.Room(value.RoomID), Status: status, Revision: strconv.FormatUint(value.Revision, 10), ModuleID: value.ModuleID, ModuleVersion: value.Version, CreatedAt: value.CreatedAt, LastActivityAt: value.UpdatedAt, Connections: connections, QueueDepth: value.QueueDepth, QueueCapacity: value.QueueCapacity, QueueRatio: value.QueueSaturation})
	}
	sort.Slice(values, func(i, j int) bool { return values[i].PublicRoomRef < values[j].PublicRoomRef })
	return values
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
