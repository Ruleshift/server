package gamejampromo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func newID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func discoveryDigest(value DiscoveredJam) string {
	encoded, _ := json.Marshal([]any{value.SourceURL, value.OfficialURL, value.Title, value.Organizer, value.Format, value.City, value.CountryCode, value.Languages, value.StartsOn.Format(time.DateOnly), value.EndsOn.Format(time.DateOnly), value.Description, value.Relevance, value.RelevanceNotes})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (s *Store) WithDiscoveryLock(ctx context.Context, fn func(context.Context) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire discovery connection: %w", err)
	}
	defer conn.Close()
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext('ruleshift_gamejam_discovery'))`).Scan(&locked); err != nil {
		return fmt.Errorf("acquire discovery lock: %w", err)
	}
	if !locked {
		return ErrDiscoveryBusy
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext('ruleshift_gamejam_discovery'))`)
	return fn(ctx)
}

func (s *Store) StartRun(ctx context.Context, source string, startedAt time.Time) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO discovery_runs(source,started_at,result) VALUES($1,$2,'running') RETURNING id`, source, startedAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("start discovery run: %w", err)
	}
	return id, nil
}

func (s *Store) FinishRun(ctx context.Context, id int64, result string, count int, message string) error {
	message = truncate(message, 1024)
	_, err := s.db.ExecContext(ctx, `UPDATE discovery_runs SET finished_at=NOW(),result=$2,found_count=$3,message=$4 WHERE id=$1`, id, result, count, message)
	if err != nil {
		return fmt.Errorf("finish discovery run: %w", err)
	}
	return nil
}

func (s *Store) UpsertCandidate(ctx context.Context, value DiscoveredJam, seenAt time.Time) error {
	id, err := newID()
	if err != nil {
		return fmt.Errorf("create candidate id: %w", err)
	}
	languages, _ := json.Marshal(value.Languages)
	_, err = s.db.ExecContext(ctx, `INSERT INTO gamejam_candidates(
id,source,external_id,source_url,official_url,title,organizer,format,city,country_code,languages,starts_on,ends_on,description,relevance,relevance_notes,source_digest,first_seen_at,last_seen_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$18)
ON CONFLICT(source,external_id) DO UPDATE SET
source_url=EXCLUDED.source_url,official_url=EXCLUDED.official_url,title=EXCLUDED.title,organizer=EXCLUDED.organizer,format=EXCLUDED.format,
city=EXCLUDED.city,country_code=EXCLUDED.country_code,languages=EXCLUDED.languages,starts_on=EXCLUDED.starts_on,ends_on=EXCLUDED.ends_on,
description=EXCLUDED.description,relevance=EXCLUDED.relevance,relevance_notes=EXCLUDED.relevance_notes,source_digest=EXCLUDED.source_digest,
last_seen_at=EXCLUDED.last_seen_at,missing_runs=0,status=CASE WHEN gamejam_candidates.status='archived' THEN 'pending' ELSE gamejam_candidates.status END,updated_at=NOW()`,
		id, value.Source, value.ExternalID, value.SourceURL, value.OfficialURL, value.Title, value.Organizer, string(value.Format), value.City,
		strings.ToUpper(value.CountryCode), languages, value.StartsOn, value.EndsOn, value.Description, string(value.Relevance), value.RelevanceNotes,
		discoveryDigest(value), seenAt)
	if err != nil {
		return fmt.Errorf("upsert game jam candidate: %w", err)
	}
	return nil
}

func (s *Store) MarkMissing(ctx context.Context, source string, runStartedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE gamejam_candidates c SET
missing_runs=missing_runs+1,
status=CASE WHEN missing_runs+1 >= 3 THEN 'archived' ELSE status END,
updated_at=NOW()
WHERE c.source=$1 AND c.last_seen_at < $2 AND c.status='pending'
AND NOT EXISTS (SELECT 1 FROM gamejam_candidate_links l WHERE l.candidate_id=c.id)`, source, runStartedAt)
	if err != nil {
		return fmt.Errorf("mark missing candidates: %w", err)
	}
	return nil
}

func (s *Store) ListCandidates(ctx context.Context, filter CandidateFilter) ([]Candidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,c.source,c.external_id,c.source_url,c.official_url,c.title,c.organizer,c.format,c.city,c.country_code,
c.languages,c.starts_on,c.ends_on,c.description,c.relevance,c.relevance_notes,c.status,
(c.reviewed_digest<>'' AND c.reviewed_digest<>c.source_digest),COALESCE(l.game_jam_id,''),
COALESCE((SELECT l2.game_jam_id FROM gamejam_candidates c2 JOIN gamejam_candidate_links l2 ON l2.candidate_id=c2.id
  WHERE c2.id<>c.id AND c.official_url<>'' AND c2.official_url=c.official_url ORDER BY l2.linked_at LIMIT 1),''),
COALESCE((SELECT jsonb_agg(s.game_jam_id) FROM (SELECT j.id AS game_jam_id FROM game_jams j
  WHERE lower(regexp_replace(j.title,'[^[:alnum:]]','','g'))=lower(regexp_replace(c.title,'[^[:alnum:]]','','g'))
  AND j.starts_on=c.starts_on AND j.ends_on=c.ends_on
  AND (c.organizer='' OR j.organizer='' OR lower(regexp_replace(j.organizer,'[^[:alnum:]]','','g'))=lower(regexp_replace(c.organizer,'[^[:alnum:]]','','g')))
  ORDER BY j.created_at LIMIT 5) s),'[]'::jsonb),c.first_seen_at,c.last_seen_at
FROM gamejam_candidates c LEFT JOIN gamejam_candidate_links l ON l.candidate_id=c.id
WHERE ($1='' OR c.source=$1) AND ($2='' OR c.format=$2) AND ($3='' OR c.relevance=$3) AND ($4='' OR c.status=$4)
ORDER BY c.starts_on,c.title,c.id LIMIT $5 OFFSET $6`, filter.Source, string(filter.Format), string(filter.Relevance), filter.Status, filter.Limit, filter.Offset)
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	defer rows.Close()
	values := make([]Candidate, 0)
	for rows.Next() {
		value, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidates: %w", err)
	}
	return values, nil
}

func (s *Store) GetCandidate(ctx context.Context, id string) (Candidate, error) {
	row := s.db.QueryRowContext(ctx, `SELECT c.id,c.source,c.external_id,c.source_url,c.official_url,c.title,c.organizer,c.format,c.city,c.country_code,
c.languages,c.starts_on,c.ends_on,c.description,c.relevance,c.relevance_notes,c.status,
(c.reviewed_digest<>'' AND c.reviewed_digest<>c.source_digest),COALESCE(l.game_jam_id,''),
COALESCE((SELECT l2.game_jam_id FROM gamejam_candidates c2 JOIN gamejam_candidate_links l2 ON l2.candidate_id=c2.id
  WHERE c2.id<>c.id AND c.official_url<>'' AND c2.official_url=c.official_url ORDER BY l2.linked_at LIMIT 1),''),
COALESCE((SELECT jsonb_agg(s.game_jam_id) FROM (SELECT j.id AS game_jam_id FROM game_jams j
  WHERE lower(regexp_replace(j.title,'[^[:alnum:]]','','g'))=lower(regexp_replace(c.title,'[^[:alnum:]]','','g'))
  AND j.starts_on=c.starts_on AND j.ends_on=c.ends_on
  AND (c.organizer='' OR j.organizer='' OR lower(regexp_replace(j.organizer,'[^[:alnum:]]','','g'))=lower(regexp_replace(c.organizer,'[^[:alnum:]]','','g')))
  ORDER BY j.created_at LIMIT 5) s),'[]'::jsonb),c.first_seen_at,c.last_seen_at
FROM gamejam_candidates c LEFT JOIN gamejam_candidate_links l ON l.candidate_id=c.id WHERE c.id=$1`, id)
	value, err := scanCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Candidate{}, ErrNotFound
	}
	return value, err
}

type scanner interface{ Scan(...any) error }

func scanCandidate(row scanner) (Candidate, error) {
	var value Candidate
	var format, relevance string
	var languages, possibleDuplicates []byte
	if err := row.Scan(&value.ID, &value.Source, &value.ExternalID, &value.SourceURL, &value.OfficialURL, &value.Title, &value.Organizer, &format,
		&value.City, &value.CountryCode, &languages, &value.StartsOn, &value.EndsOn, &value.Description, &relevance, &value.RelevanceNotes,
		&value.Status, &value.SourceChanged, &value.LinkedGameJamID, &value.ExactDuplicateGameJamID, &possibleDuplicates, &value.FirstSeenAt, &value.LastSeenAt); err != nil {
		return Candidate{}, err
	}
	value.Format, value.Relevance = Format(format), Relevance(relevance)
	if err := json.Unmarshal(languages, &value.Languages); err != nil {
		return Candidate{}, fmt.Errorf("decode candidate languages: %w", err)
	}
	if err := json.Unmarshal(possibleDuplicates, &value.PossibleDuplicateGameJamIDs); err != nil {
		return Candidate{}, fmt.Errorf("decode candidate duplicate suggestions: %w", err)
	}
	return value, nil
}

type CandidateUpdate struct {
	OfficialURL string
	Title       string
	Organizer   string
	Format      Format
	City        string
	CountryCode string
	Languages   []string
	StartsOn    time.Time
	EndsOn      time.Time
	Relevance   Relevance
}

func (s *Store) UpdateCandidate(ctx context.Context, id string, value CandidateUpdate) error {
	languages, _ := json.Marshal(value.Languages)
	result, err := s.db.ExecContext(ctx, `UPDATE gamejam_candidates SET official_url=$2,title=$3,organizer=$4,format=$5,city=$6,country_code=$7,languages=$8,starts_on=$9,ends_on=$10,relevance=$11,updated_at=NOW() WHERE id=$1`,
		id, value.OfficialURL, value.Title, value.Organizer, string(value.Format), value.City, strings.ToUpper(value.CountryCode), languages, value.StartsOn, value.EndsOn, string(value.Relevance))
	if err != nil {
		return fmt.Errorf("update candidate: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RejectCandidate(ctx context.Context, id, reason string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE gamejam_candidates SET status='rejected',rejection_reason=$2,reviewed_digest=source_digest,updated_at=NOW() WHERE id=$1 AND status<>'archived'`, id, truncate(reason, 512))
	if err != nil {
		return fmt.Errorf("reject candidate: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ApproveCandidate(ctx context.Context, candidateID string, reason EligibilityReason, code ProtectedCode) (GameJam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GameJam{}, fmt.Errorf("begin candidate approval: %w", err)
	}
	defer tx.Rollback()
	candidate, err := getCandidateForUpdate(ctx, tx, candidateID)
	if err != nil {
		return GameJam{}, err
	}
	if candidate.LinkedGameJamID != "" {
		return GameJam{}, ErrConflict
	}
	if candidate.Status != "pending" {
		return GameJam{}, ErrConflict
	}
	if candidate.OfficialURL != "" {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, candidate.OfficialURL); err != nil {
			return GameJam{}, fmt.Errorf("lock official URL deduplication: %w", err)
		}
		var exactDuplicate bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM gamejam_candidates c JOIN gamejam_candidate_links l ON l.candidate_id=c.id
WHERE c.id<>$1 AND c.official_url=$2)`, candidate.ID, candidate.OfficialURL).Scan(&exactDuplicate); err != nil {
			return GameJam{}, fmt.Errorf("check official URL duplicate: %w", err)
		}
		if exactDuplicate {
			return GameJam{}, ErrConflict
		}
	}
	jamID, err := newID()
	if err != nil {
		return GameJam{}, fmt.Errorf("create game jam id: %w", err)
	}
	languages, _ := json.Marshal(candidate.Languages)
	_, err = tx.ExecContext(ctx, `INSERT INTO game_jams(id,title,organizer,format,city,country_code,languages,starts_on,ends_on,eligibility_reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		jamID, candidate.Title, candidate.Organizer, string(candidate.Format), candidate.City, candidate.CountryCode, languages, candidate.StartsOn, candidate.EndsOn, string(reason))
	if err != nil {
		return GameJam{}, fmt.Errorf("insert game jam: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO gamejam_candidate_links(candidate_id,game_jam_id) VALUES($1,$2)`, candidateID, jamID); err != nil {
		return GameJam{}, fmt.Errorf("link candidate: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE gamejam_candidates SET status='approved',reviewed_digest=source_digest,updated_at=NOW() WHERE id=$1`, candidateID); err != nil {
		return GameJam{}, fmt.Errorf("mark candidate reviewed: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO promotion_codes(game_jam_id,lookup_hmac,ciphertext,nonce,last_four) VALUES($1,$2,$3,$4,$5)`, jamID, code.LookupHMAC, code.Ciphertext, code.Nonce, code.LastFour); err != nil {
		if isUniqueViolation(err) {
			return GameJam{}, ErrCodeCollision
		}
		return GameJam{}, fmt.Errorf("insert promotion code: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GameJam{}, fmt.Errorf("commit candidate approval: %w", err)
	}
	return s.GetGameJam(ctx, jamID)
}

func getCandidateForUpdate(ctx context.Context, tx *sql.Tx, id string) (Candidate, error) {
	row := tx.QueryRowContext(ctx, `SELECT c.id,c.source,c.external_id,c.source_url,c.official_url,c.title,c.organizer,c.format,c.city,c.country_code,
c.languages,c.starts_on,c.ends_on,c.description,c.relevance,c.relevance_notes,c.status,
(c.reviewed_digest<>'' AND c.reviewed_digest<>c.source_digest),COALESCE(l.game_jam_id,''),
COALESCE((SELECT l2.game_jam_id FROM gamejam_candidates c2 JOIN gamejam_candidate_links l2 ON l2.candidate_id=c2.id
  WHERE c2.id<>c.id AND c.official_url<>'' AND c2.official_url=c.official_url ORDER BY l2.linked_at LIMIT 1),''),
COALESCE((SELECT jsonb_agg(s.game_jam_id) FROM (SELECT j.id AS game_jam_id FROM game_jams j
  WHERE lower(regexp_replace(j.title,'[^[:alnum:]]','','g'))=lower(regexp_replace(c.title,'[^[:alnum:]]','','g'))
  AND j.starts_on=c.starts_on AND j.ends_on=c.ends_on
  AND (c.organizer='' OR j.organizer='' OR lower(regexp_replace(j.organizer,'[^[:alnum:]]','','g'))=lower(regexp_replace(c.organizer,'[^[:alnum:]]','','g')))
  ORDER BY j.created_at LIMIT 5) s),'[]'::jsonb),c.first_seen_at,c.last_seen_at
FROM gamejam_candidates c LEFT JOIN gamejam_candidate_links l ON l.candidate_id=c.id WHERE c.id=$1 FOR UPDATE OF c`, id)
	value, err := scanCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Candidate{}, ErrNotFound
	}
	return value, err
}

func (s *Store) MergeCandidate(ctx context.Context, candidateID, gameJamID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	candidate, err := getCandidateForUpdate(ctx, tx, candidateID)
	if err != nil {
		return err
	}
	if candidate.Status != "pending" || candidate.LinkedGameJamID != "" {
		return ErrConflict
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_jams WHERE id=$1)`, gameJamID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gamejam_candidate_links(candidate_id,game_jam_id) VALUES($1,$2)`, candidateID, gameJamID); err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gamejam_candidates SET status='approved',reviewed_digest=source_digest,updated_at=NOW() WHERE id=$1`, candidateID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReviewCandidateUpdate(ctx context.Context, candidateID string, apply bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin candidate update review: %w", err)
	}
	defer tx.Rollback()
	candidate, err := getCandidateForUpdate(ctx, tx, candidateID)
	if err != nil {
		return err
	}
	if candidate.LinkedGameJamID == "" || !candidate.SourceChanged {
		return ErrConflict
	}
	if apply {
		languages, _ := json.Marshal(candidate.Languages)
		result, err := tx.ExecContext(ctx, `UPDATE game_jams SET title=$2,organizer=$3,format=$4,city=$5,country_code=$6,languages=$7,starts_on=$8,ends_on=$9,updated_at=NOW() WHERE id=$1`,
			candidate.LinkedGameJamID, candidate.Title, candidate.Organizer, string(candidate.Format), candidate.City, candidate.CountryCode, languages, candidate.StartsOn, candidate.EndsOn)
		if err != nil {
			return fmt.Errorf("apply candidate source update: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return ErrNotFound
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gamejam_candidates SET reviewed_digest=source_digest,updated_at=NOW() WHERE id=$1`, candidateID); err != nil {
		return fmt.Errorf("acknowledge candidate source update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit candidate update review: %w", err)
	}
	return nil
}

func (s *Store) ListGameJams(ctx context.Context, limit, offset int) ([]GameJam, []ProtectedCode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT j.id,j.title,j.organizer,j.format,j.city,j.country_code,j.languages,j.starts_on,j.ends_on,j.eligibility_reason,j.status,j.created_at,j.updated_at,
COALESCE(p.lookup_hmac,'\x'::bytea),COALESCE(p.ciphertext,'\x'::bytea),COALESCE(p.nonce,'\x'::bytea),COALESCE(p.last_four,'')
FROM game_jams j LEFT JOIN promotion_codes p ON p.game_jam_id=j.id AND p.revoked_at IS NULL
ORDER BY j.starts_on DESC,j.title LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, nil, fmt.Errorf("list game jams: %w", err)
	}
	defer rows.Close()
	jams, codes := make([]GameJam, 0), make([]ProtectedCode, 0)
	for rows.Next() {
		jam, code, err := scanGameJam(rows)
		if err != nil {
			return nil, nil, err
		}
		jams, codes = append(jams, jam), append(codes, code)
	}
	return jams, codes, rows.Err()
}

func (s *Store) GetGameJam(ctx context.Context, id string) (GameJam, error) {
	row := s.db.QueryRowContext(ctx, `SELECT j.id,j.title,j.organizer,j.format,j.city,j.country_code,j.languages,j.starts_on,j.ends_on,j.eligibility_reason,j.status,j.created_at,j.updated_at,
COALESCE(p.lookup_hmac,'\x'::bytea),COALESCE(p.ciphertext,'\x'::bytea),COALESCE(p.nonce,'\x'::bytea),COALESCE(p.last_four,'')
FROM game_jams j LEFT JOIN promotion_codes p ON p.game_jam_id=j.id AND p.revoked_at IS NULL WHERE j.id=$1`, id)
	jam, _, err := scanGameJam(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GameJam{}, ErrNotFound
	}
	return jam, err
}

func scanGameJam(row scanner) (GameJam, ProtectedCode, error) {
	var value GameJam
	var code ProtectedCode
	var format, reason string
	var languages []byte
	if err := row.Scan(&value.ID, &value.Title, &value.Organizer, &format, &value.City, &value.CountryCode, &languages, &value.StartsOn, &value.EndsOn,
		&reason, &value.Status, &value.CreatedAt, &value.UpdatedAt, &code.LookupHMAC, &code.Ciphertext, &code.Nonce, &code.LastFour); err != nil {
		return GameJam{}, ProtectedCode{}, err
	}
	value.Format, value.EligibilityReason, value.CodeLastFour = Format(format), EligibilityReason(reason), code.LastFour
	if err := json.Unmarshal(languages, &value.Languages); err != nil {
		return GameJam{}, ProtectedCode{}, err
	}
	return value, code, nil
}

type GameJamUpdate struct {
	Title       string
	Organizer   string
	Format      Format
	City        string
	CountryCode string
	Languages   []string
	StartsOn    time.Time
	EndsOn      time.Time
	Status      string
}

func (s *Store) UpdateGameJam(ctx context.Context, id string, value GameJamUpdate) error {
	languages, _ := json.Marshal(value.Languages)
	result, err := s.db.ExecContext(ctx, `UPDATE game_jams SET title=$2,organizer=$3,format=$4,city=$5,country_code=$6,languages=$7,starts_on=$8,ends_on=$9,status=$10,updated_at=NOW() WHERE id=$1`,
		id, value.Title, value.Organizer, string(value.Format), value.City, strings.ToUpper(value.CountryCode), languages, value.StartsOn, value.EndsOn, value.Status)
	if err != nil {
		return fmt.Errorf("update game jam: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RotateCode(ctx context.Context, gameJamID string, code ProtectedCode) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE promotion_codes SET revoked_at=NOW() WHERE game_jam_id=$1 AND revoked_at IS NULL`, gameJamID)
	if err != nil {
		return fmt.Errorf("revoke promotion code: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO promotion_codes(game_jam_id,lookup_hmac,ciphertext,nonce,last_four) VALUES($1,$2,$3,$4,$5)`, gameJamID, code.LookupHMAC, code.Ciphertext, code.Nonce, code.LastFour); err != nil {
		if isUniqueViolation(err) {
			return ErrCodeCollision
		}
		return err
	}
	return tx.Commit()
}

func (s *Store) FindActiveByCode(ctx context.Context, lookup []byte, date time.Time) (GameJam, error) {
	row := s.db.QueryRowContext(ctx, `SELECT j.id,j.title,j.organizer,j.format,j.city,j.country_code,j.languages,j.starts_on,j.ends_on,j.eligibility_reason,j.status,j.created_at,j.updated_at,
p.lookup_hmac,p.ciphertext,p.nonce,p.last_four
FROM promotion_codes p JOIN game_jams j ON j.id=p.game_jam_id
WHERE p.lookup_hmac=$1 AND p.revoked_at IS NULL AND j.status='approved' AND j.starts_on<=$2::date AND j.ends_on>=$2::date`, lookup, date)
	jam, _, err := scanGameJam(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GameJam{}, ErrNotFound
	}
	return jam, err
}

func (s *Store) ListRuns(ctx context.Context, limit int) ([]DiscoveryRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,source,started_at,finished_at,result,found_count,message FROM discovery_runs ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]DiscoveryRun, 0)
	for rows.Next() {
		var value DiscoveryRun
		if err := rows.Scan(&value.ID, &value.Source, &value.StartedAt, &value.FinishedAt, &value.Result, &value.FoundCount, &value.Message); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) CountPending(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM gamejam_candidates WHERE status='pending'`).Scan(&count)
	return count, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
