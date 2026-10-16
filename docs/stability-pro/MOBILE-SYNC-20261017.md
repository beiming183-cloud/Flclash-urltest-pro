# Mobile selection repair 2026101701

Baseline: 0.8.96, `codex/latency-quality-096` at a89f6eb. The original navigation, proxy list, one-tap delay testing, manual pinning, startup availability path and account identities remain.

The Windows fork's subsequent corrections are ported back into the shared Go engine and outbound integration:

- Retain fresh healthy standbys. A normal quality replacement requires three successful samples spanning ten seconds, a material cost improvement and a thirty-second pool hold. Failure replacement does not wait for that hold.
- Include cold first-response evidence from the first measured sample, avoiding a discontinuity at sample three.
- Re-evaluate validated backups between failed establishment attempts, including backups that became ready during the failed attempt. At most three distinct candidates are tried within the existing request deadline, before application payload is sent.
- Revalidate expired TLS/exit evidence even when a manual delay test just refreshed an outside candidate. Existing trust, expected-exit, cooldown and budget constraints remain.
- Preferred startup hints change probe order, not the visible candidate list.
- Quick exit verification permits 2.5 seconds within the existing three-second bootstrap deadline, rather than rejecting healthy cold TLS at 1.4 seconds.

The diagnostic state now includes scheduler eligibility counts and competition-budget availability. Selection records include quality cost, previous cost, formal sample count, qualification and health age. Expiration is distinguished from a real connection timeout in all four UI locales. Existing log rotation, sampling, privacy filtering and 2 MiB native export limit remain.

Before production changes were applied, local regressions reproduced cold-cost inversion, ignored newly validated standby, expired-identity starvation and hidden candidates. These then passed after the port. Cloud CI and final signed-APK emulator acceptance are recorded separately; unit tests do not prove real-phone ChatGPT SSL behavior. Existing established TCP streams cannot migrate seamlessly.

No certificate verification bypass, cross-IP fallback, desktop routing change or destructive node removal is introduced. Unrelated local edits to Meta.png and UPSTREAM.md are excluded from commits.
