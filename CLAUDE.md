# Z'AM fork of evcc: rules for agents and humans

@AGENTS.md
@CONTRIBUTING.md

The evcc rules above (`AGENTS.md`, `CONTRIBUTING.md`) apply in full to this
fork: code style, comment style, tests, generators, commit subjects
`Component: short description`, PR descriptions, AI attribution. The Z'AM
team rules (internal `CLAUDE.md` of the app repository) apply on top. If two
rules conflict, the stricter one wins; when in doubt, stop and ask.

## Branches

- `master`: mirror of `evcc-io/evcc` master. Never commit to it.
- `zam-main`: our line = upstream master + Z'AM features. It is an
  integration branch only and is never installed on a site. Never commit or
  push to it directly, never `--force`.
  Changes only via pull request against `zam-main`; the repository owner
  decides merges.
- Feature branches: `zam/<topic>`, branched from `zam-main`.
- Pulling upstream: merge `evcc-io/evcc` master into `zam-main` via pull
  request, no rebase.

## Spec first

Every feature needs an approved spec in the Z'AM app repository before
code is written. No spec or an unclear one: stop and ask. Every pull
request names its spec.

## Build upstream-ready

- Cut every feature so it can become its own pull request to
  `evcc-io/evcc`: one topic, English, no Z'AM-specific naming in code, and
  never include this file in a feature commit.
- Open a pull request at evcc only when the related issue or discussion
  shows a positive tendency from the team (CONTRIBUTING "Discuss first").
  Issues, pull requests and comments at evcc are posted by the repository
  owner or with their explicit approval.
- Commit messages without `Co-Authored-By`; pull request texts carry the AI
  footer (AGENTS.md "AI Attribution").

## Operations

- No evcc change reaches a site without a deliberate, checked decision for
  that site: not an upstream sync, not a fork build, not an apt upgrade.
- Only an explicit release tag can be installed, and each site pins its own
  version. A new tag changes no site until its pin is raised.
- Every upstream sync is its own pull request that lists upstream's breaking
  changes and what they mean for the sites.
- Fork builds go to one internal test site first; other sites only after a
  successful field test and approval.
- Device access, sudo, restarts and updates on site hardware are done by
  the repository owner. Access details live in internal docs, never here.
- Never commit secrets or site data: no sponsor token, no site `evcc.yaml`
  or `evcc.db`, no passwords, hostnames or IP addresses.
