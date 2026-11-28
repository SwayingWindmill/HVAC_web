package forecast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/clickhousehttp"
)

const (
	minOutdoorDryBulbC       = -25.0
	maxOutdoorDryBulbC       = 50.0
	minOutdoorWetBulbC       = -30.0
	maxOutdoorWetBulbC       = 45.0
	minRelativeHumidityPct   = 1.0
	maxRelativeHumidityPct   = 100.0
	maxWetBulbExceedanceC    = 0.5
	defaultOpenMeteoBaseURL  = "https://api.open-meteo.com/v1/forecast"
	defaultOpenMeteoTimeout  = 2 * time.Second
	maxOpenMeteoResponseSize = int64(512 << 10)
)

const (
	WeatherSourceNone             = "NONE"
	WeatherSourceSiteSensors      = "SITE_SENSORS"
	WeatherSourceOpenMeteo        = "OPEN_METEO"
	WeatherSourceSiteAndOpenMeteo = "SITE_AND_OPEN_METEO"
)

// SiteCoordinates identifies geographic coordinates for external weather lookups.
type SiteCoordinates struct {
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lon"`
}

// WeatherObservation holds sanitized outdoor weather conditions for one timestamp.
type WeatherObservation struct {
	ObservedAt          time.Time `json:"observedAt"`
	DryBulbC            *float64  `json:"dryBulbC,omitempty"`
	WetBulbC            *float64  `json:"wetBulbC,omitempty"`
	RelativeHumidityPct *float64  `json:"relativeHumidityPct,omitempty"`
	Source              string    `json:"source,omitempty"`
}

// WeatherHistoryQuery scopes historical site weather queries in telemetry_history.observations.
type WeatherHistoryQuery struct {
	TenantID           string
	SiteID             string
	From               time.Time
	To                 time.Time
	GranularityMinutes int
}

// WeatherHistoryReader reads historical site outdoor weather measurements.
type WeatherHistoryReader interface {
	ReadWeatherSeries(context.Context, WeatherHistoryQuery) ([]WeatherObservation, error)
}

// WeatherForecastRequest asks for forecasted weather for a site window.
type WeatherForecastRequest struct {
	SiteID             string
	From               time.Time
	To                 time.Time
	GranularityMinutes int
}

// WeatherForecastProvider fetches optional forecasted outdoor weather (e.g. Open-Meteo).
type WeatherForecastProvider interface {
	ForecastWeather(context.Context, WeatherForecastRequest) ([]WeatherObservation, time.Time, error)
}

// SanitizeDryBulbC validates physical outdoor dry-bulb bounds [-25, 50] degC.
func SanitizeDryBulbC(value float64) (float64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < minOutdoorDryBulbC || value > maxOutdoorDryBulbC {
		return 0, false
	}
	return value, true
}

// SanitizeWetBulbC validates physical outdoor wet-bulb bounds [-30, 45] degC.
func SanitizeWetBulbC(value float64) (float64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < minOutdoorWetBulbC || value > maxOutdoorWetBulbC {
		return 0, false
	}
	return value, true
}

// SanitizeRelativeHumidityPct validates relative humidity bounds [1, 100] %.
func SanitizeRelativeHumidityPct(value float64) (float64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < minRelativeHumidityPct || value > maxRelativeHumidityPct {
		return 0, false
	}
	return value, true
}

// StullWetBulbC estimates wet-bulb temperature (degC) from dry-bulb temperature (degC)
// and relative humidity (%) using Stull (2011).
func StullWetBulbC(dryBulbC, rhPct float64) (float64, bool) {
	td, okT := SanitizeDryBulbC(dryBulbC)
	rh, okRH := SanitizeRelativeHumidityPct(rhPct)
	if !okT || !okRH {
		return 0, false
	}
	tw := td*math.Atan(0.151977*math.Sqrt(rh+8.313659)) +
		math.Atan(td+rh) -
		math.Atan(rh-1.676331) +
		0.00391838*math.Pow(rh, 1.5)*math.Atan(0.023101*rh) -
		4.686035
	if math.IsNaN(tw) || math.IsInf(tw, 0) {
		return 0, false
	}
	if tw > td {
		tw = td
	}
	if tw < minOutdoorWetBulbC {
		return 0, false
	}
	return tw, true
}

// sanitizeWeatherObservation enforces physical bounds and applies Stull wet-bulb enrichment
// when direct wet-bulb is unavailable but dry-bulb and relative humidity are present.
func sanitizeWeatherObservation(raw WeatherObservation) (WeatherObservation, bool) {
	if raw.ObservedAt.IsZero() {
		return WeatherObservation{}, false
	}
	out := WeatherObservation{
		ObservedAt: raw.ObservedAt.UTC(),
		Source:     strings.TrimSpace(raw.Source),
	}
	if raw.DryBulbC != nil {
		if v, ok := SanitizeDryBulbC(*raw.DryBulbC); ok {
			out.DryBulbC = &v
		}
	}
	if raw.RelativeHumidityPct != nil {
		if v, ok := SanitizeRelativeHumidityPct(*raw.RelativeHumidityPct); ok {
			out.RelativeHumidityPct = &v
		}
	}
	if raw.WetBulbC != nil {
		if v, ok := SanitizeWetBulbC(*raw.WetBulbC); ok {
			if out.DryBulbC == nil || v <= *out.DryBulbC+maxWetBulbExceedanceC {
				clamped := v
				if out.DryBulbC != nil && clamped > *out.DryBulbC {
					clamped = *out.DryBulbC
				}
				out.WetBulbC = &clamped
			}
		}
	}
	if out.WetBulbC == nil && out.DryBulbC != nil && out.RelativeHumidityPct != nil {
		if tw, ok := StullWetBulbC(*out.DryBulbC, *out.RelativeHumidityPct); ok {
			out.WetBulbC = &tw
		}
	}
	if out.DryBulbC == nil && out.WetBulbC == nil && out.RelativeHumidityPct == nil {
		return WeatherObservation{}, false
	}
	return out, true
}

// mergeWeatherObservations combines site sensor observations and external weather forecasts on a
// slot grid. Site sensors strictly take precedence over external observations for the same slot.
func mergeWeatherObservations(slot time.Duration, siteObs, externalObs []WeatherObservation) []WeatherObservation {
	bySlot := make(map[int64]WeatherObservation, len(siteObs)+len(externalObs))
	apply := func(items []WeatherObservation, prefer bool) {
		for _, item := range items {
			clean, ok := sanitizeWeatherObservation(item)
			if !ok {
				continue
			}
			key := clean.ObservedAt.Truncate(slot).Unix()
			clean.ObservedAt = time.Unix(key, 0).UTC()
			existing, exists := bySlot[key]
			if !exists {
				bySlot[key] = clean
				continue
			}
			merged := existing
			if prefer {
				if clean.DryBulbC != nil {
					merged.DryBulbC = clean.DryBulbC
					merged.Source = clean.Source
				}
				if clean.RelativeHumidityPct != nil {
					merged.RelativeHumidityPct = clean.RelativeHumidityPct
				}
				if clean.WetBulbC != nil {
					merged.WetBulbC = clean.WetBulbC
				}
			} else {
				if merged.DryBulbC == nil && clean.DryBulbC != nil {
					merged.DryBulbC = clean.DryBulbC
					if merged.Source == "" {
						merged.Source = clean.Source
					}
				}
				if merged.RelativeHumidityPct == nil && clean.RelativeHumidityPct != nil {
					merged.RelativeHumidityPct = clean.RelativeHumidityPct
				}
				if merged.WetBulbC == nil && clean.WetBulbC != nil {
					merged.WetBulbC = clean.WetBulbC
				}
			}
			if merged.WetBulbC == nil && merged.DryBulbC != nil && merged.RelativeHumidityPct != nil {
				if tw, ok := StullWetBulbC(*merged.DryBulbC, *merged.RelativeHumidityPct); ok {
					merged.WetBulbC = &tw
				}
			}
			bySlot[key] = merged
		}
	}
	apply(externalObs, false)
	apply(siteObs, true)
	if len(bySlot) == 0 {
		return nil
	}
	keys := make([]int64, 0, len(bySlot))
	for k := range bySlot {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]WeatherObservation, 0, len(keys))
	for _, k := range keys {
		out = append(out, bySlot[k])
	}
	return out
}

// OpenMeteoConfig configures optional free Open-Meteo weather fetching.
type OpenMeteoConfig struct {
	Enabled       bool
	BaseURL       string
	DefaultCoords *SiteCoordinates
	SiteCoords    map[string]SiteCoordinates
	HTTPClient    *http.Client
	Clock         Clock
}

// OpenMeteoProvider fetches hourly outdoor temperature and relative humidity from Open-Meteo
// and computes wet-bulb via Stull approximation. Disabled or unconfigured providers return no-op.
type OpenMeteoProvider struct {
	enabled       bool
	baseURL       *url.URL
	defaultCoords *SiteCoordinates
	siteCoords    map[string]SiteCoordinates
	client        *http.Client
	clock         Clock
}

// NewOpenMeteoProvider constructs an OpenMeteoProvider; when disabled, it succeeds cleanly as a no-op.
func NewOpenMeteoProvider(cfg OpenMeteoConfig) (*OpenMeteoProvider, error) {
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	if !cfg.Enabled {
		return &OpenMeteoProvider{enabled: false, clock: clock}, nil
	}
	rawURL := strings.TrimSpace(cfg.BaseURL)
	if rawURL == "" {
		rawURL = defaultOpenMeteoBaseURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("open-meteo base URL must be an absolute http/https URL")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultOpenMeteoTimeout}
	}
	coordsMap := make(map[string]SiteCoordinates, len(cfg.SiteCoords))
	for k, v := range cfg.SiteCoords {
		coordsMap[strings.TrimSpace(k)] = v
	}
	var defCopy *SiteCoordinates
	if cfg.DefaultCoords != nil {
		c := *cfg.DefaultCoords
		defCopy = &c
	}
	return &OpenMeteoProvider{
		enabled:       true,
		baseURL:       parsed,
		defaultCoords: defCopy,
		siteCoords:    coordsMap,
		client:        client,
		clock:         clock,
	}, nil
}

// NewOpenMeteoProviderFromEnv builds an optional Open-Meteo provider from environment variables.
// Unset or disabled envs return nil without error so offline operation never stalls.
func NewOpenMeteoProviderFromEnv() (*OpenMeteoProvider, error) {
	enabledRaw := strings.TrimSpace(os.Getenv("FORECAST_OPEN_METEO_ENABLED"))
	enabled := strings.EqualFold(enabledRaw, "true") || enabledRaw == "1"
	if !enabled {
		return nil, nil
	}
	var defCoords *SiteCoordinates
	latStr := strings.TrimSpace(os.Getenv("FORECAST_OPEN_METEO_LAT"))
	lonStr := strings.TrimSpace(os.Getenv("FORECAST_OPEN_METEO_LON"))
	if latStr != "" && lonStr != "" {
		lat, latErr := strconv.ParseFloat(latStr, 64)
		lon, lonErr := strconv.ParseFloat(lonStr, 64)
		if latErr != nil || lonErr != nil || !validCoords(lat, lon) {
			return nil, errors.New("FORECAST_OPEN_METEO_LAT/LON must be valid coordinates")
		}
		defCoords = &SiteCoordinates{Latitude: lat, Longitude: lon}
	}
	var siteCoords map[string]SiteCoordinates
	if rawMap := strings.TrimSpace(os.Getenv("FORECAST_OPEN_METEO_SITE_COORDS")); rawMap != "" {
		if err := json.Unmarshal([]byte(rawMap), &siteCoords); err != nil {
			return nil, fmt.Errorf("parse FORECAST_OPEN_METEO_SITE_COORDS: %w", err)
		}
		for siteID, c := range siteCoords {
			if !validCoords(c.Latitude, c.Longitude) {
				return nil, fmt.Errorf("invalid coordinates for site %s", siteID)
			}
		}
	}
	timeout := defaultOpenMeteoTimeout
	if rawTimeout := strings.TrimSpace(os.Getenv("FORECAST_OPEN_METEO_TIMEOUT")); rawTimeout != "" {
		parsedTimeout, err := time.ParseDuration(rawTimeout)
		if err != nil || parsedTimeout <= 0 {
			return nil, errors.New("FORECAST_OPEN_METEO_TIMEOUT must be a positive duration")
		}
		timeout = parsedTimeout
	}
	return NewOpenMeteoProvider(OpenMeteoConfig{
		Enabled:       true,
		BaseURL:       strings.TrimSpace(os.Getenv("FORECAST_OPEN_METEO_BASE_URL")),
		DefaultCoords: defCoords,
		SiteCoords:    siteCoords,
		HTTPClient:    &http.Client{Timeout: timeout},
	})
}

func validCoords(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsInf(lat, 0) && !math.IsNaN(lon) && !math.IsInf(lon, 0) &&
		lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

var ErrOpenMeteoCoordsMissing = errors.New("open-meteo site coordinates are not configured")

func (p *OpenMeteoProvider) ForecastWeather(ctx context.Context, req WeatherForecastRequest) ([]WeatherObservation, time.Time, error) {
	if p == nil || !p.enabled {
		return nil, time.Time{}, nil
	}
	coords, ok := p.siteCoords[req.SiteID]
	if !ok {
		if p.defaultCoords == nil {
			return nil, time.Time{}, ErrOpenMeteoCoordsMissing
		}
		coords = *p.defaultCoords
	}
	if !validCoords(coords.Latitude, coords.Longitude) {
		return nil, time.Time{}, ErrOpenMeteoCoordsMissing
	}
	days := 3
	if spanDays := int(math.Ceil(req.To.Sub(req.From).Hours() / 24.0)); spanDays > days && spanDays <= 7 {
		days = spanDays
	}
	endpoint := *p.baseURL
	q := endpoint.Query()
	q.Set("latitude", strconv.FormatFloat(coords.Latitude, 'f', 4, 64))
	q.Set("longitude", strconv.FormatFloat(coords.Longitude, 'f', 4, 64))
	q.Set("hourly", "temperature_2m,relative_humidity_2m")
	q.Set("forecast_days", strconv.Itoa(days))
	q.Set("timezone", "UTC")
	endpoint.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer clickhousehttp.DrainAndClose(resp.Body)
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenMeteoResponseSize+1))
	if err != nil {
		return nil, time.Time{}, err
	}
	if int64(len(payload)) > maxOpenMeteoResponseSize {
		return nil, time.Time{}, errors.New("open-meteo response exceeds 512 KiB")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, time.Time{}, fmt.Errorf("open-meteo returned %d", resp.StatusCode)
	}
	var decoded struct {
		Hourly struct {
			Time               []string   `json:"time"`
			Temperature2m      []*float64 `json:"temperature_2m"`
			RelativeHumidity2m []*float64 `json:"relative_humidity_2m"`
		} `json:"hourly"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, time.Time{}, fmt.Errorf("decode open-meteo payload: %w", err)
	}
	issueTime := p.clock().UTC()
	minutes := req.GranularityMinutes
	if minutes <= 0 {
		minutes = 15
	}
	subSteps := 60 / minutes
	if subSteps < 1 {
		subSteps = 1
	}
	fromUTC := req.From.UTC()
	toUTC := req.To.UTC()
	var observations []WeatherObservation
	for i, rawTime := range decoded.Hourly.Time {
		parsedAt, parseErr := parseOpenMeteoTime(rawTime)
		if parseErr != nil {
			continue
		}
		var dryPtr, rhPtr *float64
		if i < len(decoded.Hourly.Temperature2m) {
			dryPtr = decoded.Hourly.Temperature2m[i]
		}
		if i < len(decoded.Hourly.RelativeHumidity2m) {
			rhPtr = decoded.Hourly.RelativeHumidity2m[i]
		}
		// Interpolate sub-hourly steps linearly when possible; fall back to step hold at series end.
		var nextDryPtr, nextRhPtr *float64
		if i+1 < len(decoded.Hourly.Time) {
			if i+1 < len(decoded.Hourly.Temperature2m) {
				nextDryPtr = decoded.Hourly.Temperature2m[i+1]
			}
			if i+1 < len(decoded.Hourly.RelativeHumidity2m) {
				nextRhPtr = decoded.Hourly.RelativeHumidity2m[i+1]
			}
		}
		for sub := 0; sub < subSteps; sub++ {
			stepAt := parsedAt.Add(time.Duration(sub*minutes) * time.Minute)
			if stepAt.Before(fromUTC) || stepAt.After(toUTC) {
				continue
			}
			frac := float64(sub) / float64(subSteps)
			stepDry := interpolatePtr(dryPtr, nextDryPtr, frac)
			stepRH := interpolatePtr(rhPtr, nextRhPtr, frac)
			clean, ok := sanitizeWeatherObservation(WeatherObservation{
				ObservedAt:          stepAt,
				DryBulbC:            stepDry,
				RelativeHumidityPct: stepRH,
				Source:              WeatherSourceOpenMeteo,
			})
			if ok {
				observations = append(observations, clean)
			}
		}
	}
	return observations, issueTime, nil
}

func interpolatePtr(a, b *float64, frac float64) *float64 {
	if a == nil {
		return nil
	}
	if b == nil || frac == 0 {
		v := *a
		return &v
	}
	v := *a*(1.0-frac) + *b*frac
	return &v
}

func parseOpenMeteoTime(raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04"} {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported open-meteo timestamp %q", raw)
}
