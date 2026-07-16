# Frontend Dependency Security

This record documents the `npm audit` result reviewed on 2026-07-16. Run `./scripts/check-npm-audit.sh` after `npm ci`; CI fails if the advisory set or severity changes, and it separately requires `npm audit --omit=dev` to remain clean.

## Runtime exposure

`npm audit --omit=dev` reports **zero vulnerabilities**. Node.js, Vite, Vitest, and esbuild are not included in Homedex release archives or the runtime container. The frontend job produces static HTML/CSS/JavaScript that is embedded in the Go binary.

The full audit reports five vulnerable package records: three moderate, one high, and one critical. They are all beneath the development-only Vitest 2 test runner. Homedex runs `vitest run`; it does not enable or publish Vitest UI. CI runners are ephemeral. Developers should still avoid exposing any Vite/Vitest development server to untrusted networks.

| Package record | Installed path/version | Severity | Advisory and applicable exposure |
|---|---|---:|---|
| `@vitest/mocker` | Vitest's `@vitest/mocker@2.1.9` | Moderate | Inherits the vulnerable nested Vite development server described below; test/build tooling only. |
| `esbuild` | Vitest's nested `esbuild@0.21.5` | Moderate | [GHSA-67mh-4wv8-2f99](https://github.com/advisories/GHSA-67mh-4wv8-2f99): an untrusted website can read responses from a reachable esbuild development server. No esbuild server runs in production. |
| `vite` | Vitest's nested `vite@5.4.21` | High | Aggregate of [GHSA-4w7w-66w2-5vf9](https://github.com/advisories/GHSA-4w7w-66w2-5vf9), [GHSA-v6wh-96g9-6wx3](https://github.com/advisories/GHSA-v6wh-96g9-6wx3), and [GHSA-fx2h-pf6j-xcff](https://github.com/advisories/GHSA-fx2h-pf6j-xcff); affects a reachable development server, including Windows-specific paths/UNC handling. The direct build dependency is fixed `vite@6.4.3`; only Vitest 2's private Vite 5 copy remains. |
| `vite-node` | `vite-node@2.1.9` | Moderate | Inherits the nested Vite findings; test runner only. |
| `vitest` | `vitest@2.1.9` | Critical | [GHSA-5xrq-8626-4rwp](https://github.com/advisories/GHSA-5xrq-8626-4rwp): arbitrary file read/execution when Vitest UI is listening. Homedex does not use Vitest UI or ship Vitest. |

## Remediation decision

The lockfile already resolves `vitest@2.1.9`, the newest release allowed by the declared `^2.1.8` range, and direct `vite@6.4.3`/`esbuild@0.25.12` are fixed. `npm audit fix` offers only `vitest@4.1.10`, marked as a SemVer-major change; the earliest patched Vitest line is also a major upgrade from v2. There is therefore no nonbreaking dependency update that removes these five records.

Do not use `npm audit fix --force` to hide this review. Upgrade Vitest deliberately in a focused change, migrate any changed test/config APIs, run unit and seeded production E2E suites, then update this file and the audit contract together.
