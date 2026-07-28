package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maximumThingsBoardResponseBytes = int64(4 << 20)

type TokenProvider interface {
	Token(context.Context) (string, error)
}

type FileTokenProvider struct {
	Path string
}

func (provider FileTokenProvider) Token(context.Context) (string, error) {
	content, err := os.ReadFile(provider.Path)
	if err != nil {
		return "", fmt.Errorf("read ThingsBoard JWT file: %w", err)
	}
	token := strings.TrimSpace(string(content))
	if len(token) < 16 || len(token) > 16<<10 {
		return "", errors.New("ThingsBoard JWT file is invalid")
	}
	return token, nil
}

type ThingsBoardSample struct {
	Timestamp int64
	Value     json.RawMessage
}

type ThingsBoardClient struct {
	baseURL       string
	tokenProvider TokenProvider
	httpClient    *http.Client
}

type thingsBoardSampleWire struct {
	Timestamp int64           `json:"ts"`
	Value     json.RawMessage `json:"value"`
}

func NewThingsBoardClient(baseURL string, tokenProvider TokenProvider, httpClient *http.Client) (*ThingsBoardClient, error) {
	if tokenProvider == nil {
		return nil, errors.New("ThingsBoard token provider is required")
	}
	if err := validateProviderURL(baseURL, true); err != nil {
		return nil, fmt.Errorf("ThingsBoard base URL: %w", err)
	}
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				DisableCompression:  true,
				ForceAttemptHTTP2:   false,
				MaxIdleConns:        16,
				MaxIdleConnsPerHost: 8,
				IdleConnTimeout:     30 * time.Second,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &ThingsBoardClient{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), tokenProvider: tokenProvider, httpClient: httpClient}, nil
}

func (client *ThingsBoardClient) FetchTimeseries(ctx context.Context, deviceID string, keys []string, start, end time.Time, limit int) (map[string][]ThingsBoardSample, error) {
	if strings.TrimSpace(deviceID) == "" || len(keys) == 0 || start.IsZero() || end.IsZero() || end.Before(start) || limit < 1 || limit > 1000 {
		return nil, errors.New("ThingsBoard timeseries query is invalid")
	}
	uniqueKeys := make([]string, 0, len(keys))
	seen := map[string]struct{}{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if !telemetryKeyPattern.MatchString(key) {
			return nil, errors.New("ThingsBoard telemetry key is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		uniqueKeys = append(uniqueKeys, key)
	}
	if len(uniqueKeys) == 0 {
		return nil, errors.New("ThingsBoard timeseries query has no keys")
	}
	token, err := client.tokenProvider.Token(ctx)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("keys", strings.Join(uniqueKeys, ","))
	query.Set("startTs", strconv.FormatInt(start.UTC().UnixMilli(), 10))
	query.Set("endTs", strconv.FormatInt(end.UTC().UnixMilli(), 10))
	query.Set("limit", strconv.Itoa(limit))
	query.Set("agg", "NONE")
	query.Set("orderBy", "ASC")
	endpoint := client.baseURL + "/api/plugins/telemetry/DEVICE/" + url.PathEscape(deviceID) + "/values/timeseries?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create ThingsBoard timeseries request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Authorization", "Bearer "+token)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, errors.New("ThingsBoard timeseries request failed")
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, maximumThingsBoardResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read ThingsBoard timeseries response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("ThingsBoard timeseries returned %d", response.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	wire := map[string][]thingsBoardSampleWire{}
	if err := decoder.Decode(&wire); err != nil || ensureJSONEOF(decoder) != nil {
		return nil, errors.New("ThingsBoard timeseries response is invalid")
	}
	result := make(map[string][]ThingsBoardSample, len(wire))
	for key, samples := range wire {
		if _, requested := seen[key]; !requested {
			continue
		}
		converted := make([]ThingsBoardSample, 0, len(samples))
		for _, sample := range samples {
			if sample.Timestamp < 0 || len(bytes.TrimSpace(sample.Value)) == 0 || !json.Valid(sample.Value) {
				return nil, fmt.Errorf("ThingsBoard sample for %s is invalid", key)
			}
			converted = append(converted, ThingsBoardSample{Timestamp: sample.Timestamp, Value: cloneRaw(sample.Value)})
		}
		sort.SliceStable(converted, func(i, j int) bool { return converted[i].Timestamp < converted[j].Timestamp })
		result[key] = converted
	}
	return result, nil
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maximum + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maximum {
		return nil, errors.New("response body exceeds size limit")
	}
	return body, nil
}
