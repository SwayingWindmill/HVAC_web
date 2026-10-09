# Command verification latency source review — 2026-10-09

Scope: #443. A LOW-risk SET_FREQUENCY on the ATV630 acceptance drive took 38–63 s from submission to SUCCEEDED, with `connectivity_command_failed work=verification` every 12 s.

## Finding

Every reported-state read took 5–13 ms and was authoritative (AVAILABLE, ONLINE, CURRENT, FRESH, GOOD, newer revision, observed after acknowledgement) from the first read. The pass waited for the value:

1. **Tolerance boundary.** `commandmodel.ScalarMatches` compared `|actual-expected| <= tolerance` in binary floating point. `48.1 - 48` is `0.10000000000000142`, so a reading exactly at the 0.1 Hz tolerance never matched. The reading stayed at 48.1 Hz for 19 s in the measured run.
2. **Simulated drive output.** The Plant moved pump frequency by a first-order lag with τ = 20 s, so a 1 Hz change needed 40–60 s to come within 0.1 Hz. A VFD's output frequency follows its ACC/DEC ramp linearly.
3. **Runtime amplification.** `DurableVerificationWorker.RunOnce` waits inside one pass until the value matches or the two-minute verification window ends, but connectivity bounds each pass at 12 s. A legitimately waiting verification is cancelled, logged as an error, and reclaimed 3–4 s later.

## Pinned sources

- numpy v2.1.0 (`2f7fe64b8b6d7591dd208942f1cc74473d5db4cb`), [`numpy/_core/numeric.py`](https://github.com/numpy/numpy/blob/2f7fe64b8b6d7591dd208942f1cc74473d5db4cb/numpy/_core/numeric.py) `isclose`: `absolute(a - b) <= (atol + rtol * absolute(b))`.
- CPython v3.13.0 (`60403a5409ff2c3f3b07dd2ca91a7a3e096839c7`), [`Modules/mathmodule.c`](https://github.com/python/cpython/blob/60403a5409ff2c3f3b07dd2ca91a7a3e096839c7/Modules/mathmodule.c) `math_isclose_impl`: passes when the difference is within a relative tolerance of either operand or within the absolute tolerance.
- ThingsBoard v4.4 (`6d46786579c8b29caf5102f95ddb133674bed68b`), [`DeviceActorMessageProcessor.java`](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/main/java/org/thingsboard/server/actors/device/DeviceActorMessageProcessor.java) `registerPendingRpcRequest`: a pending RPC is registered and a timeout message is scheduled for its `expirationTime`; no worker blocks while the device has not answered. [`RpcStatus.java`](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/common/data/src/main/java/org/thingsboard/server/common/data/rpc/RpcStatus.java) keeps TIMEOUT as an in-flight status distinct from terminal EXPIRED/FAILED.

## Decisions

- **ADAPT numpy `isclose`:** the capability's verification tolerance is the absolute term; a relative term of `1e-9 × |expected|` absorbs representation error only. No change to any capability's tolerance.
- **ADAPT VFD ramp for the simulator:** pump output frequency moves linearly at 5 Hz/s (10 s from 0 to 50 Hz). This is a simulator modelling value, not the ATV630 factory ACC/DEC setting, which was not verified here. Flow and power still follow frequency; water temperatures keep their own time constants.
- **REJECT verifying the frequency reference (LFR) readback instead of output frequency:** it proves only that the register was written, not that the drive reached the setpoint.
- **Deferred, not adopted now — ThingsBoard-style scheduled expiry:** verification would release its claim when the value has not yet converged and be re-attempted or expired on schedule instead of blocking a 12 s pass. With the two fixes above, verification completes within a few publish intervals, so the pass bound is not reached; a real device slower than 12 s to converge would still log the waiting pass as an error.

## Evidence

Same feedback loop before and after (one SET_FREQUENCY on the ATV630 acceptance drive, submit to SUCCEEDED):

| | LOW 1 Hz | MEDIUM 3 Hz (approved) |
| --- | --- | --- |
| Before | 61.6 s, 62.6 s, 37.8 s | about 76 s (the #443 report) |
| After | 1.7–2.1 s (5 runs) | 2.0 s, 2.7 s |

No `connectivity_command_failed work=verification` was logged for these commands after the fix. `TestScalarMatchesIncludesTheToleranceBoundary` failed before and passes after the comparison change.
