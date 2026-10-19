# Security policy

Wheelhouse can delete library items and rewrite media files. Releases 0.0.x to
0.2.x have **no authentication**: upgrade, and until then run
them only on a trusted network, never exposed to the internet. From 0.3.0 on, every
route needs a signed-in user, with accounts, sessions, CSRF protection, login
throttling, encrypted service credentials and an audit log; read
[docs/operations.md](docs/operations.md) before running one and put a TLS reverse
proxy in front of it if it is reachable from outside your home network. Versions
before 1.0.0 are pre-releases.
What is deliberately not covered (no second factor, no API tokens and so on) is listed
under [Known limitations](docs/release-1.0-checklist.md#known-limitations).

The [threat model](docs/threat-model.md) lists what Wheelhouse protects, from
whom, which mitigation is in place and which test enforces it, and the risks that
remain; [docs/release-1.0-checklist.md](docs/release-1.0-checklist.md) checks each of
them again before a release. A finding that contradicts it, or a gap it does not list, is worth
reporting.

## Reporting a vulnerability

Please do not open a public issue. Use GitHub's private vulnerability
reporting: **Security → Report a vulnerability** on this repository. Include
steps to reproduce and the version or commit. You can expect an
acknowledgement within a week.
