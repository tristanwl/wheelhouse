---
name: Bug report
about: Something isn't working
labels: bug
---

<!--
SECURITY: do not report vulnerabilities here (issues are public). Use
Security -> Report a vulnerability, see SECURITY.md.
-->

**What happened / what you expected**

**Steps to reproduce**

**Environment**
- Wheelhouse version / commit (`docker exec wheelhouse wheelhouse version`):
- How you run it (Docker, Podman, from source), and whether behind a reverse proxy:
- Signed in as: administrator / user / not signed in (login or setup problem)
- Browser (for UI problems):
- Services and versions (Sonarr, Radarr, SABnzbd, Plex, ...):
- GPU / hardware encode (if relevant):

**Logs**

<!--
Before you paste anything, REMOVE:
- the one-time setup token (the `setup_token=...` line printed at first start,
  and the contents of /config/setup-token),
- session cookies and CSRF tokens (`wheelhouse` or `__Host-wheelhouse`, `X-CSRF-Token`, Cookie
  and Set-Cookie headers in browser dev-tools or curl output),
- API keys, passwords and tokens, including those in URLs (`?apikey=...`),
- real hostnames, IP addresses and library titles you do not want public.
Replace them with placeholders such as `REDACTED`. A setup token stops working
once the administrator exists, but a session cookie or API key does not.
-->
