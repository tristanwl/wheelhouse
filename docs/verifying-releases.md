# Verifying a Wheelhouse release

Every release image (`tristanwl/wheelhouse:<version>` on Docker Hub, linux/amd64
and linux/arm64) is built by the [Release workflow](../.github/workflows/release.yml)
from a tag on `main`, on a GitHub-hosted runner, and carries three kinds of
evidence you can check yourself:

| Evidence | What it is | Where it lives |
| --- | --- | --- |
| **Provenance, signed** | A GitHub artifact attestation (SLSA build provenance, signed keyless through Sigstore) saying "this image digest was built by `release.yml` in `tristanwl/wheelhouse` from this commit and tag" | GitHub's attestation store and the Docker Hub repository (pushed next to the image) |
| **Provenance, BuildKit** | BuildKit's own SLSA provenance (`mode=max`: the Dockerfile steps, base image digests, source revision) | An attestation manifest inside the image index on Docker Hub |
| **SBOM** | An SPDX software bill of materials (Alpine packages including ffmpeg, the Go modules in the server binary) | An attestation manifest inside the image index on Docker Hub |

The signed attestation is made over the digest of the **image index**, and the
index lists the per-platform images and the two BuildKit attestation manifests by
digest. So a verified index digest also vouches for the SBOM and the BuildKit
provenance attached to it.

Releases made before the first one that carries attestations (anything up to and
including 0.3.x) have none: the commands below fail for them with "no attestations
found". Versions are tied to tags; there is no `latest` tag.

You need Docker with buildx, and the [GitHub CLI](https://cli.github.com/)
**2.49 or newer** (`gh attestation`).

## 1. Find the digest and pin it

```sh
docker buildx imagetools inspect tristanwl/wheelhouse:1.0.0
```

The `Digest:` line is the index digest (`sha256:...`). The `Manifests:` list shows
a `linux/amd64` and a `linux/arm64` image plus two `unknown/unknown` entries (the
attestation manifests). To make sure you run exactly what you verified, pin the
digest in your Compose file or `docker run` command:

```yaml
image: tristanwl/wheelhouse:1.0.0@sha256:<digest from above>
```

A tag can be moved by whoever controls the Docker Hub account; a digest cannot.
Write the digest down the first time you verify and compare later pulls to it.

## 2. Verify the signed provenance

```sh
gh attestation verify oci://docker.io/tristanwl/wheelhouse:1.0.0 \
  --repo tristanwl/wheelhouse \
  --signer-workflow tristanwl/wheelhouse/.github/workflows/release.yml \
  --source-ref refs/tags/1.0.0
```

This resolves the tag to its digest, downloads the attestation, checks the
Sigstore signature and certificate chain, and requires that the build ran in
`tristanwl/wheelhouse`, in the `release.yml` workflow, for the tag `1.0.0`. It
prints "Verification succeeded" and the matching attestation. To verify a
digest you pinned instead of a tag (the safest form):

```sh
gh attestation verify oci://docker.io/tristanwl/wheelhouse@sha256:<digest> \
  --repo tristanwl/wheelhouse \
  --signer-workflow tristanwl/wheelhouse/.github/workflows/release.yml
```

Add `--format json` to see the certificate and the statement: the commit
(`sourceRepositoryDigest`), the tag (`sourceRepositoryRef`), the workflow run
URL and the runner type. Open the run URL to read the build log.

If verification fails, do not run the image; open an issue (or, if you think the
registry account or the build was compromised, report it privately, see
[SECURITY.md](../SECURITY.md)).

## 3. Inspect the BuildKit provenance and the SBOM

Both are read from the registry; nothing is downloaded but JSON.

```sh
# The SLSA provenance: base image digests, Dockerfile steps, source commit.
docker buildx imagetools inspect tristanwl/wheelhouse:1.0.0 --format '{{ json .Provenance }}'

# The commit the image was built from (compare it with the tag on GitHub):
docker buildx imagetools inspect tristanwl/wheelhouse:1.0.0 --format '{{ json .Provenance }}' \
  | grep -o '"vcs:revision": *"[0-9a-f]*"' | sort -u

# The SBOM (SPDX JSON): save it, then feed it to your scanner of choice.
docker buildx imagetools inspect tristanwl/wheelhouse:1.0.0 --format '{{ json .SBOM }}' > wheelhouse-sbom.json
grep -A1 '"name": "ffmpeg"' wheelhouse-sbom.json
```

For a multi-architecture image both commands print one entry per platform. SBOM
scanners such as `grype sbom:./wheelhouse-sbom.json` or `trivy sbom` read the
SPDX document directly. These two attestations are BuildKit's and are not
signed on their own; they are covered by the signed index digest from step 2,
which is why you should run step 2 first and always use the same digest.

The provenance records the Dockerfile's base images by digest, the build steps
and the commit. The workflow passes no build arguments or secrets into the
build, and the published documents were checked for host paths and credentials
(none; see [maintainer-settings.md](maintainer-settings.md#checking-a-release-build-without-publishing)).

## What this does and does not prove

- It proves the image digest was produced by this repository's release workflow
  from a tagged commit on `main`, and which base images and packages went in.
- It does not prove the source is free of bugs or malicious code: review is the
  job of the project's code review, tests and `govulncheck`/`npm audit` in CI.
- The Alpine packages (ffmpeg and the GPU drivers) are installed from Alpine's
  repositories at build time and are not pinned to exact versions, so rebuilding
  the same commit later can produce a different image. The Go binary itself is
  built reproducibly (same source and toolchain give the same bytes), but the
  image as a whole is not bit-for-bit reproducible. The SBOM records exactly
  which package versions this image has.
- Docker Hub, GitHub and Sigstore are trusted to run honestly: the signature
  does not protect you if the maintainer's GitHub account or the release
  environment is compromised, which is why the maintainer settings in
  [maintainer-settings.md](maintainer-settings.md) restrict who can create tags
  and use the Docker Hub token.

## Why there is no separate cosign signature

The attestation is already a keyless Sigstore signature whose certificate names
the workflow, repository, commit and tag, and `gh attestation verify` checks it
against the same identity a `cosign verify` would. Adding a second `cosign sign`
signature would be a second mechanism to maintain and document without proving
anything more, and it cannot be tested without publishing a release. If you
prefer cosign, the attestation's Sigstore bundle can be fetched from the
registry or `gh attestation download`; the identity to require is
`https://github.com/tristanwl/wheelhouse/.github/workflows/release.yml@refs/tags/<version>`
with issuer `https://token.actions.githubusercontent.com`. That route has not
been tested against a real release and is not part of the supported procedure.
