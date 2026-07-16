# Releasing Homedex

Only maintainers with repository release/package permissions should create tags.

## Release outputs

`.goreleaser.yml` builds CGO-free archives for:

- Linux: amd64, arm64, arm/v7
- macOS: amd64, arm64
- Windows: amd64, arm64

Archives include the binary, license, core operational docs, SHA-256 checksums, and Syft SBOMs. The tag workflow separately builds OCI images for `linux/amd64`, `linux/arm64`, and `linux/arm/v7` and is configured to push them to `ghcr.io/harshshah0203/homedex`.

The workflow does not publish a Docker Hub mirror. Do not document one unless a tested publishing job and credentials are added.

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
docker build --build-arg VERSION=next -t homedex:next .
./scripts/check-image-size.sh homedex:next
./scripts/smoke-container.sh homedex:next
docker compose -f demo/compose.yml up -d --build
curl -fsS http://127.0.0.1:7377/api/health
goreleaser check
goreleaser release --snapshot --clean
```

Also verify:

1. README implementation claims match the tagged code.
2. Embedded frontend assets are current.
3. Database backup/restore notes cover any migration change.
4. Compose still uses the socket proxy and does not give Homedex the raw socket.
5. The fake lab contains only fabricated names and no secret-like labels.
6. Release notes call out migrations, security changes, and known UI limitations.

## Tagging

Create an annotated semantic-version tag only from a tested clean commit:

```sh
git tag -a v0.1.0 -m "Homedex v0.1.0"
git push origin v0.1.0
```

The GitHub Actions `Release` workflow creates the GitHub release and package. Verify archives, checksums, SBOMs, image architectures, image startup, and the release page before announcing availability.

GoReleaser marks semantic prerelease tags as prereleases automatically. A configured workflow is not evidence that any particular tag or package already exists.
