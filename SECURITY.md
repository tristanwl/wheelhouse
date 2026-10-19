# Security policy

Wheelhouse can delete library items and rewrite media files, so treat it like the
other admin tools in your *arr stack.

- Releases 0.0.x to 0.2.x have **no authentication**: run them only on a trusted
  network and do not expose them to the internet.
- Later versions (and the development branch) enforce accounts, sessions, CSRF
  protection and login throttling, and have a login page, a setup wizard and
  account and admin pages. They are still pre-releases (0.x) until 1.0.0, so keep
  them off the internet too and read [docs/operations.md](docs/operations.md)
  before running one. The first start prints a one-time **setup token** in the
  log; it lets whoever knows it create the administrator, so create the
  administrator promptly and keep the log private until you have.
- The release notes of a version say which of these applies. What is deliberately not
  covered (no second factor, no API tokens and so on) is listed under
  [Known limitations](docs/release-1.0-checklist.md#known-limitations).

The [threat model](docs/threat-model.md) lists what Wheelhouse protects, from
whom, which mitigation is in place and which test enforces it, and the risks that
remain; [docs/release-1.0-checklist.md](docs/release-1.0-checklist.md) checks each of
them again before a release. A finding that contradicts it, or a gap it does not list, is worth
reporting.

## Supported versions

Only the newest release receives security fixes. Until 1.0.0 that is the latest
pre-release (0.x): older 0.x versions are not patched, and the fix is to upgrade.
From 1.0.0 on, security fixes go to the latest minor release (for example 1.2.x
once 1.2 is out); older minors are not patched. The `main` branch always
carries the newest fixes but is not a release.

## Reporting a vulnerability

Please do not open a public issue or discussion. Use GitHub's private
vulnerability reporting: **Security, then Report a vulnerability** on this
repository. Include the version or commit, how you run it, and steps to
reproduce. Leave out real API keys, passwords, session cookies and setup tokens:
describe them or replace them with placeholders. If you cannot use GitHub, open a
public issue that only asks for a private contact channel and contains no details.

What to expect (this is a one-person project, so these are targets, not
guarantees):

- an acknowledgement within 7 days;
- an assessment (confirmed, needs more information, or not a vulnerability)
  within 14 days;
- for a confirmed issue, a fix or a mitigation in a release, with a GitHub
  security advisory crediting you if you want to be named, within 90 days at the
  latest, and sooner for serious problems. Please keep the report private until
  the advisory is published, or until the 90 days pass, whichever comes first.

## Verifying what you run

Release images carry signed build provenance and an SBOM from 1.0.0 on.
[docs/verifying-releases.md](docs/verifying-releases.md) shows how to check the
image digest, the provenance and the SBOM, and what that does and does not prove.
The repository settings that protect releases are listed in
[docs/maintainer-settings.md](docs/maintainer-settings.md).
