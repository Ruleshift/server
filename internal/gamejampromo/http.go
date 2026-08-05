package gamejampromo

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	maxPublicBody = 4 << 10
	maxAdminBody  = 64 << 10
)

type PublicHandler struct {
	service       *Service
	store         *Store
	allowedOrigin string
	semaphore     chan struct{}
	mux           *http.ServeMux
}

func NewPublicHandler(service *Service, store *Store, allowedOrigin string, maxConcurrent int) *PublicHandler {
	h := &PublicHandler{service: service, store: store, allowedOrigin: allowedOrigin, semaphore: make(chan struct{}, maxConcurrent), mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /healthz", h.health)
	h.mux.HandleFunc("GET /readyz", h.ready)
	h.mux.HandleFunc("POST /v1/gamejam-discounts/verify", h.verify)
	return h
}

func (h *PublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	if origin := r.Header.Get("Origin"); origin != "" && origin == h.allowedOrigin {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	if r.Method == http.MethodOptions {
		if r.Header.Get("Origin") != h.allowedOrigin || r.URL.Path != "/v1/gamejam-discounts/verify" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.mux.ServeHTTP(w, r)
}

func (h *PublicHandler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *PublicHandler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := h.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *PublicHandler) verify(w http.ResponseWriter, r *http.Request) {
	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "temporarily_unavailable"})
		return
	}
	var request struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, maxPublicBody, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	jam, valid, err := h.service.VerifyCode(r.Context(), request.Code, time.Now())
	switch {
	case errors.Is(err, ErrValidation):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
	case err != nil:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "temporarily_unavailable"})
	case !valid:
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "message": "Код недействителен или сейчас не действует"})
	default:
		gameJam := map[string]any{"title": jam.Title, "format": jam.Format, "ends_on": jam.EndsOn.Format(time.DateOnly)}
		if jam.City != "" && jam.Format != FormatOnline {
			gameJam["city"] = jam.City
		} else {
			gameJam["city"] = nil
		}
		writeJSON(w, http.StatusOK, map[string]any{"valid": true, "message": "Право на скидку подтверждено", "game_jam": gameJam})
	}
}

type AdminHandler struct {
	service      *Service
	discovery    *Discovery
	store        *Store
	metrics      *Metrics
	username     string
	passwordHash []byte
	mux          *http.ServeMux
	logger       *slog.Logger
}

func NewAdminHandler(service *Service, discovery *Discovery, store *Store, metrics *Metrics, username, passwordHash string, logger *slog.Logger) *AdminHandler {
	h := &AdminHandler{service: service, discovery: discovery, store: store, metrics: metrics, username: username, passwordHash: []byte(passwordHash), mux: http.NewServeMux(), logger: logger}
	h.mux.HandleFunc("GET /healthz", h.health)
	h.mux.HandleFunc("GET /readyz", h.ready)
	h.mux.Handle("GET /metrics", metrics.Handler())
	h.mux.HandleFunc("GET /admin/", h.page)
	h.mux.HandleFunc("GET /admin/api/v1/candidates", h.listCandidates)
	h.mux.HandleFunc("PATCH /admin/api/v1/candidates/{id}", h.updateCandidate)
	h.mux.HandleFunc("POST /admin/api/v1/candidates/{id}/approve", h.approveCandidate)
	h.mux.HandleFunc("POST /admin/api/v1/candidates/{id}/reject", h.rejectCandidate)
	h.mux.HandleFunc("POST /admin/api/v1/candidates/{id}/merge", h.mergeCandidate)
	h.mux.HandleFunc("POST /admin/api/v1/candidates/{id}/review-update", h.reviewCandidateUpdate)
	h.mux.HandleFunc("GET /admin/api/v1/gamejams", h.listGameJams)
	h.mux.HandleFunc("PATCH /admin/api/v1/gamejams/{id}", h.updateGameJam)
	h.mux.HandleFunc("POST /admin/api/v1/gamejams/{id}/rotate-code", h.rotateCode)
	h.mux.HandleFunc("GET /admin/api/v1/discovery/runs", h.listRuns)
	h.mux.HandleFunc("POST /admin/api/v1/discovery/run", h.runDiscovery)
	return h
}

func (h *AdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	w.Header().Set("X-Frame-Options", "DENY")
	username, password, ok := r.BasicAuth()
	usernameOK := subtle.ConstantTimeCompare([]byte(username), []byte(h.username)) == 1
	passwordOK := bcrypt.CompareHashAndPassword(h.passwordHash, []byte(password)) == nil
	if !ok || !usernameOK || !passwordOK {
		w.Header().Set("WWW-Authenticate", `Basic realm="Ruleshift game jam administration", charset="UTF-8"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	h.mux.ServeHTTP(w, r)
}

func (h *AdminHandler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (h *AdminHandler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := h.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *AdminHandler) page(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, adminHTML)
}

func (h *AdminHandler) listCandidates(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50)
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	offset, err := queryInt(r, "offset", 0)
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	values, err := h.service.ListCandidates(r.Context(), CandidateFilter{Source: r.URL.Query().Get("source"), Format: Format(r.URL.Query().Get("format")), Relevance: Relevance(r.URL.Query().Get("relevance")), Status: r.URL.Query().Get("status"), Limit: limit, Offset: offset})
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values, "limit": limit, "offset": offset})
}

type candidateRequest struct {
	OfficialURL string    `json:"official_url"`
	Title       string    `json:"title"`
	Organizer   string    `json:"organizer"`
	Format      Format    `json:"format"`
	City        string    `json:"city"`
	CountryCode string    `json:"country_code"`
	Languages   []string  `json:"languages"`
	StartsOn    string    `json:"starts_on"`
	EndsOn      string    `json:"ends_on"`
	Relevance   Relevance `json:"relevance"`
}

func (h *AdminHandler) updateCandidate(w http.ResponseWriter, r *http.Request) {
	var request candidateRequest
	if err := decodeJSON(w, r, maxAdminBody, &request); err != nil {
		h.writeAdminError(w, fmt.Errorf("%w: invalid body", ErrValidation))
		return
	}
	starts, err := parseDate(request.StartsOn)
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	ends, err := parseDate(request.EndsOn)
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	err = h.service.UpdateCandidate(r.Context(), r.PathValue("id"), CandidateUpdate{OfficialURL: request.OfficialURL, Title: request.Title, Organizer: request.Organizer, Format: request.Format, City: request.City, CountryCode: request.CountryCode, Languages: request.Languages, StartsOn: starts, EndsOn: ends, Relevance: request.Relevance})
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) approveCandidate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Reason EligibilityReason `json:"reason"`
	}
	if err := decodeJSON(w, r, maxAdminBody, &request); err != nil {
		h.writeAdminError(w, fmt.Errorf("%w: invalid body", ErrValidation))
		return
	}
	value, err := h.service.ApproveCandidate(r.Context(), r.PathValue("id"), request.Reason)
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (h *AdminHandler) rejectCandidate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(w, r, maxAdminBody, &request); err != nil {
		h.writeAdminError(w, fmt.Errorf("%w: invalid body", ErrValidation))
		return
	}
	if err := h.service.RejectCandidate(r.Context(), r.PathValue("id"), request.Reason); err != nil {
		h.writeAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) mergeCandidate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		GameJamID string `json:"game_jam_id"`
	}
	if err := decodeJSON(w, r, maxAdminBody, &request); err != nil {
		h.writeAdminError(w, fmt.Errorf("%w: invalid body", ErrValidation))
		return
	}
	if err := h.service.MergeCandidate(r.Context(), r.PathValue("id"), request.GameJamID); err != nil {
		h.writeAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) reviewCandidateUpdate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Apply *bool `json:"apply"`
	}
	if err := decodeJSON(w, r, maxAdminBody, &request); err != nil || request.Apply == nil {
		h.writeAdminError(w, fmt.Errorf("%w: apply is required", ErrValidation))
		return
	}
	if err := h.service.ReviewCandidateUpdate(r.Context(), r.PathValue("id"), *request.Apply); err != nil {
		h.writeAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) listGameJams(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50)
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	offset, err := queryInt(r, "offset", 0)
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	values, err := h.service.ListGameJams(r.Context(), limit, offset)
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values, "limit": limit, "offset": offset})
}

type gameJamRequest struct {
	Title       string   `json:"title"`
	Organizer   string   `json:"organizer"`
	Format      Format   `json:"format"`
	City        string   `json:"city"`
	CountryCode string   `json:"country_code"`
	Languages   []string `json:"languages"`
	StartsOn    string   `json:"starts_on"`
	EndsOn      string   `json:"ends_on"`
	Status      string   `json:"status"`
}

func (h *AdminHandler) updateGameJam(w http.ResponseWriter, r *http.Request) {
	var request gameJamRequest
	if err := decodeJSON(w, r, maxAdminBody, &request); err != nil {
		h.writeAdminError(w, fmt.Errorf("%w: invalid body", ErrValidation))
		return
	}
	starts, err := parseDate(request.StartsOn)
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	ends, err := parseDate(request.EndsOn)
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	err = h.service.UpdateGameJam(r.Context(), r.PathValue("id"), GameJamUpdate{Title: request.Title, Organizer: request.Organizer, Format: request.Format, City: request.City, CountryCode: request.CountryCode, Languages: request.Languages, StartsOn: starts, EndsOn: ends, Status: request.Status})
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) rotateCode(w http.ResponseWriter, r *http.Request) {
	code, err := h.service.RotateCode(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"code": code})
}

func (h *AdminHandler) listRuns(w http.ResponseWriter, r *http.Request) {
	values, err := h.service.ListRuns(r.Context())
	if err != nil {
		h.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values})
}

func (h *AdminHandler) runDiscovery(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	go func() {
		defer cancel()
		if err := h.discovery.Run(ctx); err != nil && !errors.Is(err, ErrDiscoveryBusy) {
			h.logger.Error("manual game jam discovery failed")
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (h *AdminHandler) writeAdminError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, ErrValidation):
		status, code = http.StatusBadRequest, "validation_error"
	case errors.Is(err, ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, ErrConflict):
		status, code = http.StatusConflict, "conflict"
	}
	writeJSON(w, status, map[string]string{"code": code})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
}

func queryInt(r *http.Request, key string, fallback int) (int, error) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be an integer", ErrValidation, key)
	}
	return parsed, nil
}

func parseDate(value string) (time.Time, error) {
	parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: date must use YYYY-MM-DD", ErrValidation)
	}
	return parsed, nil
}
