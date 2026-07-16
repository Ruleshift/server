package ruleshift

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-resty/resty/v2"
)

const maxResponseBytes = 4 << 20

type Client struct {
	http *resty.Client
}

func (c *Client) PublishModuleVersion(ctx context.Context, request PublishModuleVersionRequest) (ModuleVersion, error) {
	manifest, err := json.Marshal(request.Manifest)
	if err != nil {
		return ModuleVersion{}, fmt.Errorf("encode module manifest: %w", err)
	}
	fields := []*resty.MultipartField{
		{Param: "manifest", Reader: bytes.NewReader(manifest)},
		{Param: "descriptor_set", Reader: bytes.NewReader(request.DescriptorSet)},
		{Param: "conformance_vectors", Reader: bytes.NewReader(request.ConformanceVectors)},
		{Param: "oci_reference", Reader: strings.NewReader(request.OCIReference)},
	}
	if request.RegistryCredential != "" {
		fields = append(fields, &resty.MultipartField{Param: "registry_credential", Reader: strings.NewReader(request.RegistryCredential)})
	}
	path := "/v2/developer/modules/" + url.PathEscape(request.ModuleID) + "/versions"
	var version ModuleVersion
	err = c.execute(c.request(ctx, &version).SetMultipartFields(fields...), http.MethodPost, path)
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
		defaultClient := *http.DefaultClient
		httpClient = &defaultClient
	}
	client := resty.NewWithClient(httpClient).
		SetBaseURL(baseURL).
		SetAuthToken(apiKey).
		SetHeader("Accept", "application/json").
		SetResponseBodyLimit(maxResponseBytes).
		SetJSONUnmarshaler(func(data []byte, value any) error {
			if len(data) == 0 {
				return nil
			}
			return json.Unmarshal(data, value)
		})
	return &Client{http: client}, nil
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
	var page RowsPage
	err := c.execute(c.request(ctx, &page).SetQueryParamsFromValues(query), http.MethodGet, path)
	return page, err
}

func (c *Client) CreateRow(ctx context.Context, moduleKey, table string, values map[string]any) (Row, error) {
	path := "/v2/developer/modules/" + url.PathEscape(moduleKey) + "/tables/" + url.PathEscape(table) + "/rows"
	var row Row
	err := c.do(ctx, http.MethodPost, path, CreateRowRequest{Values: values}, &row)
	return row, err
}

func (c *Client) do(ctx context.Context, method, path string, requestBody any, responseBody any) error {
	request := c.request(ctx, responseBody)
	if requestBody != nil {
		request.SetHeader("Content-Type", "application/json").SetBody(requestBody)
	}
	return c.execute(request, method, path)
}

func (c *Client) request(ctx context.Context, result any) *resty.Request {
	request := c.http.R().SetContext(ctx).ForceContentType("application/json")
	if result != nil {
		request.SetResult(result)
	}
	return request
}

func (c *Client) execute(request *resty.Request, method, path string) error {
	response, err := request.Execute(method, path)
	if errors.Is(err, resty.ErrResponseBodyTooLarge) {
		return fmt.Errorf("ruleshift response exceeds %d bytes", maxResponseBytes)
	}
	if err != nil {
		if response != nil && response.IsSuccess() {
			return fmt.Errorf("decode ruleshift response: %w", err)
		}
		return fmt.Errorf("call ruleshift API: %w", err)
	}
	if response.IsSuccess() {
		return nil
	}
	apiErr := &APIError{StatusCode: response.StatusCode(), Code: "request_failed", Message: http.StatusText(response.StatusCode())}
	_ = json.Unmarshal(response.Body(), apiErr)
	return apiErr
}
