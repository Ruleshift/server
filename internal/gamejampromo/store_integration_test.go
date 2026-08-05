//go:build integration

package gamejampromo

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresApprovalVerificationAndRotation(t *testing.T) {
	url := os.Getenv("GAMEJAM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("GAMEJAM_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE promotion_codes,gamejam_candidate_links,game_jams,gamejam_candidates,discovery_runs RESTART IDENTITY`); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	encodedKey := base64.StdEncoding.EncodeToString(key)
	manager, _ := NewCodeManager(encodedKey)
	service, _ := NewService(store, manager, NewMetrics())
	start := time.Date(2030, 1, 10, 0, 0, 0, 0, time.UTC)
	firstCandidate := DiscoveredJam{Source: "fixture", ExternalID: "one", SourceURL: "https://example.test/jam", OfficialURL: "https://official.example/jam", Title: "RU Jam", Organizer: "Ruleshift", Format: FormatOnline, Languages: []string{"ru"}, StartsOn: start, EndsOn: start.AddDate(0, 0, 2), Relevance: RelevanceLikely}
	if err := store.UpsertCandidate(ctx, firstCandidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCandidate(ctx, firstCandidate, time.Now()); err != nil {
		t.Fatalf("idempotent upsert: %v", err)
	}
	candidates, err := service.ListCandidates(ctx, CandidateFilter{Status: "pending", Limit: 10})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates = %+v, err = %v", candidates, err)
	}
	jam, err := service.ApproveCandidate(ctx, candidates[0].ID, EligibilityLanguageRU)
	if err != nil || len(jam.Code) != 10 {
		t.Fatalf("jam = %+v, err = %v", jam, err)
	}
	moscow, _ := time.LoadLocation("Europe/Moscow")
	if _, valid, err := service.VerifyCode(ctx, jam.Code, time.Date(2030, 1, 9, 23, 59, 0, 0, moscow)); err != nil || valid {
		t.Fatalf("code before jam valid = %v, err = %v", valid, err)
	}
	verified, valid, err := service.VerifyCode(ctx, jam.Code, time.Date(2030, 1, 10, 0, 0, 0, 0, moscow))
	if err != nil || !valid || verified.ID != jam.ID {
		t.Fatalf("verification = %+v, %v, %v", verified, valid, err)
	}
	if _, valid, err := service.VerifyCode(ctx, jam.Code, time.Date(2030, 1, 12, 23, 59, 0, 0, moscow)); err != nil || !valid {
		t.Fatalf("code on final day valid = %v, err = %v", valid, err)
	}
	if _, valid, err := service.VerifyCode(ctx, jam.Code, time.Date(2030, 1, 13, 0, 0, 0, 0, moscow)); err != nil || valid {
		t.Fatalf("code after jam valid = %v, err = %v", valid, err)
	}

	restartedManager, _ := NewCodeManager(encodedKey)
	restartedService, _ := NewService(store, restartedManager, NewMetrics())
	jams, err := restartedService.ListGameJams(ctx, 10, 0)
	if err != nil || len(jams) != 1 || jams[0].Code != jam.Code {
		t.Fatalf("code after restart = %+v, err = %v", jams, err)
	}
	wrongKey := make([]byte, 32)
	_, _ = rand.Read(wrongKey)
	wrongManager, _ := NewCodeManager(base64.StdEncoding.EncodeToString(wrongKey))
	wrongService, _ := NewService(store, wrongManager, NewMetrics())
	if _, err := wrongService.ListGameJams(ctx, 10, 0); err == nil {
		t.Fatal("wrong master key decrypted a promotion code")
	}

	secondCandidate := firstCandidate
	secondCandidate.Source, secondCandidate.ExternalID, secondCandidate.SourceURL = "fixture_two", "two", "https://another.example/jam"
	if err := store.UpsertCandidate(ctx, secondCandidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	duplicates, err := service.ListCandidates(ctx, CandidateFilter{Source: "fixture_two", Status: "pending", Limit: 10})
	if err != nil || len(duplicates) != 1 || duplicates[0].ExactDuplicateGameJamID != jam.ID || len(duplicates[0].PossibleDuplicateGameJamIDs) == 0 {
		t.Fatalf("duplicate suggestions = %+v, err = %v", duplicates, err)
	}
	if _, err := service.ApproveCandidate(ctx, duplicates[0].ID, EligibilityLanguageRU); !errors.Is(err, ErrConflict) {
		t.Fatalf("approve exact duplicate error = %v", err)
	}
	if err := service.MergeCandidate(ctx, duplicates[0].ID, jam.ID); err != nil {
		t.Fatalf("merge exact duplicate: %v", err)
	}
	var linkCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM gamejam_candidate_links WHERE game_jam_id=$1`, jam.ID).Scan(&linkCount); err != nil || linkCount != 2 {
		t.Fatalf("link count = %d, err = %v", linkCount, err)
	}

	sourceUpdate := firstCandidate
	sourceUpdate.Title = "RU Jam Changed Upstream"
	if err := store.UpsertCandidate(ctx, sourceUpdate, time.Now()); err != nil {
		t.Fatal(err)
	}
	changedCandidate, err := store.GetCandidate(ctx, candidates[0].ID)
	if err != nil || !changedCandidate.SourceChanged {
		t.Fatalf("source change = %+v, err = %v", changedCandidate, err)
	}
	if err := service.ReviewCandidateUpdate(ctx, changedCandidate.ID, false); err != nil {
		t.Fatalf("keep approved snapshot: %v", err)
	}
	unchangedJam, err := store.GetGameJam(ctx, jam.ID)
	if err != nil || unchangedJam.Title != "RU Jam" {
		t.Fatalf("kept snapshot = %+v, err = %v", unchangedJam, err)
	}
	sourceUpdate.Title = "RU Jam Applied Update"
	if err := store.UpsertCandidate(ctx, sourceUpdate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := service.ReviewCandidateUpdate(ctx, changedCandidate.ID, true); err != nil {
		t.Fatalf("apply source update: %v", err)
	}
	updatedJam, err := store.GetGameJam(ctx, jam.ID)
	if err != nil || updatedJam.Title != "RU Jam Applied Update" {
		t.Fatalf("applied snapshot = %+v, err = %v", updatedJam, err)
	}

	concurrentCandidate := firstCandidate
	concurrentCandidate.ExternalID, concurrentCandidate.SourceURL, concurrentCandidate.OfficialURL = "three", "https://example.test/three", ""
	concurrentCandidate.Title, concurrentCandidate.StartsOn, concurrentCandidate.EndsOn = "Concurrent Jam", start.AddDate(0, 1, 0), start.AddDate(0, 1, 1)
	if err := store.UpsertCandidate(ctx, concurrentCandidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	concurrentCandidates, err := service.ListCandidates(ctx, CandidateFilter{Source: "fixture", Status: "pending", Limit: 10})
	if err != nil || len(concurrentCandidates) != 1 {
		t.Fatalf("concurrent candidate = %+v, err = %v", concurrentCandidates, err)
	}
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := service.ApproveCandidate(ctx, concurrentCandidates[0].ID, EligibilityLanguageRU)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent approval error = %v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent approvals: successes=%d conflicts=%d", successes, conflicts)
	}
	newCode, err := service.RotateCode(ctx, jam.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, valid, _ := service.VerifyCode(ctx, jam.Code, time.Date(2030, 1, 11, 12, 0, 0, 0, moscow)); valid {
		t.Fatal("revoked code remains valid")
	}
	if _, valid, err := service.VerifyCode(ctx, newCode, time.Date(2030, 1, 11, 12, 0, 0, 0, moscow)); err != nil || !valid {
		t.Fatalf("new code valid = %v, err = %v", valid, err)
	}
}
