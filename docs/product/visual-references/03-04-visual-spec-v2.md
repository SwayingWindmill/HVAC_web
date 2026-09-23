# 03 / 04 Visual Specification V2

Status: `VISUAL REFERENCE / IMPLEMENTATION MUST FOLLOW BUSINESS CONTRACTS`

## Reference images

The reviewed generated mockups in the design conversation define the visual direction. The repository stores the corresponding real-browser implementation captures used for the final gate:

- `03-site-overview-implemented-v7.png`
- `04-system-operations-implemented-v7.png`

These captures preserve the approved shadcn/ui + shadcn-admin direction while reflecting the actual data contracts and the scope corrections below: app shell, spacing, attention hierarchy, table density, chart treatment, contextual inspector, restrained semantic color, and overall desktop composition.

Generated-image incidental details are **not** authoritative data contracts. Where a generated image conflicts with a real owner or scope, the implementation contract wins.

## Required implementation corrections already applied

### 03 Site Overview

- Priority work remains the first visual decision layer.
- Site summary is flatter than the generated nested mini-card composition.
- Current Operations and Surface 04 consume the same equipment-group power projection.
- Data-quality state is consistent across the top context and Data Status panel.
- Data-quality denominators are explicit; the UI does not infer missing populations.

### 04 System Operations

The generated image contains two known scope ambiguities that **must not** be copied literally:

1. `运行设备 18 / 22` is not used as the current-workspace equipment population. The real implementation derives the displayed workspace population from the same six topology groups and labels it `当前工作区范围`.
2. Site-level data quality is not presented as selected-object quality in the Context Inspector. Site-level data quality stays explicitly site-scoped; the inspector only shows facts owned for the selected group.

Additional corrections:

- Site alarm totals are not presented as selected-object alarm totals unless a verified group-to-alarm relation exists.
- Diagnose and Control remain equal-weight navigation actions in the inspector.
- No comparison trend is shown without an explicit comparison owner and period definition.
- Decorative photos and decorative ellipsis menus are not required.

## Implementation authority

For 03 / 04 implementation decisions, use this precedence:

1. Surface specification and authoritative API/data contract
2. `DESIGN.md`
3. This visual reference
4. Generated-image incidental details

The visual reference may guide composition and aesthetics but must never override scope, units, timestamp semantics, status semantics, authorization, or accessibility behavior.
