package developerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Ruleshift/server/internal/game"
	storagepostgres "github.com/Ruleshift/server/internal/storage/postgres"
	"github.com/Ruleshift/server/pkg/ruleshift"
)

const maxRequestBytes = 1 << 20

type Store interface {
	AuthenticateDeveloper(ctx context.Context, apiKey string) (string, error)
	ProvisionDefinition(ctx context.Context, developerID, displayName string, definition game.DatabaseDefinition) error
	GetModule(ctx context.Context, developerID, moduleKey string) (ruleshift.Module, error)
	ListModules(ctx context.Context, developerID string) ([]ruleshift.Module, error)
	DescribeModule(ctx context.Context, developerID, moduleKey string) (ruleshift.Schema, error)
	ListTableRows(ctx context.Context, developerID, moduleKey, tableName string, limit, offset int) (ruleshift.RowsPage, error)
	CreateTableRow(ctx context.Context, developerID, moduleKey, tableName string, values map[string]any) (ruleshift.Row, error)
}

type Handler struct {
	store Store
	mux   *http.ServeMux
}

type developerContextKey struct{}

func New(store Store) (*Handler, error) {
	if store == nil {
		return nil, fmt.Errorf("developer API store must not be nil")
	}
	handler := &Handler{store: store, mux: http.NewServeMux()}
	handler.mux.HandleFunc("GET /v1/developer/modules", handler.listModules)
	handler.mux.HandleFunc("POST /v1/developer/modules", handler.createModule)
	handler.mux.HandleFunc("GET /v1/developer/modules/{module}/schema", handler.getSchema)
	handler.mux.HandleFunc("GET /v1/developer/modules/{module}/tables/{table}/rows", handler.listRows)
	handler.mux.HandleFunc("POST /v1/developer/modules/{module}/tables/{table}/rows", handler.createRow)
	return handler, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	developerID, err := h.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "a valid developer API bearer token is required")
		return
	}
	ctx := context.WithValue(r.Context(), developerContextKey{}, developerID)
	h.mux.ServeHTTP(w, r.WithContext(ctx))
}

func (h *Handler) createModule(w http.ResponseWriter, r *http.Request) {
	var request ruleshift.CreateModuleRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	definition, err := compileDefinition(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_schema", err.Error())
		return
	}
	if err := h.store.ProvisionDefinition(r.Context(), developerID(r), request.DisplayName, definition); err != nil {
		writeError(w, http.StatusConflict, "provision_failed", err.Error())
		return
	}
	module, err := h.store.GetModule(r.Context(), developerID(r), request.Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "module_read_failed", "module was provisioned but could not be read")
		return
	}
	writeJSON(w, http.StatusCreated, module)
}

func (h *Handler) listModules(w http.ResponseWriter, r *http.Request) {
	modules, err := h.store.ListModules(r.Context(), developerID(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "module_list_failed", "could not list modules")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"modules": modules})
}

func (h *Handler) getSchema(w http.ResponseWriter, r *http.Request) {
	schema, err := h.store.DescribeModule(r.Context(), developerID(r), r.PathValue("module"))
	if errors.Is(err, storagepostgres.ErrModuleNotFound) {
		writeError(w, http.StatusNotFound, "module_not_found", "module does not exist")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "schema_read_failed", "could not read module schema")
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

func (h *Handler) listRows(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	offset, err := queryInt(r, "offset", 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_offset", err.Error())
		return
	}
	page, err := h.store.ListTableRows(r.Context(), developerID(r), r.PathValue("module"), r.PathValue("table"), limit, offset)
	switch {
	case errors.Is(err, storagepostgres.ErrModuleNotFound):
		writeError(w, http.StatusNotFound, "module_not_found", "module does not exist")
		return
	case errors.Is(err, storagepostgres.ErrTableNotFound):
		writeError(w, http.StatusNotFound, "table_not_found", "table does not exist")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, "rows_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) createRow(w http.ResponseWriter, r *http.Request) {
	var request ruleshift.CreateRowRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(request.Values) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_row", "values must not be empty")
		return
	}
	row, err := h.store.CreateTableRow(r.Context(), developerID(r), r.PathValue("module"), r.PathValue("table"), request.Values)
	switch {
	case errors.Is(err, storagepostgres.ErrModuleNotFound):
		writeError(w, http.StatusNotFound, "module_not_found", "module does not exist")
		return
	case errors.Is(err, storagepostgres.ErrTableNotFound):
		writeError(w, http.StatusNotFound, "table_not_found", "table does not exist or is platform-managed")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, "row_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (h *Handler) authenticate(r *http.Request) (string, error) {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return "", storagepostgres.ErrInvalidDeveloperAPIKey
	}
	provided := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return h.store.AuthenticateDeveloper(r.Context(), provided)
}

func developerID(r *http.Request) string {
	developerID, _ := r.Context().Value(developerContextKey{}).(string)
	return developerID
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("JSON body must contain one value")
	}
	return nil
}

func queryInt(r *http.Request, key string, fallback int) (int, error) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return parsed, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}
