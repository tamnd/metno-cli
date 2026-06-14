package metno

import (
	"testing"

	"github.com/tamnd/any-cli/kit"
)

// These tests are offline: they exercise the URI driver's pure string functions
// and the host wiring (mint, body, resolve), which need no network.
// The client's HTTP behaviour is covered in metno_test.go.

func TestDomainInfo(t *testing.T) {
	info := Domain{}.Info()
	if info.Scheme != "metno" {
		t.Errorf("Scheme = %q, want metno", info.Scheme)
	}
	if len(info.Hosts) == 0 || info.Hosts[0] != Host {
		t.Errorf("Hosts = %v, want [%s]", info.Hosts, Host)
	}
	if info.Identity.Binary != "metno" {
		t.Errorf("Identity.Binary = %q, want metno", info.Identity.Binary)
	}
}

func TestClassifyLocation(t *testing.T) {
	cases := []struct {
		in      string
		wantTyp string
		wantID  string
	}{
		{"59.91,10.75", "location", "59.91,10.75"},
		{"0,0", "location", "0,0"},
		{"-33.87,151.21", "location", "-33.87,151.21"},
	}
	for _, tc := range cases {
		typ, id, err := Domain{}.Classify(tc.in)
		if err != nil {
			t.Errorf("Classify(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if typ != tc.wantTyp || id != tc.wantID {
			t.Errorf("Classify(%q) = (%q, %q), want (%q, %q)",
				tc.in, typ, id, tc.wantTyp, tc.wantID)
		}
	}
}

func TestClassifyQuery(t *testing.T) {
	cases := []string{"Oslo", "New York", "Tokyo"}
	for _, tc := range cases {
		typ, id, err := Domain{}.Classify(tc)
		if err != nil {
			t.Errorf("Classify(%q) unexpected error: %v", tc, err)
			continue
		}
		if typ != "query" {
			t.Errorf("Classify(%q).type = %q, want query", tc, typ)
		}
		if id != tc {
			t.Errorf("Classify(%q).id = %q, want %q", tc, id, tc)
		}
	}
}

func TestClassifyEmptyError(t *testing.T) {
	_, _, err := Domain{}.Classify("")
	if err == nil {
		t.Error("Classify(\"\") expected error, got nil")
	}
}

func TestLocateLocation(t *testing.T) {
	got, err := Domain{}.Locate("location", "59.91,10.75")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	want := "https://www.yr.no/nb/forecast/daily-table/59.91,10.75"
	if got != want {
		t.Errorf("Locate = %q, want %q", got, want)
	}
}

func TestLocateQuery(t *testing.T) {
	got, err := Domain{}.Locate("query", "Oslo")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if got == "" {
		t.Error("Locate returned empty URL")
	}
}

func TestLocateUnknownType(t *testing.T) {
	_, err := Domain{}.Locate("unknown", "foo")
	if err == nil {
		t.Error("Locate with unknown type expected error, got nil")
	}
}

func TestHostWiring(t *testing.T) {
	h, err := kit.Open()
	if err != nil {
		t.Fatal(err)
	}

	// Classify a lat,lon pair and resolve to a URI via the domain.
	got, err := h.ResolveOn("metno", "59.91,10.75")
	if err != nil {
		t.Fatalf("ResolveOn: %v", err)
	}
	// The kit host URL-encodes the comma in the path segment.
	want1 := "metno://location/59.91,10.75"
	want2 := "metno://location/59.91%2C10.75"
	if got.String() != want1 && got.String() != want2 {
		t.Errorf("ResolveOn = %q, want %q or %q", got.String(), want1, want2)
	}
}

func TestToWeatherPoint(t *testing.T) {
	ts := wireTimeseries{Time: "2026-06-14T16:00:00Z"}
	ts.Data.Instant.Details.AirTemperature = 19.8
	ts.Data.Instant.Details.WindSpeed = 3.6
	ts.Data.Instant.Details.WindFromDirection = 225.5
	ts.Data.Instant.Details.RelativeHumidity = 65.1
	ts.Data.Instant.Details.CloudAreaFraction = 100.0
	next1 := struct {
		Summary struct {
			SymbolCode string `json:"symbol_code"`
		} `json:"summary"`
		Details struct {
			PrecipitationAmount float64 `json:"precipitation_amount"`
		} `json:"details"`
	}{}
	next1.Summary.SymbolCode = "cloudy"
	next1.Details.PrecipitationAmount = 0.5
	ts.Data.Next1Hours = &next1

	wp := toWeatherPoint(ts)
	if wp.Time != "2026-06-14T16:00:00Z" {
		t.Errorf("Time = %q, want 2026-06-14T16:00:00Z", wp.Time)
	}
	if wp.Temperature != 19.8 {
		t.Errorf("Temperature = %v, want 19.8", wp.Temperature)
	}
	if wp.Symbol != "cloudy" {
		t.Errorf("Symbol = %q, want cloudy", wp.Symbol)
	}
	if wp.Precip1h != 0.5 {
		t.Errorf("Precip1h = %v, want 0.5", wp.Precip1h)
	}
}

func TestToWeatherPointFallbackSymbol(t *testing.T) {
	ts := wireTimeseries{Time: "2026-06-14T22:00:00Z"}
	// no Next1Hours, only Next6Hours
	next6 := struct {
		Summary struct {
			SymbolCode string `json:"symbol_code"`
		} `json:"summary"`
		Details struct {
			PrecipitationAmount float64 `json:"precipitation_amount"`
		} `json:"details"`
	}{}
	next6.Summary.SymbolCode = "rain"
	ts.Data.Next6Hours = &next6

	wp := toWeatherPoint(ts)
	if wp.Symbol != "rain" {
		t.Errorf("Symbol = %q, want rain (fallback from next_6_hours)", wp.Symbol)
	}
	if wp.Precip1h != 0 {
		t.Errorf("Precip1h = %v, want 0 (no next_1_hours)", wp.Precip1h)
	}
}
