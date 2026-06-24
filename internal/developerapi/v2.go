package developerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/roomcore"
	storagepostgres "github.com/Ruleshift/server/internal/storage/postgres"
	"github.com/Ruleshift/server/pkg/ruleshift"
)

const maxPublishBytes = controlplane.MaxDescriptorBytes + (4 << 20)

type APIKeyAuthenticator interface {
	AuthenticateDeveloper(context.Context, string) (string, error)
}

type RoomService interface {
	CreateRoom(context.Context, string, string, string) (roomcore.Route, error)
	GetRoom(context.Context, string, string) (roomcore.Route, error)
}

type V2Handler struct {
	auth      APIKeyAuthenticator
	store     controlplane.Store
	scheduler controlplane.Scheduler
	validator controlplane.Validator
	rooms     RoomService
	data      V2DataStore
	mux       *http.ServeMux
}

type V2DataStore interface {
	ListTableRows(context.Context, string, string, string, int, int) (ruleshift.RowsPage, error)
	CreateTableRow(context.Context, string, string, string, map[string]any) (ruleshift.Row, error)
}

func NewV2(auth APIKeyAuthenticator, store controlplane.Store, scheduler controlplane.Scheduler, validator controlplane.Validator, rooms RoomService) (*V2Handler, error) {
	if auth == nil || store == nil || scheduler == nil || rooms == nil {
		return nil, fmt.Errorf("developer API v2 dependencies are required")
	}
	h := &V2Handler{auth: auth, store: store, scheduler: scheduler, validator: validator, rooms: rooms, mux: http.NewServeMux()}
	h.data, _ = auth.(V2DataStore)
	h.mux.HandleFunc("PUT /v2/developer/registry-credentials/{name}", h.putRegistryCredential)
	h.mux.HandleFunc("POST /v2/developer/modules", h.createModule)
	h.mux.HandleFunc("POST /v2/developer/modules/{module}/versions", h.publishVersion)
	h.mux.HandleFunc("GET /v2/developer/modules/{module}/versions/{version}", h.getVersion)
	h.mux.HandleFunc("GET /v2/developer/modules/{module}/versions/{version}/validation", h.getValidation)
	h.mux.HandleFunc("POST /v2/rooms", h.createRoom)
	h.mux.HandleFunc("GET /v2/rooms/{room_id}", h.getRoom)
	if h.data != nil {
		h.mux.HandleFunc("GET /v2/developer/modules/{module}/tables/{table}/rows", h.listRowsV2)
		h.mux.HandleFunc("POST /v2/developer/modules/{module}/tables/{table}/rows", h.createRowV2)
	}
	return h, nil
}

func (h *V2Handler) listRowsV2(w http.ResponseWriter, r *http.Request) {
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
	page, err := h.data.ListTableRows(r.Context(), developerID(r), r.PathValue("module"), r.PathValue("table"), limit, offset)
	if errors.Is(err, storagepostgres.ErrModuleNotFound) {
		writeError(w, http.StatusNotFound, "module_not_found", "module does not exist")
		return
	}
	if errors.Is(err, storagepostgres.ErrTableNotFound) {
		writeError(w, http.StatusNotFound, "table_not_found", "table does not exist")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "rows_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}
func (h *V2Handler) createRowV2(w http.ResponseWriter, r *http.Request) {
	var request ruleshift.CreateRowRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	row, err := h.data.CreateTableRow(r.Context(), developerID(r), r.PathValue("module"), r.PathValue("table"), request.Values)
	if errors.Is(err, storagepostgres.ErrModuleNotFound) || errors.Is(err, storagepostgres.ErrTableNotFound) {
		writeError(w, http.StatusNotFound, "table_not_found", "module or table does not exist")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "row_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (h *V2Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	developerID, err := authenticateV2(r, h.auth)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "a valid developer API bearer token is required")
		return
	}
	h.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), developerContextKey{}, developerID)))
}

func (h *V2Handler) putRegistryCredential(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Server   string `json:"server"`
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	secret := request.Password
	if secret == "" {
		secret = request.Token
	}
	if err := h.scheduler.PutRegistryCredential(r.Context(), developerID(r), r.PathValue("name"), request.Server, request.Username, secret); err != nil {
		writeError(w, http.StatusBadRequest, "credential_store_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *V2Handler) createModule(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Key         string `json:"key"`
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Key == "" || request.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "invalid_module", "key and display_name are required")
		return
	}
	value, err := h.store.CreateModule(r.Context(), controlplane.Module{DeveloperID: developerID(r), Key: request.Key, DisplayName: request.DisplayName})
	if err != nil {
		writeError(w, http.StatusConflict, "module_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (h *V2Handler) publishVersion(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPublishBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_multipart", err.Error())
		return
	}
	fields := map[string][]byte{}
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_multipart", nextErr.Error())
			return
		}
		if _, duplicate := fields[part.FormName()]; duplicate {
			writeError(w, http.StatusBadRequest, "invalid_multipart", "duplicate part "+part.FormName())
			return
		}
		limit := int64(1 << 20)
		if part.FormName() == "descriptor_set" {
			limit = controlplane.MaxDescriptorBytes
		}
		value, readErr := readPart(part, limit)
		if readErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_multipart", readErr.Error())
			return
		}
		fields[part.FormName()] = value
	}
	var manifest controlplane.Manifest
	if err = json.Unmarshal(fields["manifest"], &manifest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_manifest", err.Error())
		return
	}
	request := controlplane.PublishRequest{DeveloperID: developerID(r), ModuleID: r.PathValue("module"), ImageRef: string(fields["oci_reference"]), CredentialName: string(fields["registry_credential"]), Manifest: manifest, DescriptorSet: fields["descriptor_set"], Vectors: fields["conformance_vectors"]}
	version, err := h.validator.Publish(r.Context(), request)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, controlplane.ErrVersionConflict) {
			status = http.StatusConflict
		}
		writeError(w, status, "validation_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, version)
}

func (h *V2Handler) getVersion(w http.ResponseWriter, r *http.Request) {
	value, err := h.store.GetVersion(r.Context(), developerID(r), r.PathValue("module"), r.PathValue("version"))
	if errors.Is(err, controlplane.ErrVersionNotFound) {
		writeError(w, http.StatusNotFound, "version_not_found", "module version does not exist")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "version_read_failed", "could not read module version")
		return
	}
	writeJSON(w, http.StatusOK, value)
}
func (h *V2Handler) getValidation(w http.ResponseWriter, r *http.Request) {
	value, err := h.store.GetValidation(r.Context(), developerID(r), r.PathValue("module"), r.PathValue("version"))
	if errors.Is(err, controlplane.ErrVersionNotFound) {
		writeError(w, http.StatusNotFound, "validation_not_found", "validation run does not exist")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "validation_read_failed", "could not read validation")
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *V2Handler) createRoom(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ModuleID string `json:"module_id"`
		Version  string `json:"version,omitempty"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	route, err := h.rooms.CreateRoom(r.Context(), developerID(r), request.ModuleID, request.Version)
	if err != nil {
		writeError(w, http.StatusConflict, "room_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, route)
}
func (h *V2Handler) getRoom(w http.ResponseWriter, r *http.Request) {
	route, err := h.rooms.GetRoom(r.Context(), developerID(r), r.PathValue("room_id"))
	if errors.Is(err, roomcore.ErrRoomNotFound) || errors.Is(err, controlplane.ErrUnauthorized) {
		writeError(w, http.StatusNotFound, "room_not_found", "room does not exist")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "room_read_failed", "could not read room")
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func authenticateV2(r *http.Request, auth APIKeyAuthenticator) (string, error) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return "", errors.New("missing bearer token")
	}
	return auth.AuthenticateDeveloper(r.Context(), strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
}
func readPart(part *multipart.Part, limit int64) ([]byte, error) {
	defer part.Close()
	value, err := io.ReadAll(io.LimitReader(part, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > limit {
		return nil, fmt.Errorf("part %q exceeds %d bytes", part.FormName(), limit)
	}
	return value, nil
}
