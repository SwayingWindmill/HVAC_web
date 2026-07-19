# 01 — Contract-first Gateway bootstrap

**What to build:** establish a buildable Go platform workspace and expose the first production-shaped public path through `platform-gateway`. A browser or HTTP client can call the Gateway health/version contract and receive a generated, typed response or a stable Problem Details error. The HVAC Web uses the generated client for a minimal platform-status check, proving the public contract, Go build, frontend integration and CI seam without adding business-domain behavior.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] A Go workspace supports independently buildable service binaries rather than one shared process, beginning with `platform-gateway`.
- [ ] The Gateway is the only public service in local/test topology; internal service ports are not exposed to the browser.
- [ ] `/api/v1` includes typed health and build/version representations with stable request/trace headers.
- [ ] All public errors use `application/problem+json` with stable `code`, `traceId`, `retryable` and safe detail.
- [ ] No global `{success,data,message}` response envelope is introduced.
- [ ] OpenAPI is checked in as the public contract authority and generates Go server/client types plus a TypeScript client and runtime validation.
- [ ] Generated artifacts are reproducible with locked tooling; CI fails when regeneration produces a diff.
- [ ] The HVAC Web platform-status check uses the generated TypeScript client rather than handwritten protocol types.
- [ ] Browser and API black-box tests verify success, method-not-allowed, unknown route and malformed request behavior through Gateway.
- [ ] Structured logging and W3C Trace Context are initialized, without logging cookies, tokens or request bodies by default.
- [ ] Go tests, frontend type checking and the existing frontend build run from documented repository commands.
- [ ] The implementation contains no Organization, Device, Telemetry, Command, Schedule or AI business state.
- [ ] Gateway packages remain edge/protocol focused and do not become a generic business-service container.

