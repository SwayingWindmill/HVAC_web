# 06 — HVAC Web real Organization/Site/Device pages

**What to build:** replace the target HVAC Web Registry Mock reads with the generated S1 client. The System site's Organization/Site/asset-structure view and the Registry metadata used by Assets navigation must read through Gateway in real mode, display authoritative Site timezone and platform IDs, and show typed empty/error/degraded states without silently falling back to Mock.

**Blocked by:** 05 — Gateway Registry API and generated clients.

**Status:** ready-after-spec-approval

- [ ] The target Registry views use generated Organization, Site, Equipment and Device types and client functions.
- [ ] `mockSites` and `mockAssetTree` are not imported or used by the target Registry view when `VITE_API_MODE=real`.
- [ ] Thin frontend adapters may build presentation nodes but do not copy protocol DTOs, infer owning Organization or decide authorization.
- [ ] Organization and Site navigation uses authorized API results rather than token claims, localStorage or `X-Site-Id`.
- [ ] Site detail displays the authoritative IANA timezone and platform UUIDv7.
- [ ] Equipment and Device remain visually and semantically distinct; a Legacy Asset is not presented as verified Equipment without a valid mapping.
- [ ] Device lifecycle/registry status is clearly separated from online or telemetry status, which remains unavailable until S2.
- [ ] Pagination supports load-more/next-cursor behavior without offset or exact-count assumptions.
- [ ] Empty collections, invalid cursor, resource invisibility, unavailable Core and retryable Gateway failures have typed user-visible states.
- [ ] Real mode never catches a failed Registry request and substitutes local Mock business data.
- [ ] Target Registry write controls are disabled or explicitly deferred; they do not continue mutating local Mock state as if production changed.
- [ ] User/role management, telemetry values, alarms, commands, schedules and AI surfaces are not expanded by S1.
- [ ] Browser tests prove a logged-in user can navigate Organization → Site → Equipment/Device using only Gateway requests.
- [ ] Browser tests prove cross-organization and sibling-Site resources cannot be discovered through navigation, URL entry, cursor reuse or error detail.
- [ ] Accessibility, loading behavior and existing layout conventions remain intact without a broad visual redesign.
- [ ] Frontend production build and generated-client drift checks pass.
