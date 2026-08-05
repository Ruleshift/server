package gamejampromo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDiscoverAfishaIncludesOnlineAndOfflineJams(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = w.Write([]byte("User-agent: *\nAllow: /"))
			return
		}
		_, _ = w.Write([]byte(`<table>
<tr><td><a href="/event/1">Moscow Jam</a></td><td>10.09.2027 → 12.09.2027</td><td>Москва</td><td>Джем</td></tr>
<tr><td><a href="/event/2">Online Jam</a></td><td>20.09.2027</td><td>Онлайн</td><td>Джем</td></tr>
<tr><td><a href="/event/3">Meetup</a></td><td>20.09.2027</td><td>Москва</td><td>Митап</td></tr>
</table>`))
	}))
	defer server.Close()
	value, err := newHTMLSource("gamedev_afisha", server.URL, "RuleshiftGameJamBot/1.0", discoverAfisha)
	if err != nil {
		t.Fatal(err)
	}
	value.client = server.Client()
	items, err := value.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Format != FormatOffline || items[1].Format != FormatOnline || items[1].City != "" {
		t.Fatalf("items = %+v", items)
	}
}

func TestDiscoverJammerParsesJamCards(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = w.Write([]byte("User-agent: *\nAllow: /"))
			return
		}
		_, _ = w.Write([]byte(`<article><a href="/ru/jams/mmcs">MMCS Game Jam</a><span>11.05.2027 – 25.05.2027</span><span>Онлайн</span></article>`))
	}))
	defer server.Close()
	value, err := newHTMLSource("jammer", server.URL+"/ru/jams", "RuleshiftGameJamBot/1.0", discoverJammer)
	if err != nil {
		t.Fatal(err)
	}
	value.client = server.Client()
	items, err := value.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "MMCS Game Jam" || items[0].Format != FormatOnline {
		t.Fatalf("items = %+v", items)
	}
}

func TestRussianClassification(t *testing.T) {
	if relevance, _ := classifyRussian("Онлайн-джем для русскоязычных разработчиков игр из России"); relevance != RelevanceLikely {
		t.Fatalf("relevance = %q", relevance)
	}
	if relevance, _ := classifyRussian("A global game jam for everyone"); relevance != RelevanceUnknown {
		t.Fatalf("relevance = %q", relevance)
	}
	if relevance, _ := classifyRussian("An in-person game jam in Berlin, Germany"); relevance != RelevanceUnlikely {
		t.Fatalf("relevance = %q", relevance)
	}
}

func TestItchDetailRussianTextAndMalformedPage(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken" {
			_, _ = w.Write([]byte(`<html><h1>Broken jam</h1></html>`))
			return
		}
		_, _ = w.Write([]byte(`<html><h1>Русский онлайн-джем</h1><div data-start_date="1925769600" data-end_date="1925942400"></div><p>Онлайн game jam для русскоязычных разработчиков игр из России. Участники создают игры и общаются на русском языке в течение всего события.</p></html>`))
	}))
	defer server.Close()
	value, err := newHTMLSource("itch_io", server.URL, "RuleshiftGameJamBot/1.0", discoverItch)
	if err != nil {
		t.Fatal(err)
	}
	value.client = server.Client()
	jam, err := discoverItchDetail(context.Background(), value, server.URL+"/jam")
	if err != nil || jam.Relevance != RelevanceLikely || jam.Format != FormatOnline || len(jam.Languages) != 1 {
		t.Fatalf("jam = %+v, err = %v", jam, err)
	}
	if _, err := discoverItchDetail(context.Background(), value, server.URL+"/broken"); err == nil {
		t.Fatal("malformed itch page was accepted")
	}
}

func TestDiscoverItchAcceptsSameHostJamLinks(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			_, _ = w.Write([]byte("User-agent: *\nAllow: /"))
		case "/jams/upcoming", "/jams/in-progress":
			_, _ = w.Write([]byte(`<div class="jam_cell"><a href="/jam/russian-jam">Русский джем</a></div>`))
		case "/jam/russian-jam":
			_, _ = w.Write([]byte(`<html><h1>Русский джем</h1><div class="jam_host_header">Hosted by Ruleshift Community</div><div data-start_date="1925769600" data-end_date="1925942400"></div><p>Онлайн-джем для русскоязычных разработчиков игр из России.</p></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	value, err := newHTMLSource("itch_io", server.URL+"/jams/upcoming", "RuleshiftGameJamBot/1.0", discoverItch)
	if err != nil {
		t.Fatal(err)
	}
	value.client = server.Client()
	items, err := value.Discover(context.Background())
	if err != nil || len(items) != 1 || items[0].Organizer != "Ruleshift Community" || items[0].Relevance != RelevanceLikely {
		t.Fatalf("items = %+v, err = %v", items, err)
	}
}

func TestCalendarDatesAndFormats(t *testing.T) {
	start, end, ok := parseCalendarRange("Jam 5.09.2027")
	if !ok || start != end || start.Format(time.DateOnly) != "2027-09-05" {
		t.Fatalf("single date = %v, %v, %v", start, end, ok)
	}
	start, end, ok = parseCalendarRange("Jam 05.09.2027 — 07.09.2027")
	if !ok || start.Format(time.DateOnly) != "2027-09-05" || end.Format(time.DateOnly) != "2027-09-07" {
		t.Fatalf("date range = %v, %v, %v", start, end, ok)
	}
	if _, _, ok := parseCalendarRange("page without a date"); ok {
		t.Fatal("malformed date was accepted")
	}
	if format := inferFormat("online and in-person hybrid jam", "Москва"); format != FormatHybrid {
		t.Fatalf("hybrid format = %q", format)
	}
}

func TestRobotsLongestRuleWins(t *testing.T) {
	body := "User-agent: *\nDisallow: /jams\nAllow: /jams/upcoming\n"
	if !robotsPathAllowed(body, "/jams/upcoming") || robotsPathAllowed(body, "/jams/private") {
		t.Fatalf("unexpected robots decision for %q", strings.TrimSpace(body))
	}
	botOverride := "User-agent: *\nDisallow: /\n\nUser-agent: RuleshiftGameJamBot\nAllow: /jams\n"
	if !robotsPathAllowed(botOverride, "/jams/upcoming") {
		t.Fatal("bot-specific robots group did not override the wildcard group")
	}
}
