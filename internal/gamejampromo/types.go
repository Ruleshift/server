package gamejampromo

import (
	"context"
	"errors"
	"time"
)

type Format string

const (
	FormatOnline  Format = "online"
	FormatOffline Format = "offline"
	FormatHybrid  Format = "hybrid"
	FormatUnknown Format = "unknown"
)

type Relevance string

const (
	RelevanceLikely   Relevance = "likely_ru"
	RelevanceUnknown  Relevance = "unknown"
	RelevanceUnlikely Relevance = "unlikely_ru"
)

type EligibilityReason string

const (
	EligibilityVenueRU     EligibilityReason = "venue_ru"
	EligibilityLanguageRU  EligibilityReason = "language_ru"
	EligibilityAudienceRU  EligibilityReason = "audience_ru"
	EligibilityOrganizerRU EligibilityReason = "organizer_ru"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrValidation    = errors.New("validation error")
	ErrCodeCollision = errors.New("promotion code collision")
	ErrDiscoveryBusy = errors.New("discovery already running")
)

type DiscoveredJam struct {
	Source         string
	ExternalID     string
	SourceURL      string
	OfficialURL    string
	Title          string
	Organizer      string
	Format         Format
	City           string
	CountryCode    string
	Languages      []string
	StartsOn       time.Time
	EndsOn         time.Time
	Description    string
	Relevance      Relevance
	RelevanceNotes string
}

type Source interface {
	Name() string
	Discover(context.Context) ([]DiscoveredJam, error)
}

type Candidate struct {
	ID                          string    `json:"id"`
	Source                      string    `json:"source"`
	ExternalID                  string    `json:"external_id"`
	SourceURL                   string    `json:"source_url"`
	OfficialURL                 string    `json:"official_url,omitempty"`
	Title                       string    `json:"title"`
	Organizer                   string    `json:"organizer,omitempty"`
	Format                      Format    `json:"format"`
	City                        string    `json:"city,omitempty"`
	CountryCode                 string    `json:"country_code,omitempty"`
	Languages                   []string  `json:"languages"`
	StartsOn                    time.Time `json:"starts_on"`
	EndsOn                      time.Time `json:"ends_on"`
	Description                 string    `json:"description,omitempty"`
	Relevance                   Relevance `json:"relevance"`
	RelevanceNotes              string    `json:"relevance_notes,omitempty"`
	Status                      string    `json:"status"`
	SourceChanged               bool      `json:"source_changed"`
	LinkedGameJamID             string    `json:"linked_game_jam_id,omitempty"`
	ExactDuplicateGameJamID     string    `json:"exact_duplicate_game_jam_id,omitempty"`
	PossibleDuplicateGameJamIDs []string  `json:"possible_duplicate_game_jam_ids,omitempty"`
	FirstSeenAt                 time.Time `json:"first_seen_at"`
	LastSeenAt                  time.Time `json:"last_seen_at"`
}

type GameJam struct {
	ID                string            `json:"id"`
	Title             string            `json:"title"`
	Organizer         string            `json:"organizer,omitempty"`
	Format            Format            `json:"format"`
	City              string            `json:"city,omitempty"`
	CountryCode       string            `json:"country_code,omitempty"`
	Languages         []string          `json:"languages"`
	StartsOn          time.Time         `json:"starts_on"`
	EndsOn            time.Time         `json:"ends_on"`
	EligibilityReason EligibilityReason `json:"eligibility_reason"`
	Status            string            `json:"status"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	Code              string            `json:"code,omitempty"`
	CodeLastFour      string            `json:"code_last_four,omitempty"`
}

type CandidateFilter struct {
	Source    string
	Format    Format
	Relevance Relevance
	Status    string
	Limit     int
	Offset    int
}

type ProtectedCode struct {
	LookupHMAC []byte
	Ciphertext []byte
	Nonce      []byte
	LastFour   string
}

type DiscoveryRun struct {
	ID         int64      `json:"id"`
	Source     string     `json:"source"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Result     string     `json:"result"`
	FoundCount int        `json:"found_count"`
	Message    string     `json:"message,omitempty"`
}

func validFormat(value Format) bool {
	switch value {
	case FormatOnline, FormatOffline, FormatHybrid, FormatUnknown:
		return true
	default:
		return false
	}
}

func validRelevance(value Relevance) bool {
	switch value {
	case RelevanceLikely, RelevanceUnknown, RelevanceUnlikely:
		return true
	default:
		return false
	}
}

func validEligibility(value EligibilityReason) bool {
	switch value {
	case EligibilityVenueRU, EligibilityLanguageRU, EligibilityAudienceRU, EligibilityOrganizerRU:
		return true
	default:
		return false
	}
}
