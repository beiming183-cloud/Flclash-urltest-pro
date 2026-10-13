# Provenance / 来源

App baseline: [chen08209/FlClash v0.8.99](https://github.com/chen08209/FlClash/releases/tag/v0.8.99), commit `68c71b8ef9b7486a224972eb371ff153c6b2de0f`.

Core baseline: [chen08209/Clash.Meta](https://github.com/chen08209/Clash.Meta/tree/8597778c7df410ebe88c57e59eaab32d203156df), commit `8597778c7df410ebe88c57e59eaab32d203156df`, exactly the app tag's `core/Clash.Meta` gitlink. Core tracked files have been vendored into this repository; no nested Git metadata. The module replace/import layout remains unchanged.

LICENSE files and upstream attribution remain. Read upstream README_UPSTREAM.md for app screenshots, distribution and original credits. New reference model/docs use GPL-3.0 under the root license.

Changes to baseline in this handoff:

- Original README renamed README_UPSTREAM.md; root README describes the handoff status.
- Core gitlink replaced by actual pinned source; `.gitmodules` documents this choice.
- Original workflows relocated into `docs/stability-pro/upstream-workflows/*.disabled`, avoiding automatic upstream release/telemetry/signing jobs in this new repository.
- Added design, reference code/tests/fixtures, handoff checks, lightweight CI and ignore entries. Application runtime logic is unchanged in this delivery.

## Research references

These are design references, not dependencies or claims that their algorithms run in this app:

- [URLTest at the pinned core](https://github.com/chen08209/Clash.Meta/blob/8597778c7df410ebe88c57e59eaab32d203156df/adapter/outboundgroup/urltest.go): latest delay/tolerance and a single selected proxy per dial; no cross-candidate retry inside that call.
- [Mihomo v1.19.31 groupbase](https://github.com/MetaCubeX/mihomo/blob/v1.19.31/adapter/outboundgroup/groupbase.go): failures can trigger health checks, not migrate existing TCP connections.
- [Smart Controller architecture](https://github.com/AlexWhite1111/mihomo-smart-controller/blob/main/docs/ARCHITECTURE.md): reliability, secondary verification, bounded exploration and cooldown. Its deployment is not a drop-in Android implementation.
- [Smart core implementation](https://github.com/vernesong/mihomo/blob/Alpha/adapter/outboundgroup/smart.go): dial-time retry/adaptive timeout ideas; do not assume complete residential CONNECT/TLS failure coverage without tests.
- [Smart Selector](https://github.com/Uddoo/mihomo-smart-selector): external selection reference, not the in-process Android service proposed here.
- [Surge Smart](https://kb.nssurge.com/surge-knowledge-base/guidelines/smart-group): reducing connection-establishment failure impact.
- [Successive Halving research](https://proceedings.mlr.press/v51/jamieson16.html): inspiration for layered budget allocation; dynamic networks require expiry and re-entry, not permanent elimination.
- [OpenAI network recommendations](https://help.openai.com/en/articles/9247338-network-recommendations-for-chatgpt-errors-on-web-and-apps): TLS inspection and WebSocket constraints. Specific macOS statements do not establish the Android incident's root cause.

Reviewed on 2026-10-13. Moving main/Alpha URLs can change; implement against pinned local source and record any further upstream changes.
