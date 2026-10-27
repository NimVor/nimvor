# Installer review: bootstrap, install, update, upgrade

Scope: `installer/bootstrap.sh` (served at nimvor.com/install), `install.sh`,
`update.sh`, `upgrade.sh`. Reviewed for the rename PR; findings marked
**fixed** are in that PR, the rest are open recommendations.

## How an install flows

`curl -fsSL https://nimvor.com/install | sudo bash` → `bootstrap.sh` → source
from `https://github.com/NimVor/nimvor` into `/usr/local/src/nimvor` → `install.sh`
(dependencies from the distribution, a pinned Go toolchain, the agent binary
built locally, panel files, `/etc/nimvor`, systemd units).

## What is trusted

* The TLS connection to `nimvor.com` (the bootstrap script) and to GitHub (the source).
* The distribution's package repositories; on request Ondřej Surý's PPA
  (pinned signing key) or Remi's repository; `--no-third-party-repos` avoids both.
* `dl.google.com` for the Go toolchain (see C).

## Findings

| | Finding | Status |
|---|---|---|
| 1 | **Silent fallback to `main`.** The release listing's errors were discarded, so a network failure installed the development branch instead of a release. | **fixed**: a failed listing stops the install |
| 2 | **Pre-releases sorted above stable tags** (`v1.0.0-rc1` after `v1.0.0` in a version sort). | **fixed**: the newest stable tag wins; a pre-release only when there is no stable one, with a warning |
| 3 | **Root ran code from a checkout directory it did not vouch for.** A pre-existing `/usr/local/src/nimvor` owned by or writable by another account would have been executed as root. | **fixed**: the folder and its `.git` must be root-owned, not group/world-writable and not a symlink |
| 4 | **Any git source accepted**, including local paths. | **fixed**: only `https://` and ssh URLs (local paths only with `NIMVOR_ALLOW_LOCAL_SOURCE=1`, used by the tests) |
| 5 | **Branch installs left a detached HEAD**, so `update.sh` and the panel's update check (which need a tracking branch) failed. | **fixed**: branch installs track `origin/<branch>`; a release install is a detached tag and `update.sh` says how to change release |
| 6 | **The installer could be run on a server that still has the old install**, leaving two panels over one set of websites. | **fixed**: refuses when `/opt/whacp`, `/etc/whacp` or `whacp-agent.service` exist and points to the migration plan |
| A | **No cryptographic check of the source beyond TLS + GitHub account security.** Anyone who can push a tag or take over the GitHub org or nimvor.com gets root on every new install. | open. Interim: `--commit SHA` (the install stops unless the fetched source is exactly that commit); publish the commit id with each release. Recommended: signed tags (`git tag -s` or SSH signatures) verified by the bootstrap script against a public key embedded in it; branch protection and 2FA for the NimVor org |
| B | **`curl \| bash` integrity** rests on nimvor.com's TLS only. | open. Publish the script's SHA-256 and document `curl -fsSLO …/install && less install && sudo bash install` as the careful route |
| C | **Go toolchain checksum comes from the same origin as the tarball** (`dl.google.com/go/*.sha256`): it catches corruption, not a compromised mirror. | open. Pin the SHA-256 of the exact Go release in `install.sh` (I could not fetch the published digests from the build sandbox, so none were pinned here) |
| D | **The generated administrator password is printed to the terminal** at the end (and the agent token with it for remote setups): it lands in scrollback and in any log that captures the run. | open. Write it to a root-only (0600) file and print the path, or force a change at first sign-in |
| E | **A release-pinned install cannot self-update from the panel** (no branch to pull). | open. Teach `update.sh` and the update check to move to the newest stable tag |
| F | **`git fetch --depth 50`** keeps clones small. `git pull --ff-only` in `update.sh` works on it and refuses (safely) if the branch history was rewritten. | accepted |

## Reliability and permissions (reviewed, no change needed)

* `set -Eeuo pipefail`, root check, `--dry-run`, and `--reinstall` / `--resume` for idempotent repair.
* An existing install is never overwritten without `--reinstall`; configuration, agent token, panel key and database are kept.
* `umask 027`; secrets are created with `umask 077` and end up `0640 root:nimvor`; the panel tree is `root:nimvor` with no access for others.
* `upgrade.sh` backs up files, units, configuration and the database (integrity-checked, checksummed) before changing anything, restores on failure, supports `--rollback`, keeps three backups and refuses a downgrade without `--allow-downgrade`.
* The installer does not modify existing Nginx, PHP-FPM, database, firewall or SSH configuration except through the panel's own actions.
