# Releasing and container images

A `v*` tag runs `.github/workflows/release.yml`: the `binaries` job (tests, release notes from `CHANGELOG.md`,
goreleaser) and then one `images` job per image (`deploy/Dockerfile` and `deploy/Dockerfile.bridge`), each built for
`linux/amd64` and `linux/arm64` with buildx.

## Why the build stages run on `$BUILDPLATFORM`

The hosted runners are amd64, so an arm64 build stage runs under QEMU emulation. An emulated `pnpm install` is many
times slower than a native one and has hung release jobs for over 20 minutes. So in both Dockerfiles:

- The `web` stage is `FROM --platform=$BUILDPLATFORM`. The web assets are the same for every architecture, so it is
  built once, natively, and shared by both targets.
- The `go` stage is also `FROM --platform=$BUILDPLATFORM` and cross-compiles with `GOOS=$TARGETOS GOARCH=$TARGETARCH`.
  This is safe because the binary is pure Go (`CGO_ENABLED=0`).
- Runtime stages keep the target platform. The plain image's runtime stage has no `RUN` step; the bridge image's
  installs the vendor CLIs and system packages, which is the only part still emulated.

Keep it that way: a new build step that doesn't depend on the target architecture belongs in a `$BUILDPLATFORM`
stage, and anything architecture-specific must use `TARGETOS`/`TARGETARCH` rather than the build host's values.

## Cache, timeout and CI

- The `images` jobs use the GitHub Actions buildx cache (`type=gha`, one scope per image) and a 30-minute timeout, so a
  stuck build fails instead of running on.
- The `ci` workflow's `docker` job builds both images for both platforms without pushing, with the same cache scopes,
  then loads the plain image for each platform and checks its architecture and `version` output. An arm64 regression
  shows up on the pull request, not on the tag.
