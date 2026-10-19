# Security policy

Wheelhouse can delete library items and rewrite media files. Releases 0.0.x to
0.2.x have **no authentication**: run them only on a trusted network and do not
expose them to the internet. Later versions (and the development branch) enforce
accounts, sessions, CSRF protection and login throttling, and have a login page,
a setup wizard and an account page; they are still pre-releases (0.x) until the
[Phase 2 plan](docs/wheelhouse-phase2-plan.md) is finished, so keep them off the
internet too and read [docs/operations.md](docs/operations.md) before running one.
Check the release notes of the version you run to see which applies.

The [threat model](docs/threat-model.md) lists what Wheelhouse protects, from
whom, which mitigation is already in place, which comes with which planned
change ([Phase 2 plan](docs/wheelhouse-phase2-plan.md)), and the risks that
remain. A finding that contradicts it, or a gap it does not list, is worth
reporting.

## Reporting a vulnerability

Please do not open a public issue. Use GitHub's private vulnerability
reporting: **Security → Report a vulnerability** on this repository. Include
steps to reproduce and the version or commit. You can expect an
acknowledgement within a week.
