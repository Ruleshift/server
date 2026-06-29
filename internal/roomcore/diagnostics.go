package roomcore

import "time"

// Diagnostic is an internal, payload-free view of a room runtime. RoomID is
// intentionally present only inside the private process boundary; public APIs
// must replace it with a public_room_ref.
type Diagnostic struct {
	RoomID          string
	ModuleID        string
	Version         string
	Status          string
	Revision        uint64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	QueueDepth      int
	QueueCapacity   int
	QueueSaturation float64
}

func (r *Runtime) Diagnostic() Diagnostic {
	capacity := cap(r.input)
	depth := len(r.input)
	ratio := 0.0
	if capacity > 0 {
		ratio = float64(depth) / float64(capacity)
	}
	return Diagnostic{
		RoomID:          r.roomID,
		ModuleID:        r.moduleID,
		Version:         r.version,
		Status:          r.status,
		Revision:        r.revision.Load(),
		CreatedAt:       r.createdAt,
		UpdatedAt:       time.UnixMilli(r.updatedAt.Load()).UTC(),
		QueueDepth:      depth,
		QueueCapacity:   capacity,
		QueueSaturation: ratio,
	}
}
