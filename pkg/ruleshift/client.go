package ruleshift

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func (c *Client) CreateModule(ctx context.Context, request CreateModuleRequest) (Module, error) {
	var module Module
	err := c.do(ctx, http.MethodPost, "/v1/developer/modules", request, &module)
	return module, err
}

func (c *Client) ListModules(ctx context.Context) ([]Module, error) {
	var response struct {
		Modules []Module `json:"modules"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/developer/modules", nil, &response)
	return response.Modules, err
}

func (c *Client) GetSchema(ctx context.Context, moduleKey string) (Schema, error) {
	var schema Schema
	err := c.do(ctx, http.MethodGet, "/v1/developer/modules/"+url.PathEscape(moduleKey)+"/schema", nil, &schema)
	return schema, err
}

func (c *Client) ListRows(ctx context.Context, moduleKey, table string, limit, offset int) (RowsPage, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	path := "/v1/developer/modules/" + url.PathEscape(moduleKey) + "/tables/" + url.PathEscape(table) + "/rows"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var page RowsPage
	err := c.do(ctx, http.MethodGet, path, nil, &page)
	return page, err
}

func (c *Client) CreateRow(ctx context.Context, moduleKey, table string, values map[string]any) (Row, error) {
	path := "/v1/developer/modules/" + url.PathEscape(moduleKey) + "/tables/" + url.PathEscape(table) + "/rows"
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
