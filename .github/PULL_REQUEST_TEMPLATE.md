## What and why

## How I tested it
<!-- OS, GPU (if relevant), which services (Sonarr/Radarr/SABnzbd/Plex/...) -->

## Checklist
- [ ] What CI runs for the parts I touched passes locally (`make -C server check`;
      in `web/`: typecheck, lint, format:check, test, build; root: typecheck and
      format:check; acceptance suite; the Docker image if I touched it; see CONTRIBUTING.md)
- [ ] A new API route is in the router table and `acceptance/routes.ts` with its access level
- [ ] Commits are signed off (`git commit -s`) and signed (`-S`)
- [ ] No secrets, tokens, real hostnames or `.env` files included (public repo)
- [ ] Docs updated (README, architecture, threat model) when behaviour or a security boundary changed
- [ ] New dependencies are justified here (why, newest stable at least 2 weeks old, audit clean);
      new actions are pinned by commit SHA and images by digest
