# Releasing Homedex

Only maintainers with repository release/package permissions should create tags.

## Release outputs

`.goreleaser.yml` builds CGO-free archives for:

- Linux: amd64, arm64, arm/v7
- macOS: amd64, arm64
- Windows: amd64, arm64

Archives include the binary, license, core operational docs, SHA-256 checksums, and Syft SBOMs. The tag workflow separately builds OCI images for `linux/amd64`, `linux/arm64`, and `linux/arm/v7` and is configured to push them to `ghcr.io/harshshah0203/homedex`.

The workflow does not publish a Docker Hub mirror. Do not document one unless a tested publishing job and credentials are added.

The default `docker-compose.yml` pulls `ghcr.io/harshshah0203/homedex:${HOMEDEX_VERSION:-0.2}`, so every `0.2.x` release reaches new installs and `docker compose pull` without a file change. When a release starts a new minor line, bump that default only after the release workflow has published the image; until then, new installs from `main` would try to pull a tag that does not exist yet.

## Budgets

- Binary hard limit: 40 MiB in CI.
- Uncompressed runtime image target: under 30 MiB.
- Uncompressed runtime image hard limit: under 40 MiB.
- Cold application startup: under 2 seconds in the smoke environment.

The target is an engineering goal; the hard limit blocks CI. Check locally with:

```sh
docker build --build-arg VERSION=local -t homedex:local .
./scripts/check-image-size.sh homedex:local
./scripts/smoke-container.sh homedex:local
```

## Pre-tag checklist

```sh
git status --short
make check
make smoke
npx --prefix web playwright install chromium
make test-e2e
docker build --build-arg VERSION=next -t homedex:next .
./scripts/check-image-size.sh homedex:next
./scripts/smoke-container.sh homedex:next
./scripts/check-npm-audit.sh
docker compose -f demo/compose.yml up -d --build
curl -fsS http://127.0.0.1:7377/api/health
HOMEDEX_PORT=17380 docker compose -p homedex-source-check -f docker-compose.yml -f docker-compose.build.yml up -d --build
curl -fsS http://127.0.0.1:17380/api/health
goreleaser check
goreleaser release --snapshot --clean
```

Also verify:

1. README implementation claims match the tagged code.
2. Embedded frontend assets are current.
3. Seeded production E2E runs against the Go-served embedded frontend, not Vite/demo fixtures.
4. Database backup/restore notes cover any migration change.
5. Compose still uses the socket proxy and does not give Homedex the raw socket.
6. The runtime smoke proves the non-root/read-only guarantees and `ssh -V` prerequisite.
7. The fake lab contains only fabricated names and no secret-like labels.
8. Release notes call out migrations, security changes, dependency advisories, and known UI limitations.

## Tagging

Create an annotated semantic-version tag only from a tested clean commit:

```sh
git tag -a v0.1.0 -m "Homedex v0.1.0"
git push origin v0.1.0
```

The GitHub Actions `Release` workflow creates the GitHub release and package. Verify archives, checksums, SBOMs, image architectures, image startup, and the release page before announcing availability. Then check the pull-only install from an empty directory. The separate project name keeps the check away from any Homedex already running on the same machine, and the explicit pull matters: Compose only pulls a missing image by default, so a machine that ran an earlier check would otherwise start the cached image of that line and never test the new one:

```sh
mkdir homedex-release-check
cd homedex-release-check
curl -fsSLO https://raw.githubusercontent.com/HarshShah0203/homedex/main/docker-compose.yml
docker compose -p homedex-release-check pull
HOMEDEX_PORT=17390 docker compose -p homedex-release-check up -d
curl -fsS http://127.0.0.1:17390/api/health
curl -fsS http://127.0.0.1:17390/api/version
docker compose -p homedex-release-check down -v
```

`/api/version` must report the tag you just pushed (for example `{"version":"v0.2.0"}`) whenever that tag is on the line the default file follows. An older version means the moving tag has not been published yet or the pull was skipped, so the check has not tested this release.

GoReleaser marks semantic prerelease tags as prereleases automatically. A configured workflow is not evidence that any particular tag or package already exists.
