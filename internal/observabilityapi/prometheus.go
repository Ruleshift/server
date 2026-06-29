package observabilityapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type PrometheusClient struct {
	baseURL string
	client  *http.Client
}

type Sample struct {
	Value     float64
	Timestamp time.Time
	Present   bool
}

func NewPrometheusClient(baseURL string, client *http.Client) *PrometheusClient {
	return &PrometheusClient{baseURL: baseURL, client: client}
}

func (c *PrometheusClient) Query(ctx context.Context, query string) (Sample, error) {
	target := c.baseURL + "/api/v1/query?" + url.Values{"query": []string{query}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Sample{}, err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return Sample{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Sample{}, fmt.Errorf("Prometheus status %d", response.StatusCode)
	}
	var envelope struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err = json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return Sample{}, err
	}
	if envelope.Status != "success" {
		return Sample{}, fmt.Errorf("Prometheus query failed: %s", envelope.Error)
	}
	var pair []json.RawMessage
	switch envelope.Data.ResultType {
	case "vector":
		var values []struct {
			Value []json.RawMessage `json:"value"`
		}
		if err = json.Unmarshal(envelope.Data.Result, &values); err != nil {
			return Sample{}, err
		}
		if len(values) == 0 {
			return Sample{}, nil
		}
		pair = values[0].Value
	case "scalar":
		if err = json.Unmarshal(envelope.Data.Result, &pair); err != nil {
			return Sample{}, err
		}
	default:
		return Sample{}, fmt.Errorf("unsupported Prometheus result type %q", envelope.Data.ResultType)
	}
	if len(pair) != 2 {
		return Sample{}, fmt.Errorf("invalid Prometheus sample")
	}
	var timestamp float64
	var rawValue string
	if err = json.Unmarshal(pair[0], &timestamp); err != nil {
		return Sample{}, err
	}
	if err = json.Unmarshal(pair[1], &rawValue); err != nil {
		return Sample{}, err
	}
	value, err := strconv.ParseFloat(rawValue, 64)
	if err != nil {
		return Sample{}, err
	}
	seconds, fraction := mathModf(timestamp)
	return Sample{Value: value, Timestamp: time.Unix(int64(seconds), int64(fraction*1e9)).UTC(), Present: true}, nil
}

func mathModf(value float64) (float64, float64) {
	whole := float64(int64(value))
	return whole, value - whole
}
