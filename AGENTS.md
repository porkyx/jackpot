# Instructions

## Read First
- Read the code before writing tests.
- Treat code as the source of truth.
- Do not assume behavior.

## Simplicity (Implementation)
- Simplicity is the highest value.
- Apply KISS to implementation: choose the simplest design that meets every requirement below.
- Prefer simpler implementation, never fewer requirements.
- Reject complexity that serves no required behavior or invariant.
- Choose clarity over cleverness.

## Testing (Rigor)
- KISS does not apply to testing.
- Apply SQLite-grade rigor across coverage, harnesses, sanitizers, and invariants.
- Tests are a first-class investment, not overhead.
- The rules below are the floor, not the ceiling.

## Required
- Test both success and failure paths.
- Test all branches of conditions.
- Test boundary values (nil, empty, zero, min, max).
- Add regression tests for every bug fix.
- Validate invariants, not just errors.
- Ensure tests are deterministic.
- Ensure all resources are cleaned up.
- Use multiple test layers when needed (unit, boundary, fault, concurrency, fuzz).

## Must Verify
- state consistency
- side effects
- idempotency
- rollback correctness
- resource cleanup

## Coverage
- Target 100% branch coverage on core logic.
- Apply MC/DC (Modified Condition/Decision Coverage) to boolean-heavy code.
- Verify branches via mutation testing: every branch must change observable behavior or state.

## Invariants
- Assert preconditions, postconditions, and loop invariants in code.
- Assert expected-unreachable branches; make violations loud, not silent.
- Required invariant checks must stay enforced in production. Only purely debug-only assertions may be elided.

## Failure Injection
- Simulate dependency failures.
- Cover first-call, Nth-call, and continuous failures.
- Include timeout and cancellation cases.
- Include partial success followed by failure.

## Concurrency
- Verify no race conditions.
- Verify no deadlocks.
- Verify no duplicate execution.
- Verify ordering and invariants.

## Persistence
- Ensure atomic behavior (all or nothing).
- Ensure no corrupt intermediate state.
- Ensure safe retry and recovery.

## Fuzz
- Apply fuzz tests to input parsing and decoding.
- Ensure no panic or unbounded resource usage.

## Harnesses
- Run multiple independent test harnesses against the same code.
- Maintain a fast pre-commit suite and a full pre-release suite.
- Preserve every fuzz or crash finding as a permanent regression case.

## Sanitizers and Builds
- Run the full suite under every dynamic analyzer the toolchain provides (race, address, undefined, memory, leak).
- For compiled languages, verify optimized and unoptimized builds produce identical output.
- For cross-platform targets, test across relevant architectures, endianness, and compilers.

## Determinism
- Do not rely on sleep-based timing.
- Control time and randomness.
- Use bounded retries.

## Naming
- Name tests by behavior, edge case, or failure mode.

## Release Gate
- Produce a concrete human-review checklist for every release candidate; do not merely state that one is required.
- Enumerate specific, checkable items across user-visible behavior, security posture, performance, compatibility, and rollback readiness — specs and CI cannot capture intent or cross-cutting impact.
- Re-run the full regression suite before every release candidate.

## Forbidden
- Do not test only happy paths.
- Do not skip edge cases.
- Do not write non-deterministic tests.
- Do not leave resources unverified.
- Do not merge multiple concerns into one test.
- Do not rely only on line coverage.
- Do not ignore failure scenarios.