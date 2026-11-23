package connectivity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	DriftSectionEdgeModels = "EDGE_MODELS"

	maxEdgeModelsJSONBytes        = 262144
	maxEdgeModelStatusesJSONBytes = 65536
	maxEdgeModelBundlesPerGateway = 32
)

// OptionalEdgeModelBundles represents tri-state JSON presence for PUT partial
// updates: omitted (Set=false, Value=nil), explicit JSON null/empty array (Set=true)
// to clear, or a replacement list of signed/validated edge model bundles.
type OptionalEdgeModelBundles struct {
	Set   bool
	Value []edgecontrol.EdgeModelBundle
}

func (o OptionalEdgeModelBundles) IsSet() bool {
	return o.Set || o.Value != nil
}

func (o OptionalEdgeModelBundles) IsZero() bool {
	return !o.Set && o.Value == nil
}

func (o OptionalEdgeModelBundles) MarshalJSON() ([]byte, error) {
	if o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

func (o *OptionalEdgeModelBundles) UnmarshalJSON(data []byte) error {
	if o == nil {
		return errors.New("OptionalEdgeModelBundles: UnmarshalJSON on nil pointer")
	}
	trimmed := bytes.TrimSpace(data)
	o.Set = true
	if bytes.Equal(trimmed, []byte("null")) {
		o.Value = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var bundles []edgecontrol.EdgeModelBundle
	if err := dec.Decode(&bundles); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON content in edgeModels")
	}
	if bundles == nil {
		bundles = []edgecontrol.EdgeModelBundle{}
	}
	o.Value = bundles
	return nil
}

func cloneEdgeModelBundles(in []edgecontrol.EdgeModelBundle) []edgecontrol.EdgeModelBundle {
	if len(in) == 0 {
		return nil
	}
	out := make([]edgecontrol.EdgeModelBundle, len(in))
	for i, b := range in {
		out[i] = b.Clone()
	}
	return out
}

func validateEdgeModelBundles(in []edgecontrol.EdgeModelBundle) ([]edgecontrol.EdgeModelBundle, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxEdgeModelBundlesPerGateway {
		return nil, fmt.Errorf("%w: edgeModels count %d exceeds %d", ErrConfigShadowInvalid, len(in), maxEdgeModelBundlesPerGateway)
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]edgecontrol.EdgeModelBundle, 0, len(in))
	for i, raw := range in {
		b := raw.Clone()
		if err := edgecontrol.ValidateEdgeModelBundleEnvelope(b); err != nil {
			return nil, fmt.Errorf("%w: edgeModels[%d]: %v", ErrConfigShadowInvalid, i, err)
		}
		key := string(b.Domain) + "|" + strings.TrimSpace(b.ModelKey) + "|" + string(b.RolloutMode)
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("%w: duplicate edgeModels entry %q (%s)", ErrConfigShadowInvalid, b.ModelKey, b.RolloutMode)
		}
		seen[key] = struct{}{}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModelKey != out[j].ModelKey {
			return out[i].ModelKey < out[j].ModelKey
		}
		return out[i].RolloutMode < out[j].RolloutMode
	})
	return out, nil
}

func validateEdgeModelStatuses(in []edgecontrol.EdgeModelRuntimeStatus) ([]edgecontrol.EdgeModelRuntimeStatus, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxEdgeModelBundlesPerGateway {
		return nil, fmt.Errorf("%w: edgeModelStatuses count %d exceeds %d", ErrConfigShadowInvalid, len(in), maxEdgeModelBundlesPerGateway)
	}
	out := make([]edgecontrol.EdgeModelRuntimeStatus, 0, len(in))
	for i, st := range in {
		if strings.TrimSpace(st.ModelKey) == "" || !edgecontrol.ValidEdgeModelDomain(st.Domain) {
			return nil, fmt.Errorf("%w: edgeModelStatuses[%d] requires valid modelKey and domain", ErrConfigShadowInvalid, i)
		}
		if st.ShadowSampleCount < 0 || st.RollbackCount < 0 {
			return nil, fmt.Errorf("%w: edgeModelStatuses[%d] counts cannot be negative", ErrConfigShadowInvalid, i)
		}
		if math.IsNaN(st.ShadowMeanAbsDeviation) || math.IsInf(st.ShadowMeanAbsDeviation, 0) ||
			math.IsNaN(st.ShadowMaxAbsDeviation) || math.IsInf(st.ShadowMaxAbsDeviation, 0) {
			return nil, fmt.Errorf("%w: edgeModelStatuses[%d] deviation cannot be NaN/Inf", ErrConfigShadowInvalid, i)
		}
		out = append(out, st)
	}
	return out, nil
}

func edgeModelDriftKey(b edgecontrol.EdgeModelBundle) string {
	return fmt.Sprintf("%s:%s:%s", b.Domain, strings.TrimSpace(b.ModelKey), b.RolloutMode)
}

func diffEdgeModelBundles(desired, reported []edgecontrol.EdgeModelBundle) []DriftDetail {
	dMap := make(map[string]edgecontrol.EdgeModelBundle, len(desired))
	for _, b := range desired {
		dMap[edgeModelDriftKey(b)] = b
	}
	rMap := make(map[string]edgecontrol.EdgeModelBundle, len(reported))
	for _, b := range reported {
		rMap[edgeModelDriftKey(b)] = b
	}
	keys := make([]string, 0, len(dMap)+len(rMap))
	seen := make(map[string]struct{}, len(dMap)+len(rMap))
	for k := range dMap {
		keys = append(keys, k)
		seen[k] = struct{}{}
	}
	for k := range rMap {
		if _, ok := seen[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var drift []DriftDetail
	for _, k := range keys {
		d, hasD := dMap[k]
		r, hasR := rMap[k]
		switch {
		case hasD && !hasR:
			drift = append(drift, DriftDetail{
				Section: DriftSectionEdgeModels,
				Target:  k,
				Reason:  DriftReasonMissingOnEdge,
				Detail: clampUTF8Runes(
					fmt.Sprintf("desired edge model (%s@%s digest=%s) is missing on edge", d.ModelKey, d.Version, shortEdgeBundleDigest(d.ArtifactSHA256)),
					maxDriftDetailRunes,
				),
			})
		case !hasD && hasR:
			drift = append(drift, DriftDetail{
				Section: DriftSectionEdgeModels,
				Target:  k,
				Reason:  DriftReasonUnexpectedOnEdge,
				Detail: clampUTF8Runes(
					fmt.Sprintf("unexpected edge model (%s@%s digest=%s) present on edge", r.ModelKey, r.Version, shortEdgeBundleDigest(r.ArtifactSHA256)),
					maxDriftDetailRunes,
				),
			})
		case d.BundleID != r.BundleID || d.Version != r.Version || d.ArtifactSHA256 != r.ArtifactSHA256:
			drift = append(drift, DriftDetail{
				Section: DriftSectionEdgeModels,
				Target:  k,
				Reason:  DriftReasonValueMismatch,
				Detail: clampUTF8Runes(
					fmt.Sprintf("edge model mismatch (desired %s digest=%s, reported %s digest=%s)",
						d.Version, shortEdgeBundleDigest(d.ArtifactSHA256), r.Version, shortEdgeBundleDigest(r.ArtifactSHA256)),
					maxDriftDetailRunes,
				),
			})
		}
	}
	return drift
}
