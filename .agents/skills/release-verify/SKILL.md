---
name: release-verify
description: >-
  Prepare or verify a Skopos release: preflight, tag rules, signatures,
  attestations, SBOMs, image and chart digests, and a chart render. Use when
  cutting a release, verifying a published release, or changing release or
  signing documentation.
license: Apache-2.0
---

# Release verify

Releases are tag-only and immutable. The source of truth is `docs/release.md`
and `.github/workflows/release.yml`; this skill is the checklist.

## When to use

- Preparing the next annotated `vX.Y.Z` tag.
- Verifying a published release on a clean machine.
- Changing the release workflow, signing, SBOM, or chart distribution.

Do not use this skill to publish from a branch, re-tag a release, or add a
manual publish input.

## Inputs

- The intended tag, for example `v0.1.0-alpha.3`.
- The repository, image repository, and chart reference. The defaults come from
  the environment and `docs/release.md`.
- A clean checkout of the commit that will be tagged.

## Preflight

1. Confirm `deploy/helm/skopos/Chart.yaml` has `version` and `appVersion` equal
   to the tag without `v`.
2. Confirm the commit is on `main` and CI is green.
3. Confirm there are no open high or critical code-scanning alerts:
   `go run ./tools/repoctl codescan`.
4. Confirm `docs/security/osv-exceptions.yaml` is empty, or every exception has
   a reason and an unexpired date: `go run ./tools/repoctl osv`.
5. Run the release preflight:

```sh
make release-check
go test -race ./...
```

`make release-check` covers vet, tests, Helm, modules, and script syntax. The
race run is the release gate, not only the local unit default.

## Cut the release

```sh
git switch main
git pull --ff-only
git tag -a vX.Y.Z -m "Skopos vX.Y.Z"
git push origin vX.Y.Z
```

The workflow then verifies the tag is annotated and an ancestor of `main`,
builds the multi-architecture image and chart, signs them with keyless Sigstore,
attests provenance and SBOMs, writes `values.release.yaml`, and publishes an
immutable GitHub release.

## Verify a published release

```sh
make release-verify TAG=vX.Y.Z
```

The verifier downloads `values.release.yaml` from the release and checks:

- the image and chart signatures chain to the release workflow and the GitHub
  Actions OIDC issuer;
- provenance attestations exist for both artifacts;
- the release publishes both SPDX SBOMs;
- the image digest in `values.release.yaml` matches the published image;
- the chart renders with the pinned values.

Also inspect the release assets: chart `.tgz`, `release-metadata.txt`,
`SHA256SUMS`, signature bundles, and both SBOMs. Missing assets are a failed
verification, not a partial pass.

## Verification

```sh
go run ./tools/repoctl codescan
go run ./tools/repoctl osv
make release-check
go test -race ./...
make release-verify TAG=vX.Y.Z
```

A pass means every command above succeeds and the release asset list is
complete. If a tool is unavailable, report the skipped check and do not claim
the release is verified.

## Guardrails

- No manual publish path, no mutable tag, no re-tag after publication.
- Do not run the release workflow from a pull request or a branch.
- The release environment, tag ruleset, and signing identity are human gates.
  Do not weaken them to make a release pass.
- A version bump is a separate pull request from a feature or fix.
- If the process changes, update `docs/release.md` and this skill in the same
  pull request.

## References

- `docs/release.md`
- `docs/chart-repository.md`
- `.github/workflows/release.yml`
- `scripts/release/verify-release.sh`
- `tools/repoctl/internal/releaseverify`
