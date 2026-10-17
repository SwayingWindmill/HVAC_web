package analyticsmodel

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The most a semantic query may span and return: 31 days by hour, and rows after its dimensions.
const (
	SemanticBucketLimit    = 744
	SemanticRowLimit       = 2000
	semanticMeasureLimit   = 4
	semanticDimensionLimit = 2
)

// ErrSemanticRowLimit is a query whose rows would exceed SemanticRowLimit: a narrower query is the
// answer, not a retry.
var ErrSemanticRowLimit = fmt.Errorf("the semantic query returns more than %d rows", SemanticRowLimit)

//go:embed semantic_catalog.json
var semanticCatalogJSON []byte

// SemanticMember is a view member as the catalog lists it, with its Chinese title and description.
type SemanticMember struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type SemanticView struct {
	Name        string           `json:"name"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Measures    []SemanticMember `json:"measures"`
	Dimensions  []SemanticMember `json:"dimensions"`
}

// SemanticCatalog is the whitelist of the Operations Agent's semantic query (#573), generated from
// the Cube views by scripts/generate-semantic-catalog.mjs. Members it does not list cannot be asked
// for: the Site comes from the grant and time from the granularity.
type SemanticCatalog struct {
	SchemaVersion int            `json:"schemaVersion"`
	TimeDimension string         `json:"timeDimension"`
	Views         []SemanticView `json:"views"`
}

var semanticCatalog = func() SemanticCatalog {
	var catalog SemanticCatalog
	if err := json.Unmarshal(semanticCatalogJSON, &catalog); err != nil {
		panic(fmt.Sprintf("decode the semantic catalog: %v", err))
	}
	return catalog
}()

func Catalog() SemanticCatalog { return semanticCatalog }

func (catalog SemanticCatalog) View(name string) (SemanticView, bool) {
	index := slices.IndexFunc(catalog.Views, func(view SemanticView) bool { return view.Name == name })
	if index < 0 {
		return SemanticView{}, false
	}
	return catalog.Views[index], true
}

func (view SemanticView) Measure(name string) (SemanticMember, bool) {
	return find(view.Measures, name)
}

func (view SemanticView) Dimension(name string) (SemanticMember, bool) {
	return find(view.Dimensions, name)
}

func find(members []SemanticMember, name string) (SemanticMember, bool) {
	index := slices.IndexFunc(members, func(member SemanticMember) bool { return member.Name == name })
	if index < 0 {
		return SemanticMember{}, false
	}
	return members[index], true
}

// SemanticQuery asks one view for some of its measures by time bucket, optionally grouped by some of
// its dimensions, on the Site's clock.
type SemanticQuery struct {
	TenantID    string      `json:"tenantId"`
	SiteID      string      `json:"siteId"`
	View        string      `json:"view"`
	Measures    []string    `json:"measures"`
	Dimensions  []string    `json:"dimensions,omitempty"`
	Granularity Granularity `json:"granularity"`
	Timezone    string      `json:"timezone"`
	From        time.Time   `json:"from"`
	To          time.Time   `json:"to"`
}

func (query SemanticQuery) Validate() error {
	if !validUUIDv7(query.TenantID) || !validUUIDv7(query.SiteID) {
		return errors.New("semantic query tenant and site must be UUIDv7")
	}
	view, ok := semanticCatalog.View(query.View)
	if !ok {
		return fmt.Errorf("semantic query view %q is not in the catalog", query.View)
	}
	if len(query.Measures) == 0 || len(query.Measures) > semanticMeasureLimit || len(query.Dimensions) > semanticDimensionLimit {
		return fmt.Errorf("semantic query takes 1-%d measures and up to %d dimensions", semanticMeasureLimit, semanticDimensionLimit)
	}
	for _, name := range query.Measures {
		if _, ok := view.Measure(name); !ok {
			return fmt.Errorf("semantic query measure %q is not a %s measure", name, view.Name)
		}
	}
	for _, name := range query.Dimensions {
		if _, ok := view.Dimension(name); !ok {
			return fmt.Errorf("semantic query dimension %q is not a %s dimension", name, view.Name)
		}
	}
	if hasDuplicate(query.Measures) || hasDuplicate(query.Dimensions) {
		return errors.New("semantic query names a member twice")
	}
	if !query.Granularity.Valid() {
		return errors.New("semantic query granularity is invalid")
	}
	location, err := time.LoadLocation(query.Timezone)
	if strings.TrimSpace(query.Timezone) == "" || query.Timezone == "Local" || err != nil {
		return errors.New("semantic query timezone is invalid")
	}
	if query.From.IsZero() || !query.From.Before(query.To) || query.To.Sub(query.From) > MaximumQueryRange {
		return errors.New("semantic query time range is invalid")
	}
	if buckets := countBuckets(query.From.In(location), query.To.In(location), query.Granularity); buckets > SemanticBucketLimit {
		return fmt.Errorf("semantic query spans more than %d time buckets", SemanticBucketLimit)
	}
	return nil
}

// Buckets is how many time buckets of its granularity a valid query's range holds at least.
func (query SemanticQuery) Buckets() int {
	location, _ := time.LoadLocation(query.Timezone)
	return countBuckets(query.From.In(location), query.To.In(location), query.Granularity)
}

// ScopeDigest binds a grant to exactly this query.
func (query SemanticQuery) ScopeDigest() (string, error) {
	if err := query.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Action      string      `json:"action"`
		TenantID    string      `json:"tenantId"`
		SiteID      string      `json:"siteId"`
		View        string      `json:"view"`
		Measures    []string    `json:"measures"`
		Dimensions  []string    `json:"dimensions"`
		Granularity Granularity `json:"granularity"`
		Timezone    string      `json:"timezone"`
		From        string      `json:"from"`
		To          string      `json:"to"`
	}{
		Action: SemanticQueryScope, TenantID: query.TenantID, SiteID: query.SiteID, View: query.View,
		Measures: query.Measures, Dimensions: query.Dimensions, Granularity: query.Granularity, Timezone: query.Timezone,
		From: query.From.UTC().Format(time.RFC3339Nano), To: query.To.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// SemanticQueryScope keeps a semantic query's digest apart from an Energy Series one; both are read
// under the Energy Series action.
const SemanticQueryScope = "analytics.semantic-query"

func hasDuplicate(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func countBuckets(from, to time.Time, granularity Granularity) int {
	count := 0
	for at := from; at.Before(to) && count <= SemanticBucketLimit; count++ {
		switch granularity {
		case GranularityHour:
			at = at.Add(time.Hour)
		case GranularityDay:
			at = at.AddDate(0, 0, 1)
		default:
			at = at.AddDate(0, 1, 0)
		}
	}
	return count
}

// SemanticColumn is one column of a semantic query's rows: its time bucket, a dimension or a measure.
type SemanticColumn struct {
	Name  string             `json:"name"`
	Title string             `json:"title"`
	Kind  SemanticColumnKind `json:"kind"`
}

type SemanticColumnKind string

const (
	SemanticColumnTime      SemanticColumnKind = "time"
	SemanticColumnDimension SemanticColumnKind = "dimension"
	SemanticColumnMeasure   SemanticColumnKind = "measure"
)

// SemanticQueryResponse is the rows of a semantic query: the bucket start (RFC 3339), the dimensions
// as text and the measures as numbers, or null where nothing counted toward them.
type SemanticQueryResponse struct {
	SchemaVersion int              `json:"schemaVersion"`
	Columns       []SemanticColumn `json:"columns"`
	Rows          [][]any          `json:"rows"`
	Metadata      SemanticMetadata `json:"metadata"`
}

type SemanticMetadata struct {
	DataWatermark   *time.Time `json:"dataWatermark,omitempty"`
	DatasetRevision string     `json:"datasetRevision"`
	// Partial says the data does not yet reach the end of the range or a time bucket has no rows.
	Partial bool `json:"partial"`
}
