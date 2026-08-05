package gamejampromo

import (
	"errors"
	"testing"
	"time"
)

func TestValidateCandidateUpdateSupportsOnlineJamWithoutCity(t *testing.T) {
	value := CandidateUpdate{
		OfficialURL: "https://example.test/jam",
		Title:       "Russian Online Jam",
		Format:      FormatOnline,
		Languages:   []string{"ru"},
		StartsOn:    time.Date(2030, 4, 1, 0, 0, 0, 0, time.UTC),
		EndsOn:      time.Date(2030, 4, 2, 0, 0, 0, 0, time.UTC),
		Relevance:   RelevanceLikely,
	}
	if err := validateCandidateUpdate(value); err != nil {
		t.Fatalf("valid online jam: %v", err)
	}
	value.OfficialURL = "http://example.test/jam"
	if err := validateCandidateUpdate(value); !errors.Is(err, ErrValidation) {
		t.Fatalf("insecure official URL error = %v", err)
	}
}

func TestApprovalRequiresEligibilityReason(t *testing.T) {
	service := &Service{}
	if _, err := service.ApproveCandidate(t.Context(), "candidate", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("approval error = %v", err)
	}
}
