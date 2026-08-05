package gamejampromo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var codePattern = regexp.MustCompile(`^[0-9]{10}$`)

type Service struct {
	store   *Store
	codes   *CodeManager
	metrics *Metrics
	zone    *time.Location
}

func NewService(store *Store, codes *CodeManager, metrics *Metrics) (*Service, error) {
	zone, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return nil, fmt.Errorf("load Moscow timezone: %w", err)
	}
	return &Service{store: store, codes: codes, metrics: metrics, zone: zone}, nil
}

func (s *Service) VerifyCode(ctx context.Context, code string, now time.Time) (GameJam, bool, error) {
	code = strings.TrimSpace(code)
	if !codePattern.MatchString(code) {
		return GameJam{}, false, fmt.Errorf("%w: code must contain exactly 10 digits", ErrValidation)
	}
	local := now.In(s.zone)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.zone)
	value, err := s.store.FindActiveByCode(ctx, s.codes.LookupHMAC(code), date)
	if errors.Is(err, ErrNotFound) {
		if s.metrics != nil {
			s.metrics.ObserveVerification("invalid")
		}
		return GameJam{}, false, nil
	}
	if err != nil {
		if s.metrics != nil {
			s.metrics.ObserveVerification("error")
		}
		return GameJam{}, false, err
	}
	if s.metrics != nil {
		s.metrics.ObserveVerification("valid")
	}
	return value, true, nil
}

func (s *Service) ListCandidates(ctx context.Context, filter CandidateFilter) ([]Candidate, error) {
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 || filter.Offset > 10_000 {
		return nil, fmt.Errorf("%w: invalid pagination", ErrValidation)
	}
	if filter.Format != "" && !validFormat(filter.Format) {
		return nil, fmt.Errorf("%w: invalid format", ErrValidation)
	}
	if filter.Relevance != "" && !validRelevance(filter.Relevance) {
		return nil, fmt.Errorf("%w: invalid relevance", ErrValidation)
	}
	if filter.Status != "" && filter.Status != "pending" && filter.Status != "approved" && filter.Status != "rejected" && filter.Status != "archived" {
		return nil, fmt.Errorf("%w: invalid status", ErrValidation)
	}
	return s.store.ListCandidates(ctx, filter)
}

func (s *Service) UpdateCandidate(ctx context.Context, id string, value CandidateUpdate) error {
	value.OfficialURL = strings.TrimSpace(value.OfficialURL)
	value.Title = strings.TrimSpace(value.Title)
	value.Organizer = strings.TrimSpace(value.Organizer)
	value.City = strings.TrimSpace(value.City)
	value.CountryCode = strings.TrimSpace(value.CountryCode)
	if err := validateCandidateUpdate(value); err != nil {
		return err
	}
	return s.store.UpdateCandidate(ctx, id, value)
}

func validateCandidateUpdate(value CandidateUpdate) error {
	value.Title = strings.TrimSpace(value.Title)
	if value.Title == "" || len(value.Title) > 300 {
		return fmt.Errorf("%w: title must contain 1-300 bytes", ErrValidation)
	}
	if !validFormat(value.Format) || !validRelevance(value.Relevance) {
		return fmt.Errorf("%w: invalid format or relevance", ErrValidation)
	}
	if value.StartsOn.IsZero() || value.EndsOn.Before(value.StartsOn) {
		return fmt.Errorf("%w: invalid date range", ErrValidation)
	}
	if len(value.City) > 200 || len(value.Organizer) > 300 || len(value.CountryCode) > 2 || len(value.Languages) > 16 {
		return fmt.Errorf("%w: candidate field limit exceeded", ErrValidation)
	}
	if len(value.OfficialURL) > 2048 || !validOptionalHTTPSURL(value.OfficialURL) {
		return fmt.Errorf("%w: official_url must be an HTTPS URL", ErrValidation)
	}
	return nil
}

func validOptionalHTTPSURL(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil
}

func (s *Service) RejectCandidate(ctx context.Context, id, reason string) error {
	if strings.TrimSpace(reason) == "" || len(reason) > 512 {
		return fmt.Errorf("%w: rejection reason is required and must not exceed 512 bytes", ErrValidation)
	}
	return s.store.RejectCandidate(ctx, id, reason)
}

func (s *Service) ApproveCandidate(ctx context.Context, id string, reason EligibilityReason) (GameJam, error) {
	if !validEligibility(reason) {
		return GameJam{}, fmt.Errorf("%w: eligibility reason is required", ErrValidation)
	}
	for range 8 {
		code, err := GenerateCode()
		if err != nil {
			return GameJam{}, err
		}
		protected, err := s.codes.Protect(code)
		if err != nil {
			return GameJam{}, err
		}
		jam, err := s.store.ApproveCandidate(ctx, id, reason, protected)
		if errors.Is(err, ErrCodeCollision) {
			continue
		}
		if err != nil {
			return GameJam{}, err
		}
		jam.Code = code
		return jam, nil
	}
	return GameJam{}, ErrCodeCollision
}

func (s *Service) MergeCandidate(ctx context.Context, candidateID, gameJamID string) error {
	if candidateID == "" || gameJamID == "" {
		return fmt.Errorf("%w: candidate_id and game_jam_id are required", ErrValidation)
	}
	return s.store.MergeCandidate(ctx, candidateID, gameJamID)
}

func (s *Service) ReviewCandidateUpdate(ctx context.Context, candidateID string, apply bool) error {
	if strings.TrimSpace(candidateID) == "" {
		return fmt.Errorf("%w: candidate id is required", ErrValidation)
	}
	return s.store.ReviewCandidateUpdate(ctx, candidateID, apply)
}

func (s *Service) ListGameJams(ctx context.Context, limit, offset int) ([]GameJam, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 10_000 {
		return nil, fmt.Errorf("%w: invalid pagination", ErrValidation)
	}
	jams, codes, err := s.store.ListGameJams(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	for index := range jams {
		if len(codes[index].Ciphertext) == 0 {
			continue
		}
		code, err := s.codes.Reveal(codes[index])
		if err != nil {
			return nil, err
		}
		jams[index].Code = code
	}
	return jams, nil
}

func (s *Service) UpdateGameJam(ctx context.Context, id string, value GameJamUpdate) error {
	value.Title = strings.TrimSpace(value.Title)
	value.Organizer = strings.TrimSpace(value.Organizer)
	value.City = strings.TrimSpace(value.City)
	value.CountryCode = strings.TrimSpace(value.CountryCode)
	if strings.TrimSpace(value.Title) == "" || len(value.Title) > 300 || !validFormat(value.Format) || value.StartsOn.IsZero() || value.EndsOn.Before(value.StartsOn) {
		return fmt.Errorf("%w: invalid game jam", ErrValidation)
	}
	if value.Status != "approved" && value.Status != "disabled" && value.Status != "ended" {
		return fmt.Errorf("%w: invalid game jam status", ErrValidation)
	}
	if len(value.City) > 200 || len(value.Organizer) > 300 || len(value.CountryCode) > 2 || len(value.Languages) > 16 {
		return fmt.Errorf("%w: game jam field limit exceeded", ErrValidation)
	}
	return s.store.UpdateGameJam(ctx, id, value)
}

func (s *Service) RotateCode(ctx context.Context, id string) (string, error) {
	for range 8 {
		code, err := GenerateCode()
		if err != nil {
			return "", err
		}
		protected, err := s.codes.Protect(code)
		if err != nil {
			return "", err
		}
		err = s.store.RotateCode(ctx, id, protected)
		if errors.Is(err, ErrCodeCollision) {
			continue
		}
		return code, err
	}
	return "", ErrCodeCollision
}

func (s *Service) ListRuns(ctx context.Context) ([]DiscoveryRun, error) {
	return s.store.ListRuns(ctx, 50)
}
