# Security policy

Wheelhouse can delete library items and rewrite media files. The released 0.2.x
has **no authentication**: run it only on a trusted network and do not expose it
to the internet. The development branch enforces accounts, sessions, CSRF
protection and login throttling on the server (its web UI has a login page, a
setup wizard and an account page; no release with it exists yet); see [docs/operations.md](docs/operations.md)
for running it and the [Phase 2 plan](docs/wheelhouse-phase2-plan.md) for what is
still to come.

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
