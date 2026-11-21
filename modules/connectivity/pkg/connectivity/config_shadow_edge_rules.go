package connectivity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	DriftSectionEdgeRules = "EDGE_RULES"

	maxEdgeRuleBundleJSONBytes = 262144
)

// OptionalEdgeRuleBundle represents tri-state JSON presence for PUT partial
// updates: omitted (Set=false, Value=nil), explicit JSON null (Set=true,
// Value=nil) to clear, or a replacement edge rule bundle object (Value!=nil).
type OptionalEdgeRuleBundle struct {
	Set   bool
	Value *edgecontrol.EdgeRuleBundle
}

func (o OptionalEdgeRuleBundle) IsSet() bool {
	return o.Set || o.Value != nil
}

func (o OptionalEdgeRuleBundle) IsZero() bool {
	return !o.Set && o.Value == nil
}

func (o OptionalEdgeRuleBundle) MarshalJSON() ([]byte, error) {
	if o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

func (o *OptionalEdgeRuleBundle) UnmarshalJSON(data []byte) error {
	if o == nil {
		return errors.New("OptionalEdgeRuleBundle: UnmarshalJSON on nil pointer")
	}
	trimmed := bytes.TrimSpace(data)
	o.Set = true
	if bytes.Equal(trimmed, []byte("null")) {
		o.Value = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var bundle edgecontrol.EdgeRuleBundle
	if err := dec.Decode(&bundle); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON content in edgeRuleBundle")
	}
	o.Value = &bundle
	return nil
}

func validateEdgeRuleBundle(input *edgecontrol.EdgeRuleBundle) (*edgecontrol.EdgeRuleBundle, error) {
	if input == nil {
		return nil, nil
	}
	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("%w: edgeRuleBundle: %v", ErrConfigShadowInvalid, err)
	}
	return input.Clone(), nil
}

func shortEdgeBundleDigest(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12]
}

func diffEdgeRuleBundle(desired, reported *edgecontrol.EdgeRuleBundle) []DriftDetail {
	switch {
	case desired == nil && reported == nil:
		return nil
	case desired != nil && reported == nil:
		return []DriftDetail{{
			Section: DriftSectionEdgeRules,
			Target:  "edgeRuleBundle",
			Reason:  DriftReasonMissingOnEdge,
			Detail: clampUTF8Runes(
				fmt.Sprintf("desired edge rule bundle (rev=%d digest=%s rules=%d) is missing on edge", desired.BundleRevision, shortEdgeBundleDigest(desired.EdgeBundleDigest), len(desired.Rules)),
				maxDriftDetailRunes,
			),
		}}
	case desired == nil && reported != nil:
		return []DriftDetail{{
			Section: DriftSectionEdgeRules,
			Target:  "edgeRuleBundle",
			Reason:  DriftReasonUnexpectedOnEdge,
			Detail: clampUTF8Runes(
				fmt.Sprintf("unexpected edge rule bundle (rev=%d digest=%s rules=%d) present on edge", reported.BundleRevision, shortEdgeBundleDigest(reported.EdgeBundleDigest), len(reported.Rules)),
				maxDriftDetailRunes,
			),
		}}
	case desired.EdgeBundleDigest != reported.EdgeBundleDigest || desired.BundleRevision != reported.BundleRevision || len(desired.Rules) != len(reported.Rules):
		return []DriftDetail{{
			Section: DriftSectionEdgeRules,
			Target:  "edgeRuleBundle",
			Reason:  DriftReasonValueMismatch,
			Detail: clampUTF8Runes(
				fmt.Sprintf("edge rule bundle mismatch (desired rev=%d digest=%s rules=%d, reported rev=%d digest=%s rules=%d)",
					desired.BundleRevision, shortEdgeBundleDigest(desired.EdgeBundleDigest), len(desired.Rules),
					reported.BundleRevision, shortEdgeBundleDigest(reported.EdgeBundleDigest), len(reported.Rules),
				),
				maxDriftDetailRunes,
			),
		}}
	default:
		return nil
	}
}
