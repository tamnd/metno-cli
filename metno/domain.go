// Package metno exposes the MET Norway weather API as a kit Domain driver.
// A multi-domain host (ant) enables it with a single blank import:
//
//	import _ "github.com/tamnd/metno-cli/metno"
//
// The same Domain also builds the standalone metno binary, so the binary and
// a host share one source of truth.
package metno

import (
	"context"
	"fmt"
	"strings"

	"github.com/tamnd/any-cli/kit"
	"github.com/tamnd/any-cli/kit/errs"
)

func init() { kit.Register(Domain{}) }

// Domain is the metno driver. It carries no state; the per-run client is
// built by the factory Register hands to kit.
type Domain struct{}

// Info describes the scheme, the hostnames a pasted link is matched against,
// and the identity reused for the binary's help and version.
func (Domain) Info() kit.DomainInfo {
	return kit.DomainInfo{
		Scheme: "metno",
		Hosts:  []string{Host},
		Identity: kit.Identity{
			Binary: "metno",
			Short:  "Weather forecasts from the Norwegian Meteorological Institute",
			Long: `metno fetches weather forecasts from api.met.no (MET Norway).
No API key required. Every request carries the required User-Agent header.

Rate policy: 1 request per second as a courtesy to the public API.`,
			Site: Host,
			Repo: "https://github.com/tamnd/metno-cli",
		},
	}
}

// Register installs the client factory and every operation onto app.
func (Domain) Register(app *kit.App) {
	app.SetClient(newClient)

	// forecast: time-series weather for a lat/lon
	kit.Handle(app, kit.OpMeta{
		Name:    "forecast",
		Group:   "read",
		List:    true,
		Summary: "Get weather forecast for a location (lat/lon)",
	}, forecastOp)
}

// newClient builds the client from the host-resolved config.
func newClient(_ context.Context, cfg kit.Config) (any, error) {
	c := DefaultConfig()
	if cfg.UserAgent != "" {
		c.UserAgent = cfg.UserAgent
	}
	if cfg.Rate > 0 {
		c.Rate = cfg.Rate
	}
	if cfg.Retries > 0 {
		c.Retries = cfg.Retries
	}
	if cfg.Timeout > 0 {
		c.Timeout = cfg.Timeout
	}
	return NewClient(c), nil
}

// --- inputs ---

type forecastInput struct {
	Lat    float64 `kit:"flag" help:"latitude" default:"0"`
	Lon    float64 `kit:"flag" help:"longitude" default:"0"`
	Limit  int     `kit:"flag,inherit" help:"max time points" default:"24"`
	Client *Client `kit:"inject"`
}

// --- handlers ---

func forecastOp(ctx context.Context, in forecastInput, emit func(WeatherPoint) error) error {
	limit := in.Limit
	if limit <= 0 {
		limit = 24
	}
	points, err := in.Client.Forecast(ctx, in.Lat, in.Lon, limit)
	if err != nil {
		return mapErr(err)
	}
	for _, p := range points {
		if err := emit(p); err != nil {
			return err
		}
	}
	return nil
}

// --- Resolver: pure string functions, no network ---

// Classify turns an input into the canonical (type, id).
// A "lat,lon" pair (two floats separated by comma) is a "location";
// everything else is a "query".
func (Domain) Classify(input string) (uriType, id string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", errs.Usage("empty metno reference")
	}
	if isLatLon(input) {
		return "location", input, nil
	}
	return "query", input, nil
}

// Locate returns the live https URL for a (type, id).
// For "location" the id is "lat,lon"; Locate returns the yr.no forecast URL.
func (Domain) Locate(uriType, id string) (string, error) {
	switch uriType {
	case "location":
		return fmt.Sprintf("https://www.yr.no/nb/forecast/daily-table/%s", id), nil
	case "query":
		return fmt.Sprintf("https://www.yr.no/nb/search?q=%s", id), nil
	default:
		return "", errs.Usage("metno has no resource type %q", uriType)
	}
}

// isLatLon reports whether s is a "lat,lon" pair of two floats.
func isLatLon(s string) bool {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return false
	}
	var lat, lon float64
	_, errLat := fmt.Sscanf(strings.TrimSpace(parts[0]), "%g", &lat)
	_, errLon := fmt.Sscanf(strings.TrimSpace(parts[1]), "%g", &lon)
	return errLat == nil && errLon == nil
}

// mapErr converts a library error into the kit error kind.
func mapErr(err error) error {
	return err
}
