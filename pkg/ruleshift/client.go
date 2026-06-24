package ruleshift

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maxResponseBytes = 4 << 20

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func (c *Client) PublishModuleVersion(ctx context.Context, request PublishModuleVersionRequest) (ModuleVersion, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	manifest, err := json.Marshal(request.Manifest)
	if err != nil {
		return ModuleVersion{}, fmt.Errorf("encode module manifest: %w", err)
	}
	parts := []struct {
		name  string
		value []byte
	}{{"manifest", manifest}, {"descriptor_set", request.DescriptorSet}, {"conformance_vectors", request.ConformanceVectors}, {"oci_reference", []byte(request.OCIReference)}}
	if request.RegistryCredential != "" {
		parts = append(parts, struct {
			name  string
			value []byte
		}{"registry_credential", []byte(request.RegistryCredential)})
	}
	for _, part := range parts {
		field, createErr := writer.CreateFormField(part.name)
		if createErr != nil {
			return ModuleVersion{}, createErr
		}
		if _, createErr = field.Write(part.value); createErr != nil {
			return ModuleVersion{}, createErr
		}
	}
	if err = writer.Close(); err != nil {
		return ModuleVersion{}, err
	}
	path := "/v2/developer/modules/" + url.PathEscape(request.ModuleID) + "/versions"
	var version ModuleVersion
	err = c.doReader(ctx, http.MethodPost, path, &body, writer.FormDataContentType(), &version)
	return version, err
}

func (c *Client) CreateRuntimeModule(ctx context.Context, key, displayName string) (RuntimeModule, error) {
	var result RuntimeModule
	err := c.do(ctx, http.MethodPost, "/v2/developer/modules", map[string]string{"key": key, "display_name": displayName}, &result)
	return result, err
}

func (c *Client) GetModuleVersion(ctx context.Context, moduleID, version string) (ModuleVersion, error) {
	var result ModuleVersion
	err := c.do(ctx, http.MethodGet, "/v2/developer/modules/"+url.PathEscape(moduleID)+"/versions/"+url.PathEscape(version), nil, &result)
	return result, err
}
func (c *Client) GetValidationStatus(ctx context.Context, moduleID, version string) (ValidationStatus, error) {
	var result ValidationStatus
	err := c.do(ctx, http.MethodGet, "/v2/developer/modules/"+url.PathEscape(moduleID)+"/versions/"+url.PathEscape(version)+"/validation", nil, &result)
	return result, err
}
func (c *Client) CreateRoom(ctx context.Context, request CreateRoomRequest) (Room, error) {
	var room Room
	err := c.do(ctx, http.MethodPost, "/v2/rooms", request, &room)
	return room, err
}
func (c *Client) GetRoom(ctx context.Context, roomID string) (Room, error) {
	var room Room
	err := c.do(ctx, http.MethodGet, "/v2/rooms/"+url.PathEscape(roomID), nil, &room)
	return room, err
}

type APIError struct {
	StatusCode int
	Code       string `json:"code"`
	Message    string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("ruleshift API error (%d/%s): %s", e.StatusCode, e.Code, e.Message)
}

func NewClient(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("ruleshift base URL must not be empty")
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("parse ruleshift base URL: %w", err)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("ruleshift developer API key must not be empty")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, apiKey: apiKey, httpClient: httpClient}, nil
}

func (c *Client) ListRows(ctx context.Context, moduleKey, table string, limit, offset int) (RowsPage, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	path := "/v2/developer/modules/" + url.PathEscape(moduleKey) + "/tables/" + url.PathEscape(table) + "/rows"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var page RowsPage
	err := c.do(ctx, http.MethodGet, path, nil, &page)
	return page, err
}

func (c *Client) CreateRow(ctx context.Context, moduleKey, table string, values map[string]any) (Row, error) {
	path := "/v2/developer/modules/" + url.PathEscape(moduleKey) + "/tables/" + url.PathEscape(table) + "/rows"
	var row Row
	err := c.do(ctx, http.MethodPost, path, CreateRowRequest{Values: values}, &row)
	return row, err
}

func (c *Client) do(ctx context.Context, method, path string, requestBody any, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode ruleshift request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create ruleshift request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("call ruleshift API: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read ruleshift response: %w", err)
	}
	if len(payload) > maxResponseBytes {
		return fmt.Errorf("ruleshift response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		apiErr := &APIError{StatusCode: response.StatusCode, Code: "request_failed", Message: http.StatusText(response.StatusCode)}
		_ = json.Unmarshal(payload, apiErr)
		return apiErr
	}
	if responseBody == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, responseBody); err != nil {
		return fmt.Errorf("decode ruleshift response: %w", err)
	}
	return nil
}

func (c *Client) doReader(ctx context.Context, method, path string, body io.Reader, contentType string, responseBody any) error {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create ruleshift request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", contentType)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("call ruleshift API: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxResponseBytes {
		return fmt.Errorf("ruleshift response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		apiErr := &APIError{StatusCode: response.StatusCode, Code: "request_failed", Message: http.StatusText(response.StatusCode)}
		_ = json.Unmarshal(payload, apiErr)
		return apiErr
	}
	if responseBody != nil && len(payload) > 0 {
		return json.Unmarshal(payload, responseBody)
	}
	return nil
}
