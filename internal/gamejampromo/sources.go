package gamejampromo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

const (
	maxSourceBody  = 2 << 20
	maxRobotsBody  = 256 << 10
	maxSourcePages = 20
	maxSourceJams  = 500
)

type SourceConfig struct {
	AfishaURL string
	JammerURL string
	ItchURL   string
	UserAgent string
}

func NewSources(cfg SourceConfig) ([]Source, error) {
	afisha, err := newHTMLSource("gamedev_afisha", cfg.AfishaURL, cfg.UserAgent, discoverAfisha)
	if err != nil {
		return nil, err
	}
	jammer, err := newHTMLSource("jammer", cfg.JammerURL, cfg.UserAgent, discoverJammer)
	if err != nil {
		return nil, err
	}
	itch, err := newHTMLSource("itch_io", cfg.ItchURL, cfg.UserAgent, discoverItch)
	if err != nil {
		return nil, err
	}
	return []Source{afisha, jammer, itch}, nil
}

type sourceDiscoverer func(context.Context, *htmlSource) ([]DiscoveredJam, error)

type htmlSource struct {
	name         string
	base         *url.URL
	client       *http.Client
	userAgent    string
	discover     sourceDiscoverer
	robotsMu     sync.Mutex
	robotsBody   string
	robotsLoaded bool
}

func newHTMLSource(name, rawURL, userAgent string, discover sourceDiscoverer) (*htmlSource, error) {
	base, err := url.Parse(rawURL)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" {
		return nil, fmt.Errorf("invalid HTTPS URL for %s", name)
	}
	if strings.TrimSpace(userAgent) == "" {
		return nil, fmt.Errorf("game jam source user agent is required")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if !sameAllowedHost(base.Hostname(), req.URL.Hostname()) || req.URL.Scheme != "https" {
			return errors.New("source redirect left the allowed HTTPS host")
		}
		return nil
	}
	return &htmlSource{name: name, base: base, client: client, userAgent: userAgent, discover: discover}, nil
}

func (s *htmlSource) Name() string { return s.name }

func (s *htmlSource) Discover(ctx context.Context) ([]DiscoveredJam, error) {
	allowed, err := s.robotsAllows(ctx, s.base.EscapedPath())
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, fmt.Errorf("robots.txt disallows %s", s.base.EscapedPath())
	}
	return s.discover(ctx, s)
}

func (s *htmlSource) fetch(ctx context.Context, target string, limit int64) ([]byte, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse source URL: %w", err)
	}
	if !parsed.IsAbs() {
		parsed = s.base.ResolveReference(parsed)
	}
	if parsed.Scheme != "https" || !sameAllowedHost(s.base.Hostname(), parsed.Hostname()) {
		return nil, fmt.Errorf("source URL is outside allowed HTTPS host")
	}
	allowed, err := s.robotsAllows(ctx, parsed.EscapedPath())
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, fmt.Errorf("robots.txt disallows source path")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", s.userAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch source: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("source returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read source: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("source body exceeds %d bytes", limit)
	}
	return body, nil
}

func (s *htmlSource) robotsAllows(ctx context.Context, path string) (bool, error) {
	s.robotsMu.Lock()
	defer s.robotsMu.Unlock()
	if s.robotsLoaded {
		return robotsPathAllowed(s.robotsBody, path), nil
	}
	robots := *s.base
	robots.Path, robots.RawQuery, robots.Fragment = "/robots.txt", "", ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, robots.String(), nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("User-Agent", s.userAgent)
	response, err := s.client.Do(request)
	if err != nil {
		return false, fmt.Errorf("fetch robots.txt: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		s.robotsLoaded = true
		return true, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("robots.txt returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRobotsBody+1))
	if err != nil || len(body) > maxRobotsBody {
		return false, fmt.Errorf("invalid robots.txt response")
	}
	s.robotsBody, s.robotsLoaded = string(body), true
	return robotsPathAllowed(s.robotsBody, path), nil
}

func sameAllowedHost(base, target string) bool {
	base, target = strings.ToLower(base), strings.ToLower(target)
	return target == base || strings.HasSuffix(target, "."+base) || strings.HasSuffix(base, "."+target)
}

func robotsPathAllowed(body, path string) bool {
	type rule struct {
		agentSpecificity int
		path             string
		allowed          bool
	}
	var rules []rule
	var agents []string
	seenRule := false
	for _, rawLine := range strings.Split(body, "\n") {
		line := strings.TrimSpace(strings.SplitN(rawLine, "#", 2)[0])
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(parts[1])
		switch key {
		case "user-agent":
			if seenRule {
				agents = nil
				seenRule = false
			}
			agents = append(agents, strings.ToLower(value))
		case "allow", "disallow":
			seenRule = true
			if value == "" {
				continue
			}
			specificity := -1
			for _, agent := range agents {
				switch {
				case strings.Contains(agent, "ruleshiftgamejambot"):
					specificity = max(specificity, len("ruleshiftgamejambot"))
				case agent == "*":
					specificity = max(specificity, 0)
				}
			}
			if specificity >= 0 {
				rules = append(rules, rule{agentSpecificity: specificity, path: value, allowed: key == "allow"})
			}
		}
	}
	bestAgent := -1
	for _, candidate := range rules {
		bestAgent = max(bestAgent, candidate.agentSpecificity)
	}
	bestLength, allowed := -1, true
	for _, candidate := range rules {
		if candidate.agentSpecificity != bestAgent || !strings.HasPrefix(path, candidate.path) {
			continue
		}
		if len(candidate.path) > bestLength || len(candidate.path) == bestLength && candidate.allowed {
			bestLength, allowed = len(candidate.path), candidate.allowed
		}
	}
	return allowed
}

func parseDocument(body []byte) (*html.Node, error) {
	return html.Parse(bytes.NewReader(body))
}

func discoverAfisha(ctx context.Context, source *htmlSource) ([]DiscoveredJam, error) {
	body, err := source.fetch(ctx, source.base.String(), maxSourceBody)
	if err != nil {
		return nil, err
	}
	doc, err := parseDocument(body)
	if err != nil {
		return nil, fmt.Errorf("parse afisha HTML: %w", err)
	}
	values := make([]DiscoveredJam, 0)
	walk(doc, func(node *html.Node) {
		if len(values) >= maxSourceJams || node.Type != html.ElementNode || node.Data != "tr" {
			return
		}
		text := compactText(node)
		if !containsFold(text, "Джем") {
			return
		}
		starts, ends, ok := parseCalendarRange(text)
		if !ok {
			return
		}
		link, title := firstLink(node, source.base)
		if link == "" || title == "" {
			return
		}
		cells := childElements(node, "td")
		city := ""
		if len(cells) >= 3 {
			city = strings.TrimSpace(compactText(cells[2]))
		}
		format := inferFormat(text, city)
		values = append(values, DiscoveredJam{ExternalID: link, SourceURL: link, Title: title, Format: format, City: cleanCity(city),
			CountryCode: conditional(format != FormatOnline, "RU", ""), Languages: []string{"ru"}, StartsOn: starts, EndsOn: ends,
			Description: truncate(text, 4096), Relevance: RelevanceLikely, RelevanceNotes: "Russian game-development calendar"})
	})
	return values, nil
}

func discoverJammer(ctx context.Context, source *htmlSource) ([]DiscoveredJam, error) {
	seen := make(map[string]struct{})
	values := make([]DiscoveredJam, 0)
	for page := 1; page <= maxSourcePages && len(values) < maxSourceJams; page++ {
		target := *source.base
		query := target.Query()
		if page > 1 {
			query.Set("page", strconv.Itoa(page))
		}
		target.RawQuery = query.Encode()
		body, err := source.fetch(ctx, target.String(), maxSourceBody)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		doc, err := parseDocument(body)
		if err != nil {
			return nil, err
		}
		before := len(values)
		walk(doc, func(node *html.Node) {
			if len(values) >= maxSourceJams || node.Type != html.ElementNode || node.Data != "a" {
				return
			}
			href := attribute(node, "href")
			if href == "" || (!strings.Contains(href, "/jam/") && !strings.Contains(href, "/jams/")) {
				return
			}
			resolved := source.base.ResolveReference(mustRelativeURL(href)).String()
			if _, exists := seen[resolved]; exists {
				return
			}
			container := nearestContainer(node)
			text := compactText(container)
			starts, ends, ok := parseCalendarRange(text)
			if !ok {
				return
			}
			title := strings.TrimSpace(compactText(node))
			if title == "" {
				return
			}
			seen[resolved] = struct{}{}
			format := inferFormat(text, "")
			values = append(values, DiscoveredJam{ExternalID: resolved, SourceURL: resolved, Title: title, Format: format, Languages: []string{"ru"},
				StartsOn: starts, EndsOn: ends, Description: truncate(text, 4096), Relevance: RelevanceLikely, RelevanceNotes: "Russian-language jam platform"})
		})
		if len(values) == before {
			break
		}
	}
	return values, nil
}

func discoverItch(ctx context.Context, source *htmlSource) ([]DiscoveredJam, error) {
	listingURLs := []string{source.base.String()}
	inProgress := *source.base
	inProgress.Path = strings.TrimSuffix(inProgress.Path, "/upcoming") + "/in-progress"
	listingURLs = append(listingURLs, inProgress.String())
	links := make(map[string]struct{})
	for _, listing := range listingURLs {
		for page := 1; page <= maxSourcePages && len(links) < maxSourceJams; page++ {
			target, _ := url.Parse(listing)
			query := target.Query()
			if page > 1 {
				query.Set("page", strconv.Itoa(page))
			}
			target.RawQuery = query.Encode()
			body, err := source.fetch(ctx, target.String(), maxSourceBody)
			if err != nil {
				if page == 1 {
					return nil, err
				}
				break
			}
			doc, err := parseDocument(body)
			if err != nil {
				return nil, err
			}
			before := len(links)
			walk(doc, func(node *html.Node) {
				if len(links) >= maxSourceJams || node.Type != html.ElementNode || node.Data != "a" {
					return
				}
				href := attribute(node, "href")
				parsed, err := url.Parse(href)
				if err != nil {
					return
				}
				parsed = source.base.ResolveReference(parsed)
				if parsed.Scheme != "https" || !sameAllowedHost(source.base.Hostname(), parsed.Hostname()) {
					return
				}
				if !strings.HasPrefix(parsed.Path, "/jam/") || !hasAncestorClass(node, "jam") {
					return
				}
				parsed.RawQuery, parsed.Fragment = "", ""
				links[parsed.String()] = struct{}{}
			})
			if len(links) == before {
				break
			}
		}
	}
	ordered := make([]string, 0, len(links))
	for link := range links {
		ordered = append(ordered, link)
	}
	sort.Strings(ordered)
	values := make([]DiscoveredJam, len(ordered))
	valid := make([]bool, len(ordered))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(8, len(ordered)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				value, err := discoverItchDetail(ctx, source, ordered[index])
				if err == nil {
					values[index], valid[index] = value, true
				}
			}
		}()
	}
	for index := range ordered {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	result := make([]DiscoveredJam, 0, len(values))
	for index, value := range values {
		if valid[index] {
			result = append(result, value)
		}
	}
	return result, nil
}

func discoverItchDetail(ctx context.Context, source *htmlSource, target string) (DiscoveredJam, error) {
	body, err := source.fetch(ctx, target, maxSourceBody)
	if err != nil {
		return DiscoveredJam{}, err
	}
	doc, err := parseDocument(body)
	if err != nil {
		return DiscoveredJam{}, err
	}
	title := firstHeading(doc)
	if title == "" {
		title = metaContent(doc, "og:title")
	}
	text := compactText(doc)
	starts, ends, ok := parseItchDates(doc, text)
	if !ok || title == "" {
		return DiscoveredJam{}, errors.New("itch jam is missing title or dates")
	}
	relevance, notes := classifyRussian(text)
	languages := []string{}
	if relevance == RelevanceLikely {
		languages = []string{"ru"}
	}
	return DiscoveredJam{ExternalID: target, SourceURL: target, Title: title, Organizer: itchOrganizer(doc), Format: inferFormat(text, ""), Languages: languages,
		StartsOn: starts, EndsOn: ends, Description: truncate(text, 4096), Relevance: relevance, RelevanceNotes: notes}, nil
}

var calendarDatePattern = regexp.MustCompile(`(?i)(0?[1-9]|[12][0-9]|3[01])[.\-/](0?[1-9]|1[0-2])[.\-/](20[0-9]{2})`)

func parseCalendarRange(text string) (time.Time, time.Time, bool) {
	matches := calendarDatePattern.FindAllStringSubmatch(text, 2)
	if len(matches) == 0 {
		return time.Time{}, time.Time{}, false
	}
	parse := func(match []string) time.Time {
		day, _ := strconv.Atoi(match[1])
		month, _ := strconv.Atoi(match[2])
		year, _ := strconv.Atoi(match[3])
		return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	}
	starts := parse(matches[0])
	ends := starts
	if len(matches) > 1 {
		ends = parse(matches[1])
	}
	return starts, ends, !ends.Before(starts)
}

func parseItchDates(doc *html.Node, text string) (time.Time, time.Time, bool) {
	var startRaw, endRaw string
	walk(doc, func(node *html.Node) {
		if startRaw == "" {
			startRaw = attribute(node, "data-start_date")
		}
		if endRaw == "" {
			endRaw = attribute(node, "data-end_date")
		}
	})
	parse := func(value string) time.Time {
		if integer, err := strconv.ParseInt(value, 10, 64); err == nil {
			if integer > 10_000_000_000 {
				integer /= 1000
			}
			return dayUTC(time.Unix(integer, 0))
		}
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return dayUTC(parsed)
		}
		return time.Time{}
	}
	starts, ends := parse(startRaw), parse(endRaw)
	if !starts.IsZero() && !ends.IsZero() && !ends.Before(starts) {
		return starts, ends, true
	}
	iso := regexp.MustCompile(`20[0-9]{2}-[01][0-9]-[0-3][0-9]T[0-9:]+(?:Z|[+-][0-9:]+)`).FindAllString(text, 2)
	if len(iso) >= 2 {
		first, e1 := time.Parse(time.RFC3339, iso[0])
		second, e2 := time.Parse(time.RFC3339, iso[1])
		if e1 == nil && e2 == nil {
			return dayUTC(first), dayUTC(second), true
		}
	}
	plain := regexp.MustCompile(`20[0-9]{2}-[01][0-9]-[0-3][0-9] [0-9]{2}:[0-9]{2}:[0-9]{2}`).FindAllString(text, 2)
	if len(plain) >= 2 {
		first, e1 := time.Parse("2006-01-02 15:04:05", plain[0])
		second, e2 := time.Parse("2006-01-02 15:04:05", plain[1])
		if e1 == nil && e2 == nil {
			return dayUTC(first), dayUTC(second), true
		}
	}
	return parseCalendarRange(text)
}

func itchOrganizer(doc *html.Node) string {
	organizer := ""
	walk(doc, func(node *html.Node) {
		if organizer != "" || node.Type != html.ElementNode {
			return
		}
		className := strings.ToLower(attribute(node, "class"))
		if !strings.Contains(className, "jam_host") {
			return
		}
		text := strings.TrimSpace(compactText(node))
		lower := strings.ToLower(text)
		if index := strings.Index(lower, "hosted by"); index >= 0 {
			organizer = truncate(strings.TrimSpace(text[index+len("hosted by"):]), 300)
		}
	})
	return organizer
}

func classifyRussian(text string) (Relevance, string) {
	lower := strings.ToLower(text)
	explicit := []string{"russian", "russia", "русский", "русскоязыч", "россия", "москва", "санкт-петербург", "екатеринбург", "новосибирск", "казань"}
	for _, marker := range explicit {
		if strings.Contains(lower, marker) {
			return RelevanceLikely, "explicit Russian language, audience, or location marker"
		}
	}
	cyrillic := 0
	for _, char := range text {
		if unicode.In(char, unicode.Cyrillic) {
			cyrillic++
		}
	}
	if cyrillic >= 80 && (strings.Contains(lower, " для ") || strings.Contains(lower, " игра") || strings.Contains(lower, " участ")) {
		return RelevanceLikely, "substantial Russian-language text"
	}
	foreignAudience := []string{
		"united states", "united kingdom", "canada only", "france", "germany",
		"spain", "italy", "india", "japan", "brazil", "australia",
	}
	for _, marker := range foreignAudience {
		if strings.Contains(lower, marker) {
			return RelevanceUnlikely, "explicit non-Russian location or audience marker"
		}
	}
	return RelevanceUnknown, "no reliable Russian audience marker"
}

func inferFormat(text, city string) Format {
	lower := strings.ToLower(text + " " + city)
	online := strings.Contains(lower, "онлайн") || strings.Contains(lower, "online") || strings.Contains(lower, "virtual")
	offline := city != "" && !strings.EqualFold(strings.TrimSpace(city), "онлайн") || strings.Contains(lower, "offline") || strings.Contains(lower, "офлайн") || strings.Contains(lower, "in-person")
	if online && offline {
		return FormatHybrid
	}
	if online {
		return FormatOnline
	}
	if offline {
		return FormatOffline
	}
	return FormatUnknown
}

func cleanCity(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "онлайн") || strings.EqualFold(value, "online") {
		return ""
	}
	return value
}

func conditional[T any](condition bool, yes, no T) T {
	if condition {
		return yes
	}
	return no
}

func walk(node *html.Node, visit func(*html.Node)) {
	if node == nil {
		return
	}
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walk(child, visit)
	}
}

func compactText(node *html.Node) string {
	var builder strings.Builder
	walk(node, func(current *html.Node) {
		if current.Type == html.TextNode {
			builder.WriteByte(' ')
			builder.WriteString(current.Data)
		}
	})
	return strings.Join(strings.Fields(builder.String()), " ")
}

func attribute(node *html.Node, key string) string {
	if node == nil {
		return ""
	}
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func childElements(node *html.Node, name string) []*html.Node {
	values := make([]*html.Node, 0)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == name {
			values = append(values, child)
		}
	}
	return values
}

func firstLink(node *html.Node, base *url.URL) (string, string) {
	var link, title string
	walk(node, func(current *html.Node) {
		if link != "" || current.Type != html.ElementNode || current.Data != "a" {
			return
		}
		href := attribute(current, "href")
		if href == "" {
			return
		}
		resolved := base.ResolveReference(mustRelativeURL(href))
		if resolved.Scheme != "https" {
			return
		}
		link, title = resolved.String(), strings.TrimSpace(compactText(current))
	})
	return link, title
}

func mustRelativeURL(raw string) *url.URL {
	value, _ := url.Parse(raw)
	return value
}

func nearestContainer(node *html.Node) *html.Node {
	for current := node; current != nil; current = current.Parent {
		if current.Type == html.ElementNode && (current.Data == "article" || current.Data == "li" || current.Data == "div") {
			return current
		}
	}
	return node
}

func containsFold(value, target string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(target))
}

func hasAncestorClass(node *html.Node, className string) bool {
	for current := node; current != nil; current = current.Parent {
		for _, token := range strings.Fields(attribute(current, "class")) {
			if token == className || strings.Contains(token, className) {
				return true
			}
		}
	}
	return false
}

func firstHeading(node *html.Node) string {
	var value string
	walk(node, func(current *html.Node) {
		if value == "" && current.Type == html.ElementNode && current.Data == "h1" {
			value = strings.TrimSpace(compactText(current))
		}
	})
	return value
}

func metaContent(node *html.Node, property string) string {
	var value string
	walk(node, func(current *html.Node) {
		if value == "" && current.Type == html.ElementNode && current.Data == "meta" && attribute(current, "property") == property {
			value = attribute(current, "content")
		}
	})
	return value
}
