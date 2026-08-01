package controlplane

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Ruleshift/server/internal/module"
)

type ConformanceDocument struct {
	Seed          uint64                  `json:"seed"`
	NowUnixMS     int64                   `json:"now_unix_ms"`
	PlayerCount   uint32                  `json:"player_count"`
	InitialDigest string                  `json:"initial_state_sha256"`
	Steps         []ConformanceStep       `json:"steps"`
	Projections   []ConformanceProjection `json:"projections"`
}
type ConformanceStep struct {
	Kind                string `json:"kind"`
	PlayerID            string `json:"player_id"`
	SeatIndex           uint32 `json:"seat_index"`
	TypeURL             string `json:"type_url,omitempty"`
	PayloadBase64       string `json:"payload_base64,omitempty"`
	ExpectedStateDigest string `json:"expected_state_sha256"`
	ExpectedDeltaDigest string `json:"expected_delta_sha256"`
}
type ConformanceProjection struct {
	PlayerID           string  `json:"player_id"`
	SeatIndex          *uint32 `json:"seat_index,omitempty"`
	Scope              string  `json:"scope"`
	ExpectedViewDigest string  `json:"expected_view_sha256"`
}

type DefaultConformanceRunner struct{}

func (DefaultConformanceRunner) Run(ctx context.Context, runtime module.Runtime, version Version, payload []byte) ([]byte, error) {
	var document ConformanceDocument
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, fmt.Errorf("decode vectors: %w", err)
	}
	if document.InitialDigest == "" || len(document.Steps) == 0 || len(document.Projections) < 2 {
		return nil, fmt.Errorf("vectors require player_count, initial digest, steps, and player/public projections")
	}
	if document.PlayerCount < version.Manifest.MinPlayers || document.PlayerCount > version.Manifest.MaxPlayers {
		return nil, fmt.Errorf("vector player_count must be between manifest min_players and max_players")
	}
	now := time.UnixMilli(document.NowUnixMS).UTC()
	revision := uint64(0)
	op := func() module.DeterministicContext {
		return module.DeterministicContext{Revision: revision, Now: now, Seed: document.Seed}
	}
	initial, err := runtime.CreateState(ctx, op(), module.GameSetup{PlayerCount: document.PlayerCount})
	if err != nil {
		return nil, err
	}
	if !initial.Changed {
		return nil, fmt.Errorf("CreateState must report changed")
	}
	if err = expectDigest("initial state", initial.NextState.Digest, document.InitialDigest); err != nil {
		return nil, err
	}
	state := initial.NextState
	var output bytes.Buffer
	writeOpaque(&output, state)
	hasCommand := false
	for index, step := range document.Steps {
		before := state
		var transition module.Transition
		switch step.Kind {
		case "command":
			hasCommand = true
			if step.PlayerID == "" || step.SeatIndex >= document.PlayerCount {
				return nil, fmt.Errorf("step %d actor must have player_id and a valid seat_index", index)
			}
			raw, decodeErr := base64.StdEncoding.DecodeString(step.PayloadBase64)
			if decodeErr != nil {
				return nil, decodeErr
			}
			command, opaqueErr := module.NewOpaque(step.TypeURL, raw, module.MaxMessageBytes)
			if opaqueErr != nil {
				return nil, opaqueErr
			}
			transition, err = runtime.Apply(ctx, op(), state, module.Actor{PlayerID: step.PlayerID, SeatIndex: step.SeatIndex}, command)
		default:
			return nil, fmt.Errorf("unknown conformance step kind %q", step.Kind)
		}
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", index, err)
		}
		if !transition.Changed {
			return nil, fmt.Errorf("step %d must change state", index)
		}
		if transition.NextState.TypeURL != version.Manifest.StateTypeURL {
			return nil, fmt.Errorf("step %d returned wrong state type", index)
		}
		if err = expectDigest("state", transition.NextState.Digest, step.ExpectedStateDigest); err != nil {
			return nil, fmt.Errorf("step %d: %w", index, err)
		}
		if err = expectDigest("delta", transition.Delta.Digest, step.ExpectedDeltaDigest); err != nil {
			return nil, fmt.Errorf("step %d: %w", index, err)
		}
		state = transition.NextState
		revision++
		deltaProjection, projectErr := runtime.ProjectDelta(ctx, op(), before, state, transition.Delta, module.Viewer{PlayerID: "public-probe", Scope: module.ViewScopePublic})
		if projectErr != nil {
			return nil, fmt.Errorf("step %d delta projection: %w", index, projectErr)
		}
		writeOpaque(&output, before)
		writeOpaque(&output, state)
		writeOpaque(&output, transition.Delta)
		writeOpaque(&output, deltaProjection.Payload)
	}
	if !hasCommand {
		return nil, fmt.Errorf("vectors require at least one command")
	}
	seenPrivate := false
	seenPublic := false
	for index, vector := range document.Projections {
		if vector.SeatIndex != nil && *vector.SeatIndex >= document.PlayerCount {
			return nil, fmt.Errorf("projection %d has invalid seat_index", index)
		}
		viewer, viewerErr := conformanceViewer(vector)
		if viewerErr != nil {
			return nil, viewerErr
		}
		if viewer.Scope == module.ViewScopePlayer {
			seenPrivate = true
		}
		if viewer.Scope == module.ViewScopePublic {
			seenPublic = true
		}
		projection, projectErr := runtime.ProjectSnapshot(ctx, op(), state, viewer)
		if projectErr != nil {
			return nil, projectErr
		}
		if err = expectDigest("view", projection.Payload.Digest, vector.ExpectedViewDigest); err != nil {
			return nil, fmt.Errorf("projection %d: %w", index, err)
		}
		writeOpaque(&output, projection.Payload)
	}
	if !seenPrivate || !seenPublic {
		return nil, fmt.Errorf("vectors require player-private and public projections")
	}
	return output.Bytes(), nil
}

func conformanceViewer(value ConformanceProjection) (module.Viewer, error) {
	viewer := module.Viewer{PlayerID: value.PlayerID, Scope: module.ViewScopePlayer}
	if value.SeatIndex != nil {
		viewer.Seated = true
		viewer.SeatIndex = *value.SeatIndex
	}
	switch value.Scope {
	case "player":
		viewer.Scope = module.ViewScopePlayer
	case "public":
		viewer.Scope = module.ViewScopePublic
	case "full":
		viewer.Scope = module.ViewScopeFull
	default:
		return module.Viewer{}, fmt.Errorf("unknown projection scope %q", value.Scope)
	}
	if viewer.Scope == module.ViewScopePlayer && !viewer.Seated {
		return module.Viewer{}, fmt.Errorf("player projection requires seat_index")
	}
	return viewer, nil
}
func expectDigest(name string, actual [32]byte, expected string) error {
	if expected == "" {
		return fmt.Errorf("expected %s digest is required", name)
	}
	decoded, err := hex.DecodeString(expected)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("invalid expected %s digest", name)
	}
	if !bytes.Equal(actual[:], decoded) {
		return fmt.Errorf("%s digest mismatch: got %x", name, actual)
	}
	return nil
}
func writeOpaque(buffer *bytes.Buffer, value module.OpaqueState) {
	_ = binary.Write(buffer, binary.BigEndian, uint32(len(value.TypeURL)))
	buffer.WriteString(value.TypeURL)
	_ = binary.Write(buffer, binary.BigEndian, uint32(len(value.Payload)))
	buffer.Write(value.Payload)
}
