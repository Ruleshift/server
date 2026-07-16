package observabilityapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	prometheusapi "github.com/prometheus/client_golang/api"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

type PrometheusClient struct{ api prometheusv1.API }

type Sample struct {
	Value     float64
	Timestamp time.Time
	Present   bool
}

func NewPrometheusClient(baseURL string, client *http.Client) (*PrometheusClient, error) {
	apiClient, err := prometheusapi.NewClient(prometheusapi.Config{
		Address: strings.TrimRight(baseURL, "/"),
		Client:  client,
	})
	if err != nil {
		return nil, fmt.Errorf("create Prometheus API client: %w", err)
	}
	return &PrometheusClient{api: prometheusv1.NewAPI(apiClient)}, nil
}

func (c *PrometheusClient) Query(ctx context.Context, query string) (Sample, error) {
	value, _, err := c.api.Query(ctx, query, time.Now())
	if err != nil {
		return Sample{}, err
	}
	if value == nil {
		return Sample{}, fmt.Errorf("Prometheus query returned no result")
	}
	switch result := value.(type) {
	case model.Vector:
		if len(result) == 0 {
			return Sample{}, nil
		}
		if result[0].Histogram != nil {
			return Sample{}, fmt.Errorf("Prometheus query returned a histogram sample")
		}
		return Sample{Value: float64(result[0].Value), Timestamp: result[0].Timestamp.Time().UTC(), Present: true}, nil
	case *model.Scalar:
		return Sample{Value: float64(result.Value), Timestamp: result.Timestamp.Time().UTC(), Present: true}, nil
	default:
		return Sample{}, fmt.Errorf("unsupported Prometheus result type %s", value.Type())
	}
}
