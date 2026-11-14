# Release process

Skopos uses one annotated `vX.Y.Z` tag to release both the gateway image and
the Helm chart. There is no manual publish path. Re-running a failed release
means creating a new patch tag; published releases are immutable.

## What a release produces

- `ghcr.io/<owner>/skopos-gateway:<version>` with an immutable digest. The tag
  is a multi-architecture manifest for `linux/amd64` and `linux/arm64`.
- `oci://ghcr.io/<owner>/charts/skopos:<version>` with an immutable digest.
- `skopos-<version>.tgz`: the packaged Helm chart.
- `skopos-gateway-<version>.spdx.json` and
  `skopos-chart-<version>.spdx.json`: SBOMs.
- `values.release.yaml`: the chart values that pin the released image digest.
- `release-metadata.txt` and `SHA256SUMS`.
- Cosign Sigstore bundles for the image and chart.
- GitHub artifact attestations for provenance and SBOMs.

## Before tagging

1. Update `deploy/helm/skopos/Chart.yaml`:
   - `version` must equal the tag without `v`.
   - `appVersion` must equal the tag without `v`.
2. Update the default image tag if needed. Release installs use
   `values.release.yaml`, not the chart default.
3. Merge the version bump through a pull request with all required checks.
4. Confirm that the commit is on `main` and CI is green.
5. Confirm that there are no open high or critical code-scanning alerts. The
   release preflight runs `go run ./tools/repoctl codescan`, which fails closed
   on API errors and names every blocking alert.
6. Confirm that `docs/security/osv-exceptions.yaml` is empty or that every
   exception has a reason and an unexpired date. The release preflight runs
   `go run ./tools/repoctl osv`.

## Create the release

```sh
git switch main
git pull --ff-only
git tag -a v0.0.1-alpha -m "Skopos v0.0.1-alpha"
git push origin v0.0.1-alpha
```

`.github/workflows/release.yml` then:

1. Verifies that the tag is annotated, is an ancestor of `main`, and that the
   tag, release, and image tag do not already exist.
2. Runs `go run ./tools/repoctl codescan`, `go run ./tools/repoctl osv`,
   `make release-check`, and `go test -race ./...`.
3. Builds the chart and SBOMs in a read-only job and validates the local
   image build.
4. Builds the `linux/amd64` and `linux/arm64` image manifests with Buildx,
   pushes the manifest list, signs it with keyless Sigstore, and attests
   provenance and SBOM.
5. Pushes the chart to GHCR OCI, signs it, and attests provenance and SBOM.
6. Generates `values.release.yaml` with the image digest.
7. Creates a draft GitHub release, attaches every asset, and publishes it as an
   immutable release.

If any signature or attestation verification fails, the GitHub release is not
created.

## Verify a release

```sh
make release-verify TAG=v0.0.1-alpha
```

The verifier downloads `values.release.yaml` from the published release and
checks that:

- the image and chart signatures chain to
  `.github/workflows/release.yml` at the release tag and the GitHub Actions OIDC
  issuer;
- GitHub artifact attestations exist for both the image and the chart, signed by
  that workflow;
- the release publishes `skopos-gateway-<version>.spdx.json` and
  `skopos-chart-<version>.spdx.json`;
- the image digest in `values.release.yaml` equals the digest reported by
  `cosign verify`.

It then renders the chart with the downloaded values. Every missing or
mismatched artifact fails the command. `VERSION=...` and a positional version
remain accepted by `scripts/release/verify-release.sh`.

Or manually:

```sh
cosign verify ghcr.io/mrofisr/skopos-gateway:0.0.1-alpha \
  --certificate-identity 'https://github.com/mrofisr/skopos/.github/workflows/release.yml@refs/tags/v0.0.1-alpha' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

cosign verify ghcr.io/mrofisr/charts/skopos:0.0.1-alpha \
  --certificate-identity 'https://github.com/mrofisr/skopos/.github/workflows/release.yml@refs/tags/v0.0.1-alpha' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

gh attestation verify oci://ghcr.io/mrofisr/skopos-gateway:0.0.1-alpha \
  --repo mrofisr/skopos \
  --signer-workflow mrofisr/skopos/.github/workflows/release.yml

gh release view v0.0.1-alpha \
  --repo mrofisr/skopos \
  --json assets
```

Compare the digest reported by `cosign verify` with the `digest:` line in the
release's `values.release.yaml`.

Confirm that the tag is a multi-architecture manifest:

```sh
docker buildx imagetools inspect \
  ghcr.io/mrofisr/skopos-gateway:0.0.1-alpha
```

The digest in `values.release.yaml` and the `cosign verify` output is the
manifest-list digest, not a single-platform image digest.

The SBOM asset is generated from the build job's local image. Each platform
image is a scratch image containing one statically linked Go binary plus the CA
bundle, so the Go module set and SPDX inventory are the same on
`linux/amd64` and `linux/arm64`; the attestation is attached to the
multi-architecture manifest digest.

Do not use `--insecure-ignore-tlog`. The release workflow always records
signatures in the transparency log.

## Install a release

The gateway is disabled by default. A release install uses the attached
`values.release.yaml`, which enables the gateway and pins its digest:

```sh
helm install skopos oci://ghcr.io/mrofisr/charts/skopos \
  --version 0.0.1-alpha \
  -f values.release.yaml \
  --namespace observability \
  --create-namespace
```

Download `values.release.yaml` from the GitHub release. Verify the OCI chart
before installing when your policy requires it.

## Quickstart doc-test

`make quickstart-test` is the EC5 evidence command. It runs the shell block
tagged `quickstart` in the README verbatim against the published chart:

1. `go run ./tools/repoctl quickstart` extracts the tagged block and requires exactly one
   `oci://` chart reference and one strict-SemVer `--version`.
2. It runs `helm show chart <ref> --version <version>` and fails before creating
   a cluster when the chart version is not in the registry.
3. The harness downloads `values.release.yaml` from the matching GitHub release,
   creates a clean k3d cluster with a private kubeconfig, runs the extracted
   block verbatim, and checks the ClickHouse StatefulSet and gateway Deployment
   roll out.

Requires `go`, `helm`, `k3d`, `kubectl`, and an authenticated `gh`. The run is
intentionally red until the chart version named in the README is published;
`SKOPOS_QUICKSTART_KEEP=1` keeps the cluster for inspection.

## Local dry run

`make release VERSION=0.0.1-alpha` builds the image, chart, SBOMs, metadata,
and checksums locally. It does not push, sign, or create a release. The local
image is built for the host architecture for SBOM generation and the dry run;
the release workflow builds and pushes the `linux/amd64` and `linux/arm64`
manifest list.

The repository no longer supports key-based signing or manual dispatch of a
release. Do not add either back.
