package developerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ruleshift/server/internal/game"
	storagepostgres "github.com/Ruleshift/server/internal/storage/postgres"
	"github.com/Ruleshift/server/pkg/ruleshift"
)

type fakeStore struct {
	provisioned game.DatabaseDefinition
	module      ruleshift.Module
	developerID string
}

func (s *fakeStore) AuthenticateDeveloper(_ context.Context, apiKey string) (string, error) {
	if apiKey != "secret" {
		return "", storagepostgres.ErrInvalidDeveloperAPIKey
	}
	return "studio", nil
}

func (s *fakeStore) ProvisionDefinition(_ context.Context, developerID string, displayName string, definition game.DatabaseDefinition) error {
	s.developerID = developerID
	s.provisioned = definition
	s.module = ruleshift.Module{Key: definition.Name, DisplayName: displayName}
	return nil
}

func (s *fakeStore) GetModule(_ context.Context, _, _ string) (ruleshift.Module, error) {
	return s.module, nil
}

func (s *fakeStore) ListModules(_ context.Context, _ string) ([]ruleshift.Module, error) {
	return []ruleshift.Module{s.module}, nil
}

func (s *fakeStore) DescribeModule(_ context.Context, _, moduleKey string) (ruleshift.Schema, error) {
	return ruleshift.Schema{Module: moduleKey}, nil
}

func (s *fakeStore) ListTableRows(_ context.Context, _, moduleKey, table string, limit, offset int) (ruleshift.RowsPage, error) {
	return ruleshift.RowsPage{Module: moduleKey, Table: table, Limit: limit, Offset: offset}, nil
}

func (s *fakeStore) CreateTableRow(_ context.Context, _, moduleKey, table string, values map[string]any) (ruleshift.Row, error) {
	return ruleshift.Row{Module: moduleKey, Table: table, Values: values}, nil
}

func TestHandlerRequiresDeveloperAPIKey(t *testing.T) {
	handler, err := New(&fakeStore{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/developer/modules", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestHandlerCreatesModuleFromDeclarativeSchema(t *testing.T) {
	store := &fakeStore{}
	handler, err := New(store)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	body, _ := json.Marshal(ruleshift.CreateModuleRequest{
		Key:         "inventory",
		DisplayName: "Inventory",
		Schema: ruleshift.ModuleSchema{Tables: []ruleshift.TableDefinition{{
			Name:    "items",
			Columns: []ruleshift.ColumnDefinition{{Name: "id", Type: ruleshift.ColumnTypeString, PrimaryKey: true}},
		}}},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/developer/modules", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s, want 201", response.Code, response.Body.String())
	}
	if store.provisioned.Name != "inventory" || len(store.provisioned.Migrations) != 1 {
		t.Fatalf("provisioned = %#v, want inventory migration", store.provisioned)
	}
	if store.developerID != "studio" {
		t.Fatalf("developer id = %q, want authenticated studio tenant", store.developerID)
	}
}

func TestHandlerBoundsRowsQuery(t *testing.T) {
	handler, err := New(&fakeStore{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/developer/modules/inventory/tables/items/rows?limit=not-a-number", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}
