# Maintainer settings

Workflows and files in the repository cannot configure everything that protects a
release: these settings live in GitHub and only the owner can change them. This is
the exact list, with the UI path, a `gh api` command for each, and a read-only
command to check it afterwards. `OWNER/REPO` below is `tristanwl/wheelhouse`;
`gh` must be signed in as an admin (`gh auth status`) and recent (2.49 or newer).

Nothing in the repository applies these automatically. Run the write commands
yourself, one at a time, and check each with its verify command.

## 1. Branch ruleset for `main`

Required status checks `check`, `server`, `web`, `acceptance` and `docker` (the job
names in [ci.yml](../.github/workflows/ci.yml); never rename them), a pull request
before merging, signed commits, no force pushes or deletion, merge commits only.

UI: Settings, Rules, Rulesets, New ruleset, New branch ruleset. Target `main`,
enforcement Active, no bypass list.

```sh
gh api -X POST repos/OWNER/REPO/rulesets --input - <<'JSON'
{
  "name": "main",
  "target": "branch",
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": { "ref_name": { "include": ["refs/heads/main"], "exclude": [] } },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    { "type": "required_signatures" },
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 1,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": false,
        "require_last_push_approval": false,
        "required_review_thread_resolution": true,
        "allowed_merge_methods": ["merge"]
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "required_status_checks": [
          { "context": "check" },
          { "context": "server" },
          { "context": "web" },
          { "context": "acceptance" },
          { "context": "docker" }
        ]
      }
    }
  ]
}
JSON
```

Decision for you: `required_approving_review_count`. With a single maintainer
nobody can approve your own pull request, so `1` blocks every merge unless you
add a bypass actor (or a second reviewer). Use `0` (a pull request and green
checks are still required) until there is a second maintainer, then `1`. The
merge commit GitHub creates in the web UI is signed by GitHub, so
`required_signatures` does not block merging in the UI; commits pushed to a
branch need your own signature (`git commit -S`), as CONTRIBUTING.md asks.

Verify (read-only):

```sh
gh api repos/OWNER/REPO/rulesets --jq '.[] | [.id, .name, .target, .enforcement] | @tsv'
gh api repos/OWNER/REPO/rules/branches/main --jq '.[].type'
# expect: deletion, non_fast_forward, required_signatures, pull_request, required_status_checks
gh api repos/OWNER/REPO/rules/branches/main \
  --jq '.[] | select(.type=="required_status_checks") | .parameters.required_status_checks[].context'
```

## 2. Tag ruleset for release tags

Release tags (`0.3.0`, `1.0.0`, ...) must never be moved or deleted, and only an
admin may create one. UI: Settings, Rules, Rulesets, New ruleset, New tag ruleset.

```sh
gh api -X POST repos/OWNER/REPO/rulesets --input - <<'JSON'
{
  "name": "release tags",
  "target": "tag",
  "enforcement": "active",
  "bypass_actors": [
    { "actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always" }
  ],
  "conditions": { "ref_name": { "include": ["refs/tags/[0-9]*"], "exclude": [] } },
  "rules": [
    { "type": "creation" },
    { "type": "update" },
    { "type": "deletion" },
    { "type": "non_fast_forward" }
  ]
}
JSON
```

`actor_id: 5` is the built-in Admin repository role. The `creation` rule blocks
everyone except the bypass list from creating a matching tag. A bypass actor
bypasses every rule in a ruleset, so an admin could in theory still move or
delete a release tag: never do that (a published tag and its image digest must
stay the same). If you want that to need a deliberate step, drop the bypass
list and edit the ruleset (disable it, create the tag, enable it) for each
release instead. Make the tag from `main` (`git tag -s 1.0.0 && git push origin
1.0.0`, or create the release in the UI).

Limit to know about: GitHub rulesets cannot say "create tags only from commits on
`main`". That is enforced a different way: the first step of
[release.yml](../.github/workflows/release.yml) fails the run unless the tagged
commit is on `origin/main` and equals `package.json`'s version, and the `release`
environment below only accepts runs for tags. So a tag on another branch builds
nothing and publishes nothing.

Verify (read-only; the pattern is fnmatch, check that it shows `[0-9]*`):

```sh
gh api repos/OWNER/REPO/rulesets --jq '.[] | select(.target=="tag") | {id, name, enforcement}'
gh api repos/OWNER/REPO/rulesets/<id> --jq '{include: .conditions.ref_name.include, rules: [.rules[].type], bypass: .bypass_actors}'
```

## 3. The `release` environment and `DOCKERHUB_PAT`

The Docker Hub token must only be readable by a run for a release tag, never by a
pull request or a branch. The `publish` job in release.yml declares
`environment: release`. UI: Settings, Environments, New environment `release`:
Deployment branches and tags, Selected branches and tags, Add deployment rule,
type Tag, pattern `[0-9]*`. Optionally add yourself under Required reviewers (a
click to approve each release; leave "Prevent self-review" off while you are
the only maintainer).

```sh
gh api -X PUT repos/OWNER/REPO/environments/release --input - <<'JSON'
{
  "deployment_branch_policy": { "protected_branches": false, "custom_branch_policies": true }
}
JSON
gh api -X POST repos/OWNER/REPO/environments/release/deployment-branch-policies \
  -f name='[0-9]*' -f type=tag
gh secret set DOCKERHUB_PAT --env release        # paste the token when asked
gh secret delete DOCKERHUB_PAT                   # remove the repository-level copy
```

Do the last two in this order, and only after the next release no longer needs the
old secret. Keep the repository variable `DOCKERHUB_USERNAME` as it is. Use a Docker
Hub access token limited to this one repository with Read & Write (not Delete),
and rotate it when anyone who had access leaves. A manual dry run (below) uses a
second environment, `release-dry-run`, which GitHub creates on first use; keep it
free of secrets and protection rules.

Verify:

```sh
gh api repos/OWNER/REPO/environments/release --jq '{policy: .deployment_branch_policy, rules: [.protection_rules[].type]}'
gh api repos/OWNER/REPO/environments/release/deployment-branch-policies --jq '.branch_policies[] | [.name, .type] | @tsv'
gh secret list --env release       # DOCKERHUB_PAT listed
gh secret list                     # DOCKERHUB_PAT must NOT be listed here
gh secret list --env release-dry-run   # empty
```

## 4. Repository settings

```sh
# merge commits only, delete head branches after merge
gh api -X PATCH repos/OWNER/REPO \
  -F delete_branch_on_merge=true -F allow_merge_commit=true \
  -F allow_squash_merge=false -F allow_rebase_merge=false

# secret scanning and push protection
gh api -X PATCH repos/OWNER/REPO --input - <<'JSON'
{ "security_and_analysis": {
    "secret_scanning": { "status": "enabled" },
    "secret_scanning_push_protection": { "status": "enabled" } } }
JSON

# Dependabot alerts and security updates (version updates come from dependabot.yml)
gh api -X PUT repos/OWNER/REPO/vulnerability-alerts
gh api -X PUT repos/OWNER/REPO/automated-security-fixes

# private vulnerability reporting (what SECURITY.md tells reporters to use)
gh api -X PUT repos/OWNER/REPO/private-vulnerability-reporting

# Actions: the default token is read-only and cannot approve pull requests
gh api -X PUT repos/OWNER/REPO/actions/permissions/workflow \
  -f default_workflow_permissions=read -F can_approve_pull_request_reviews=false

# Actions: only these actions may run (all are used by the workflows)
gh api -X PUT repos/OWNER/REPO/actions/permissions -F enabled=true -f allowed_actions=selected
gh api -X PUT repos/OWNER/REPO/actions/permissions/selected-actions --input - <<'JSON'
{ "github_owned_allowed": false, "verified_allowed": false,
  "patterns_allowed": ["actions/*", "docker/*", "golangci/golangci-lint-action@*"] }
JSON
```

UI paths: Settings, General (merge options, "Automatically delete head
branches"); Settings, Code security (Dependabot alerts and security updates,
Private vulnerability reporting, Secret scanning, Push protection); Settings,
Actions, General ("Actions permissions", "Workflow permissions", and "Fork pull
request workflows": set it to require approval for all outside collaborators).
If your Settings, Actions page offers "Require actions to be pinned to a full-length
commit SHA", turn it on: it enforces what `scripts/check-pins.sh` checks.
`github_owned_allowed` is false because `actions/*` is in the patterns anyway;
add a pattern when a workflow starts using a new action, and keep it as narrow
as the workflows need.

Verify:

```sh
gh api repos/OWNER/REPO --jq '{delete_branch_on_merge, allow_merge_commit, allow_squash_merge, allow_rebase_merge, security_and_analysis}'
gh api repos/OWNER/REPO/private-vulnerability-reporting                  # {"enabled":true}
gh api repos/OWNER/REPO/automated-security-fixes                         # {"enabled":true,...}
gh api repos/OWNER/REPO/vulnerability-alerts --include | head -1         # HTTP 204 = enabled
gh api repos/OWNER/REPO/actions/permissions/workflow                     # read, false
gh api repos/OWNER/REPO/actions/permissions                              # selected
gh api repos/OWNER/REPO/actions/permissions/selected-actions
```

## What the workflows already do (no setting needed)

- Every workflow's default token is `contents: read`; only the `publish` job of
  release.yml is granted more (`contents: write` for the GitHub release,
  `id-token: write` and `attestations: write` for the signed provenance).
- Every action is pinned to a full commit SHA and every image to a digest;
  `scripts/check-pins.sh` runs in the `check` job and fails the pull request
  otherwise. Exceptions go in `scripts/check-pins.allow` with a reason.
- Dependabot ([dependabot.yml](../.github/dependabot.yml)) waits 7 days after a
  release before proposing it and groups the web tooling and the actions.
  Security updates are not delayed.

## Pins Dependabot cannot bump (do these by hand)

- **The QEMU binfmt image digest**
  (`docker.io/tonistiigi/binfmt:qemu-v...@sha256:...`) in the `docker/setup-qemu-action`
  steps of ci.yml and release.yml. Pick a `qemu-v*` tag at least two weeks old,
  take its digest (`docker buildx imagetools inspect tonistiigi/binfmt:<tag>`), and
  change both files together.
- **golangci-lint** (`version:` in ci.yml) and the `go run ...@version` tools
  (govulncheck in ci.yml, go-licenses in ci.yml and `server/Makefile`).
- **The Alpine packages** installed in the Dockerfile (ffmpeg and drivers) are not
  version-pinned: the image takes what the pinned Alpine release has at build time.
  The SBOM of each release records the exact versions.

## Checking a release build without publishing

The release workflow has a manual dry run: it builds both platforms with SBOM and
provenance enabled, checks the result, and neither pushes, signs nor creates a
release.

```sh
gh workflow run Release --ref <branch> -f dry_run=true
gh run watch
```

The same build on your machine (needs a `docker-container` buildx builder, not
the default one):

```sh
docker buildx create --name dryrun --driver docker-container --bootstrap
docker buildx build --builder dryrun --platform linux/amd64 \
  --sbom=true --provenance=mode=max \
  --output type=oci,dest=/tmp/wheelhouse-oci.tar .
mkdir /tmp/wheelhouse-oci && tar -xf /tmp/wheelhouse-oci.tar -C /tmp/wheelhouse-oci
# index.json -> the image index -> the "attestation-manifest" entry -> its two
# layers (application/vnd.in-toto+json): the SPDX SBOM and the SLSA provenance.
docker buildx rm dryrun
```

When this was last checked (Phase 2 hardening), the SPDX SBOM listed the Alpine
packages and Go modules, and the provenance recorded the base image digests, the
Dockerfile steps and the git revision; neither document contained a host path, a
build argument, an environment variable of the build machine or a credential.
Repeat the check (search both JSON documents for `/home`, `token`, `password`, the
name of your machine) when the Dockerfile gains build arguments or secret
mounts, because `provenance=mode=max` records them.
