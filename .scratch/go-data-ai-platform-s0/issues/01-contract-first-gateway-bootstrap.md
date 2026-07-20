# 01 — Contract-first Gateway bootstrap

**What to build:** establish a buildable Go platform workspace and expose the first production-shaped public path through `platform-gateway`. A browser or HTTP client can call the Gateway health/version contract and receive a generated, typed response or a stable Problem Details error. The HVAC Web uses the generated client for a minimal platform-status check, proving the public contract, Go build, frontend integration and CI seam without adding business-domain behavior.

**Blocked by:** None — can start immediately.

**Status:** completed

- [x] A Go workspace supports independently buildable service binaries rather than one shared process, beginning with `platform-gateway`.
- [x] The Gateway is the only public service in local/test topology; internal service ports are not exposed to the browser.
- [x] `/api/v1` includes typed health and build/version representations with stable request/trace headers.
- [x] All public errors use `application/problem+json` with stable `code`, `traceId`, `retryable` and safe detail.
- [x] No global `{success,data,message}` response envelope is introduced.
- [x] OpenAPI is checked in as the public contract authority and generates Go server/client types plus a TypeScript client and runtime validation.
- [x] Generated artifacts are reproducible with locked tooling; CI fails when regeneration produces a diff.
- [x] The HVAC Web platform-status check uses the generated TypeScript client rather than handwritten protocol types.
- [x] Browser and API black-box tests verify success, method-not-allowed, unknown route and malformed request behavior through Gateway.
- [x] Structured logging and W3C Trace Context are initialized, without logging cookies, tokens or request bodies by default.
- [x] Go tests, frontend type checking and the existing frontend build run from documented repository commands.
- [x] The implementation contains no Organization, Device, Telemetry, Command, Schedule or AI business state.
- [x] Gateway packages remain edge/protocol focused and do not become a generic business-service container.

