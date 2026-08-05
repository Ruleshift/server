package gamejampromo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type Discovery struct {
	store   *Store
	sources []Source
	metrics *Metrics
	logger  *slog.Logger
	now     func() time.Time
}

func NewDiscovery(store *Store, sources []Source, metrics *Metrics, logger *slog.Logger) *Discovery {
	return &Discovery{store: store, sources: sources, metrics: metrics, logger: logger, now: time.Now}
}

func (d *Discovery) Run(ctx context.Context) error {
	err := d.store.WithDiscoveryLock(ctx, func(ctx context.Context) error {
		var failures []string
		for _, source := range d.sources {
			if err := d.runSource(ctx, source); err != nil {
				failures = append(failures, source.Name())
				d.logger.ErrorContext(ctx, "game jam source failed", "source", source.Name(), "reason", safeDiscoveryError(err))
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("discovery failed for sources: %s", strings.Join(failures, ", "))
		}
		return nil
	})
	if errors.Is(err, ErrDiscoveryBusy) {
		startedAt := d.now().UTC()
		for _, source := range d.sources {
			if runID, startErr := d.store.StartRun(ctx, source.Name(), startedAt); startErr == nil {
				_ = d.store.FinishRun(ctx, runID, "busy", 0, "another discovery run holds the advisory lock")
			}
			d.metrics.ObserveDiscovery(source.Name(), "busy", d.now())
		}
		return err
	}
	return err
}

func (d *Discovery) runSource(ctx context.Context, source Source) error {
	startedAt := d.now().UTC()
	runID, err := d.store.StartRun(ctx, source.Name(), startedAt)
	if err != nil {
		return err
	}
	values, discoverErr := source.Discover(ctx)
	if discoverErr != nil {
		_ = d.store.FinishRun(ctx, runID, "error", 0, safeDiscoveryError(discoverErr))
		d.metrics.ObserveDiscovery(source.Name(), "error", d.now())
		return discoverErr
	}
	count := 0
	for _, value := range values {
		value.Source = source.Name()
		if err := normalizeDiscovered(&value); err != nil {
			d.logger.WarnContext(ctx, "discarding malformed game jam candidate", "source", source.Name(), "reason", err.Error())
			continue
		}
		if value.EndsOn.Before(dayUTC(startedAt)) {
			continue
		}
		if err := d.store.UpsertCandidate(ctx, value, startedAt); err != nil {
			_ = d.store.FinishRun(ctx, runID, "error", count, err.Error())
			d.metrics.ObserveDiscovery(source.Name(), "error", d.now())
			return err
		}
		count++
	}
	if err := d.store.MarkMissing(ctx, source.Name(), startedAt); err != nil {
		_ = d.store.FinishRun(ctx, runID, "error", count, err.Error())
		return err
	}
	if err := d.store.FinishRun(ctx, runID, "success", count, ""); err != nil {
		return err
	}
	d.metrics.ObserveDiscovery(source.Name(), "success", d.now())
	if pending, err := d.store.CountPending(ctx); err == nil {
		d.metrics.SetPending(pending)
	}
	d.logger.InfoContext(ctx, "game jam source synchronized", "source", source.Name(), "candidates", count)
	return nil
}

func safeDiscoveryError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "source request timed out"
	case errors.Is(err, context.Canceled):
		return "source request canceled"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "robots.txt"):
		return "robots.txt prevented source synchronization"
	case strings.Contains(message, "too many redirects"), strings.Contains(message, "redirect left"):
		return "source redirect policy rejected the response"
	case strings.Contains(message, "exceeds"):
		return "source response exceeded a configured limit"
	case strings.Contains(message, "http "):
		return "source returned an unsuccessful HTTP status"
	case strings.Contains(message, "parse") || strings.Contains(message, "missing title or dates"):
		return "source page could not be parsed"
	default:
		return "source synchronization failed"
	}
}

func normalizeDiscovered(value *DiscoveredJam) error {
	value.ExternalID = truncate(value.ExternalID, 512)
	value.SourceURL = truncate(value.SourceURL, 2048)
	value.OfficialURL = truncate(value.OfficialURL, 2048)
	value.Title = truncate(value.Title, 300)
	value.Organizer = truncate(value.Organizer, 300)
	value.City = truncate(value.City, 200)
	value.CountryCode = strings.ToUpper(truncate(value.CountryCode, 2))
	value.Description = truncate(value.Description, 4096)
	value.RelevanceNotes = truncate(value.RelevanceNotes, 512)
	if value.ExternalID == "" {
		value.ExternalID = value.SourceURL
	}
	if value.ExternalID == "" || value.SourceURL == "" || value.Title == "" {
		return fmt.Errorf("missing identity, URL, or title")
	}
	if !validFormat(value.Format) || !validRelevance(value.Relevance) || value.StartsOn.IsZero() || value.EndsOn.Before(value.StartsOn) {
		return fmt.Errorf("invalid format, relevance, or dates")
	}
	if len(value.Languages) > 16 {
		value.Languages = value.Languages[:16]
	}
	return nil
}

func dayUTC(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
