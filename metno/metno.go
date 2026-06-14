// Package metno is the library behind the metno command line:
// the HTTP client, request shaping, and typed data models for the
// Norwegian Meteorological Institute weather API (api.met.no).
//
// Usage policy: every request MUST carry a meaningful User-Agent header
// identifying the application and a contact email. The API blocks requests
// without a proper User-Agent. A rate of 1 req/s is polite and appropriate.
package metno

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Host is the API host this client talks to.
const Host = "api.met.no"

// Config holds all tunable parameters for the Client.
type Config struct {
	BaseURL   string
	UserAgent string
	Rate      time.Duration
	Timeout   time.Duration
	Retries   int
}

// DefaultConfig returns sensible defaults: 1 req/s rate limit, 30s timeout,
// 3 retries, and the required User-Agent identifying this tool and contact.
func DefaultConfig() Config {
	return Config{
		BaseURL:   "https://api.met.no",
		UserAgent: "metno-cli/0.1 tamnd87@gmail.com",
		Rate:      time.Second,
		Retries:   3,
		Timeout:   30 * time.Second,
	}
}

// Client talks to api.met.no over HTTP.
type Client struct {
	cfg  Config
	http *http.Client
	mu   sync.Mutex
	last time.Time
}

// NewClient returns a Client configured with cfg.
func NewClient(cfg Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// WeatherPoint is one time-step of a location forecast.
type WeatherPoint struct {
	Time        string  `kit:"id" json:"time"`
	Temperature float64 `json:"temperature"`
	WindSpeed   float64 `json:"wind_speed"`
	WindDir     float64 `json:"wind_direction"`
	Humidity    float64 `json:"humidity"`
	CloudCover  float64 `json:"cloud_cover"`
	Symbol      string  `json:"symbol"`    // from next_1_hours or next_6_hours
	Precip1h    float64 `json:"precip_1h"` // 0 if not available
}

// wire types — used only for JSON decoding

type wireTimeseries struct {
	Time string `json:"time"`
	Data struct {
		Instant struct {
			Details struct {
				AirTemperature    float64 `json:"air_temperature"`
				WindSpeed         float64 `json:"wind_speed"`
				WindFromDirection float64 `json:"wind_from_direction"`
				RelativeHumidity  float64 `json:"relative_humidity"`
				CloudAreaFraction float64 `json:"cloud_area_fraction"`
			} `json:"details"`
		} `json:"instant"`
		Next1Hours *struct {
			Summary struct {
				SymbolCode string `json:"symbol_code"`
			} `json:"summary"`
			Details struct {
				PrecipitationAmount float64 `json:"precipitation_amount"`
			} `json:"details"`
		} `json:"next_1_hours"`
		Next6Hours *struct {
			Summary struct {
				SymbolCode string `json:"symbol_code"`
			} `json:"summary"`
			Details struct {
				PrecipitationAmount float64 `json:"precipitation_amount"`
			} `json:"details"`
		} `json:"next_6_hours"`
	} `json:"data"`
}

type wireForecast struct {
	Properties struct {
		Meta struct {
			UpdatedAt string `json:"updated_at"`
		} `json:"meta"`
		Timeseries []wireTimeseries `json:"timeseries"`
	} `json:"properties"`
}

// toWeatherPoint converts a wire timeseries entry to a WeatherPoint.
func toWeatherPoint(w wireTimeseries) WeatherPoint {
	d := w.Data.Instant.Details
	p := WeatherPoint{
		Time:        w.Time,
		Temperature: d.AirTemperature,
		WindSpeed:   d.WindSpeed,
		WindDir:     d.WindFromDirection,
		Humidity:    d.RelativeHumidity,
		CloudCover:  d.CloudAreaFraction,
	}
	if w.Data.Next1Hours != nil {
		p.Symbol = w.Data.Next1Hours.Summary.SymbolCode
		p.Precip1h = w.Data.Next1Hours.Details.PrecipitationAmount
	} else if w.Data.Next6Hours != nil {
		p.Symbol = w.Data.Next6Hours.Summary.SymbolCode
	}
	return p
}

// Forecast fetches the compact location forecast for the given lat/lon and
// returns up to limit time-steps. If limit <= 0 all steps are returned.
func (c *Client) Forecast(ctx context.Context, lat, lon float64, limit int) ([]WeatherPoint, error) {
	url := fmt.Sprintf("%s/weatherapi/locationforecast/2.0/compact?lat=%g&lon=%g",
		c.cfg.BaseURL, lat, lon)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	var wf wireForecast
	if err := json.Unmarshal(body, &wf); err != nil {
		return nil, fmt.Errorf("decode forecast response: %w", err)
	}
	series := wf.Properties.Timeseries
	if limit > 0 && limit < len(series) {
		series = series[:limit]
	}
	out := make([]WeatherPoint, 0, len(series))
	for _, ts := range series {
		out = append(out, toWeatherPoint(ts))
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, url)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", url, lastErr)
}

func (c *Client) do(ctx context.Context, rawURL string) ([]byte, bool, error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	return b, err != nil, err
}

func (c *Client) pace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.Rate <= 0 {
		return
	}
	if wait := c.cfg.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	return min(time.Duration(attempt)*500*time.Millisecond, 5*time.Second)
}
