package metno_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tamnd/metno-cli/metno"
)

// forecastFixture is minimal valid JSON matching wireForecast.
const forecastFixture = `{
  "type": "Feature",
  "geometry": {"type": "Point", "coordinates": [10.75, 59.91, 14]},
  "properties": {
    "meta": {"updated_at": "2026-06-14T16:28:20Z", "units": {}},
    "timeseries": [
      {
        "time": "2026-06-14T16:00:00Z",
        "data": {
          "instant": {"details": {
            "air_temperature": 19.8,
            "wind_speed": 3.6,
            "wind_from_direction": 225.5,
            "relative_humidity": 65.1,
            "cloud_area_fraction": 100.0
          }},
          "next_1_hours": {
            "summary": {"symbol_code": "cloudy"},
            "details": {"precipitation_amount": 0.0}
          },
          "next_6_hours": {
            "summary": {"symbol_code": "cloudy"},
            "details": {"precipitation_amount": 0.0}
          }
        }
      },
      {
        "time": "2026-06-14T17:00:00Z",
        "data": {
          "instant": {"details": {
            "air_temperature": 18.5,
            "wind_speed": 4.1,
            "wind_from_direction": 230.0,
            "relative_humidity": 70.0,
            "cloud_area_fraction": 80.0
          }},
          "next_6_hours": {
            "summary": {"symbol_code": "rain"},
            "details": {"precipitation_amount": 2.0}
          }
        }
      },
      {
        "time": "2026-06-14T18:00:00Z",
        "data": {
          "instant": {"details": {
            "air_temperature": 17.0,
            "wind_speed": 5.0,
            "wind_from_direction": 240.0,
            "relative_humidity": 75.0,
            "cloud_area_fraction": 90.0
          }},
          "next_1_hours": {
            "summary": {"symbol_code": "heavyrain"},
            "details": {"precipitation_amount": 3.5}
          }
        }
      }
    ]
  }
}`

func newTestServer(t *testing.T, body string) (*httptest.Server, *metno.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carried no User-Agent header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	cfg := metno.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0 // no pacing in tests
	return srv, metno.NewClient(cfg)
}

func TestForecastBasic(t *testing.T) {
	srv, c := newTestServer(t, forecastFixture)
	defer srv.Close()

	points, err := c.Forecast(context.Background(), 59.91, 10.75, 0)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("got %d points, want 3", len(points))
	}
	if points[0].Temperature != 19.8 {
		t.Errorf("points[0].Temperature = %v, want 19.8", points[0].Temperature)
	}
	if points[0].Symbol != "cloudy" {
		t.Errorf("points[0].Symbol = %q, want cloudy", points[0].Symbol)
	}
}

func TestForecastLimit(t *testing.T) {
	srv, c := newTestServer(t, forecastFixture)
	defer srv.Close()

	points, err := c.Forecast(context.Background(), 59.91, 10.75, 2)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if len(points) != 2 {
		t.Errorf("got %d points with limit=2, want 2", len(points))
	}
}

func TestForecastSymbolFallback(t *testing.T) {
	// second entry has no next_1_hours, only next_6_hours
	srv, c := newTestServer(t, forecastFixture)
	defer srv.Close()

	points, err := c.Forecast(context.Background(), 59.91, 10.75, 0)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if points[1].Symbol != "rain" {
		t.Errorf("points[1].Symbol = %q, want rain (from next_6_hours fallback)", points[1].Symbol)
	}
	if points[1].Precip1h != 0 {
		t.Errorf("points[1].Precip1h = %v, want 0 (no next_1_hours)", points[1].Precip1h)
	}
}

func TestForecastNext1HoursPrecip(t *testing.T) {
	srv, c := newTestServer(t, forecastFixture)
	defer srv.Close()

	points, err := c.Forecast(context.Background(), 59.91, 10.75, 0)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	// third entry has next_1_hours with precip 3.5
	if points[2].Precip1h != 3.5 {
		t.Errorf("points[2].Precip1h = %v, want 3.5", points[2].Precip1h)
	}
	if points[2].Symbol != "heavyrain" {
		t.Errorf("points[2].Symbol = %q, want heavyrain", points[2].Symbol)
	}
}

func TestForecastUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(forecastFixture))
	}))
	defer srv.Close()

	cfg := metno.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	c := metno.NewClient(cfg)

	_, err := c.Forecast(context.Background(), 59.91, 10.75, 1)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if gotUA != "metno-cli/0.1 tamnd87@gmail.com" {
		t.Errorf("User-Agent = %q, want metno-cli/0.1 tamnd87@gmail.com", gotUA)
	}
}

func TestForecastRetryOn503(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(forecastFixture))
	}))
	defer srv.Close()

	cfg := metno.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	cfg.Retries = 5
	c := metno.NewClient(cfg)

	points, err := c.Forecast(context.Background(), 59.91, 10.75, 1)
	if err != nil {
		t.Fatalf("Forecast after retries: %v", err)
	}
	if len(points) == 0 {
		t.Error("expected at least one point")
	}
	if hits != 3 {
		t.Errorf("server saw %d hits, want 3", hits)
	}
}

func TestForecastDecodeFields(t *testing.T) {
	srv, c := newTestServer(t, forecastFixture)
	defer srv.Close()

	points, err := c.Forecast(context.Background(), 59.91, 10.75, 0)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	p := points[0]

	// Verify all fields are populated
	if p.Time == "" {
		t.Error("Time is empty")
	}
	if p.WindSpeed == 0 {
		t.Error("WindSpeed is zero")
	}
	if p.WindDir == 0 {
		t.Error("WindDir is zero")
	}
	if p.Humidity == 0 {
		t.Error("Humidity is zero")
	}
	if p.CloudCover == 0 {
		t.Error("CloudCover is zero")
	}

	// Verify JSON round-trip
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var p2 metno.WeatherPoint
	if err := json.Unmarshal(b, &p2); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if p2.Temperature != p.Temperature {
		t.Errorf("round-trip Temperature = %v, want %v", p2.Temperature, p.Temperature)
	}
}
