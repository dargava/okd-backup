# okd-backup

> CLI tool for backing up and restoring OKD / OpenShift clusters to NFS or local storage.

![Go](https://img.shields.io/badge/go-1.21%2B-00ADD8)
![Version](https://img.shields.io/badge/version-0.9.4-green)
![License](https://img.shields.io/badge/license-MIT-lightgrey)

---

## Table of contents

- [Features](#features)
- [Requirements](#requirements)
- [Installation](#installation)
- [Configuration](#configuration)
- [Quick start](#quick-start)
- [Commands](#commands)
  - [backup](#backup)
  - [list](#list)
  - [restore list](#restore-list)
  - [restore run](#restore-run)
  - [info](#info)
  - [config](#config)
  - [detect](#detect)
  - [deps](#deps)
  - [schedule](#schedule)
    - [schedule generate](#schedule-generate)
    - [schedule list](#schedule-list)
    - [schedule remove](#schedule-remove)
  - [cleanup](#cleanup)
  - [completion](#completion)
- [Can I use this for a full cluster restore?](#can-i-use-this-for-a-full-cluster-restore)
- [Backup storage layout](#backup-storage-layout)
- [What gets backed up](#what-gets-backed-up)
- [Limitations](#limitations)
- [Shell completion](#shell-completion)
- [Third-party licenses](#third-party-licenses)
- [Contributing](#contributing)

---

## Features

- **etcd snapshot** — full cluster state via `cluster-backup.sh` on the control plane (SSH)
- **Namespace resources** — all Kubernetes/OpenShift resources exported as YAML per namespace
- **PVC data** — persistent volume contents copied via `oc rsync` using a temporary pod
- **Cluster-wide config** — OAuth, ingress, operators, CRDs, ClusterRoles, StorageClasses
- **Selective restore** — restore a single namespace, a single resource type, or everything
- **Namespace mapping** — restore into a different namespace (e.g. `production` → `staging`)
- **Config file** — set defaults for `backup_dir`, `control_plane`, `ssh_key`, `ssh_user`
- **Auto-detect control plane** — finds and probes control plane nodes automatically
- **Scheduled backups** — generates a systemd timer or crontab entry, with optional `--install`
- **Cleanup** — remove old backups by age, count, ID, or empty/failed state
- **Dry-run mode** — simulate any operation without making changes
- **Verbose mode** — show all `oc`/`kubectl` commands as they run
- **Dependency check** — `okd-backup deps` lists all required tools and verifies they are installed
- **Shell completion** — tab-completion for bash and zsh
- **Single static binary** — built with Go

---

## Requirements

| Requirement | Notes |
|---|---|
| `oc` or `kubectl` | Must be in `PATH` and logged into the cluster |
| SSH access to a control plane node | Required for `--etcd` only |
| NFS mount or local directory | Backup storage destination |

---

## Installation

okd-backup is a single static Go binary. Build it from source:

```bash
# Build
git clone <repo-url>
cd okd-backup
go build -ldflags="-s -w" -o okd-backup .

# Install system-wide
sudo cp okd-backup /usr/local/bin/okd-backup
okd-backup version
```

Or download a pre-built binary (if available) and copy it directly:

```bash
chmod +x okd-backup
sudo cp okd-backup /usr/local/bin/okd-backup
```

---

## Configuration

okd-backup can read defaults from a config file so you don't have to pass
`--backup-dir`, `--control-plane`, and `--ssh-key` on every command.

### Quick setup

```bash
okd-backup config set backup_dir /mnt/nfs/okd-backups
okd-backup config set control_plane 192.168.1.10
okd-backup config set ssh_key ~/.ssh/okd_key
```

Or copy the included example file and edit it:

```bash
cp okd-backup.yaml.example ~/.config/okd-backup/config.yaml
```

### Config file format

```yaml
backup_dir: /mnt/nfs/okd-backups
control_plane: 192.168.1.10
ssh_key: ~/.ssh/okd_key
ssh_user: core
```

### Search order

The config file is found by checking these locations in order (first match wins):

| Priority | Source |
|---|---|
| 1 | `--config <path>` CLI flag |
| 2 | `$OKD_BACKUP_CONFIG` environment variable |
| 3 | `./okd-backup.yaml` (current directory) |
| 4 | `~/.config/okd-backup/config.yaml` (user home) |

CLI flags always override config file values.

---

## Quick start

```bash
# Set up config once
okd-backup config set backup_dir /mnt/nfs/okd-backups
okd-backup config set control_plane 192.168.1.10
okd-backup config set ssh_key ~/.ssh/okd_key

# Back up everything
okd-backup backup --all

# List available backups
okd-backup list

# Restore a single namespace
okd-backup restore run --backup-id 2024-01-15_0200 --namespace production
```

---

## Commands

### backup

Create a backup of the OKD cluster.

```
okd-backup backup [OPTIONS]
```

| Option | Default | Description |
|---|---|---|
| `--all` | — | Back up everything |
| `--etcd` | — | etcd snapshot (requires `--control-plane`) |
| `--namespaces TEXT` | — | Comma-separated list of namespaces |
| `--pvcs` | — | PVC data |
| `--cluster-config` | — | OAuth, ingress, operators, CRDs |
| `--no-secrets` | — | Skip secrets |
| `--backup-dir TEXT` | config / cwd | Storage location |
| `--control-plane TEXT` | config | Hostname or IP of a control plane node |
| `--ssh-key TEXT` | config / `~/.ssh/id_rsa` | SSH private key |
| `--ssh-user TEXT` | config / `core` | SSH user |
| `--dry-run` | — | Simulate without changes |
| `--verbose` | — | Show `oc`/`kubectl` output |
| `--config PATH` | — | Path to config file |

**Examples:**

```bash
# Everything (using values from config file)
okd-backup backup --all

# etcd only with explicit options
okd-backup backup --etcd --control-plane 192.168.1.10 --ssh-key ~/.ssh/okd_key

# Specific namespaces, skip secrets
okd-backup backup --namespaces production,staging --no-secrets

# PVC data for one namespace
okd-backup backup --pvcs --namespaces production

# Cluster-wide config only
okd-backup backup --cluster-config

# Dry run with verbose output
okd-backup backup --all --dry-run --verbose
```

---

### list

List all available backups. Shortcut for `restore list`.

```
okd-backup list [OPTIONS]
```

| Option | Default | Description |
|---|---|---|
| `--backup-dir TEXT` | config / cwd | Storage location |
| `--unit [GB\|MB]` | `GB` | Unit for the size column |
| `--config PATH` | — | Path to config file |

```bash
okd-backup list
okd-backup list --unit MB
okd-backup list --backup-dir /mnt/nfs/okd-backups
```

---

### restore list

List all available backups.

```
okd-backup restore list [OPTIONS]
```

| Option | Default | Description |
|---|---|---|
| `--backup-dir TEXT` | config / cwd | Storage location |
| `--unit [GB\|MB]` | `GB` | Unit for the size column |
| `--config PATH` | — | Path to config file |

The **Contents** column shows which components were included:

| Value | Description |
|---|---|
| `etcd` | etcd snapshot — full cluster state, certificates, all resources |
| `namespaces` | Per-namespace YAML files (deployments, services, configmaps, secrets, routes, …) |
| `pvcs` | Actual data inside PersistentVolumes, copied via `oc rsync` |
| `cluster-config` | Cluster-wide config: OAuth, ingress, operators, CRDs, ClusterRoles, StorageClasses |

```bash
okd-backup restore list
okd-backup restore list --unit MB
okd-backup restore list --backup-dir /mnt/nfs/okd-backups
```

---

### restore run

Restore resources from a backup.

```
okd-backup restore run --backup-id <ID> [OPTIONS]
```

| Option | Default | Description |
|---|---|---|
| `--backup-id TEXT` | **required** | Backup ID from `restore list` |
| `--etcd` | — | Restore etcd snapshot via SSH (cluster API may be down) |
| `--namespace TEXT` | — | Restore a single namespace |
| `--namespaces TEXT` | — | Comma-separated list of namespaces |
| `--type TEXT` | — | Restore a single resource type (e.g. `deployments`) |
| `--pvcs` | — | Restore PVC data |
| `--cluster-config` | — | Restore cluster configuration |
| `--force-config` | — | Also restore `clusterversion`, `crds` (dangerous) |
| `--map-namespace TEXT` | — | Remap namespace: `old:new` |
| `--control-plane TEXT` | config | Hostname or IP of a control plane node (required for `--etcd`) |
| `--ssh-key TEXT` | config / `~/.ssh/id_rsa` | SSH private key (for `--etcd`) |
| `--ssh-user TEXT` | config / `core` | SSH user (for `--etcd`) |
| `--backup-dir TEXT` | config / cwd | Storage location |
| `--dry-run` | — | Simulate without changes |
| `--verbose` | — | Show `oc`/`kubectl` output |
| `--yes` / `-y` | — | Auto-create missing namespaces; skip etcd restore confirmation |
| `--config PATH` | — | Path to config file |

If a target namespace does not exist, `restore run` will prompt you to create it
before starting. Pass `--yes` to skip the prompt (useful in scripts).

**Examples:**

```bash
# Restore a single namespace
okd-backup restore run --backup-id 2024-01-15_0200 --namespace production

# Restore only deployments
okd-backup restore run --backup-id 2024-01-15_0200 --namespace production --type deployments

# Restore PVC data
okd-backup restore run --backup-id 2024-01-15_0200 --pvcs --namespace production

# Restore into a different namespace
okd-backup restore run --backup-id 2024-01-15_0200 --namespace production --map-namespace production:staging

# Restore cluster config (safe resources only)
okd-backup restore run --backup-id 2024-01-15_0200 --cluster-config

# Restore cluster config including clusterversion and CRDs
okd-backup restore run --backup-id 2024-01-15_0200 --cluster-config --force-config

# Dry run
okd-backup restore run --backup-id 2024-01-15_0200 --namespace production --dry-run
```

#### What is automatically skipped or modified during restore

Some resources are auto-managed by Kubernetes/OKD controllers and cannot or should not be applied manually. `restore run` handles these transparently:

| Resource | What happens | Why |
|---|---|---|
| `secrets` with `type: kubernetes.io/service-account-token` | **Skipped** | Auto-created by the SA controller; OKD 4.x forbids manually creating these |
| `secrets` with `type: kubernetes.io/dockercfg` | **Skipped** | Auto-created pull secrets; same restriction |
| `serviceaccounts` — `secrets` field | **Stripped** | Lists stale token secret names; the SA controller repopulates it automatically |
| `services` — `spec.clusterIP` / `spec.clusterIPs` | **Stripped** | Cluster-assigned IPs; headless services (`None`) are preserved |
| `jobs` — `spec.selector` and UID labels | **Stripped** | Auto-generated by the Job controller; carrying them across causes label-mismatch errors |
| `persistentvolumeclaims` — `spec.volumeName` | **Stripped** | References a PV by name that may not exist in the target cluster; stripping lets the provisioner assign a new one |

#### Known limitations (not auto-fixed)

| Resource | Issue |
|---|---|
| `routes` with auto-generated `spec.host` | The hostname contains the source cluster's wildcard domain — the route is restored as-is and will point to the wrong address if the base domain differs. Manually update `spec.host` after restore, or delete the field before backup if you want OKD to auto-generate a new one. |

---

### info

Show the contents and size breakdown of a specific backup.

```
okd-backup info <BACKUP_ID> [OPTIONS]
```

| Option | Default | Description |
|---|---|---|
| `--backup-dir TEXT` | config / cwd | Storage location |
| `--unit [GB\|MB]` | `GB` | Unit for size column |
| `--config PATH` | — | Path to config file |

```bash
okd-backup info 2024-01-15_0200
okd-backup info 2024-01-15_0200 --unit MB
```

---

### config

Manage the okd-backup configuration file.

```
okd-backup config <SUBCOMMAND>
```

| Subcommand | Description |
|---|---|
| `set <key> <value>` | Set a configuration value |
| `unset <key>` | Remove a key from the config |
| `show` | Display active config and its file location |
| `path` | Show the config file search order |

**Valid keys:**

| Key | Description | Default |
|---|---|---|
| `backup_dir` | Default backup storage directory | `/mnt/nfs/okd-backups` |
| `control_plane` | Hostname or IP of the control plane node | — |
| `ssh_key` | Path to the SSH private key | `~/.ssh/id_rsa` |
| `ssh_user` | SSH user for control plane access | `core` |

**Examples:**

```bash
okd-backup config set backup_dir /mnt/nfs/okd-backups
okd-backup config set control_plane 192.168.1.10
okd-backup config set ssh_key ~/.ssh/okd_key
okd-backup config set ssh_user core

okd-backup config show
okd-backup config unset control_plane
okd-backup config path
```

---

### detect

Find control plane nodes in the cluster and check SSH reachability.

```
okd-backup detect [OPTIONS]
```

| Option | Default | Description |
|---|---|---|
| `--ssh-port INTEGER` | `22` | SSH port to probe |
| `--timeout INTEGER` | `3` | TCP connect timeout in seconds |
| `--save` | — | Save the detected node to the config file |
| `--config PATH` | — | Config file path (used with `--save`) |

```bash
# Show all control plane nodes and their SSH reachability
okd-backup detect

# Detect and save to config in one step
okd-backup detect --save

# Custom SSH port
okd-backup detect --ssh-port 2222
```

When `--control-plane` is not set and no value exists in the config file,
`okd-backup backup --etcd` (or `--all`) will call `detect` automatically.

---

### deps

Show all required external dependencies and check whether they are available.

```
okd-backup deps
```

Displays two tables:

**External binaries**

| Binary | Purpose | Required for |
|---|---|---|
| `oc` | Cluster interaction (preferred) | all cluster operations |
| `kubectl` | Cluster interaction (fallback) | all cluster operations |
| `rsync` | PVC data transfer | `backup/restore --pvcs` |
| `systemctl` | systemd schedule management | `schedule --type systemd` |
| `crontab` | cron schedule management | `schedule --type cron` |

**Go runtime**

| Module | Purpose | Required for |
|---|---|---|
| `github.com/spf13/cobra` | CLI framework | all |
| `golang.org/x/crypto/ssh` | SSH to control plane | `backup --etcd` |
| `github.com/pkg/sftp` | SFTP file download | `backup --etcd` |
| `gopkg.in/yaml.v3` | Config file parsing | config |

**Cluster access** (shown when `oc` or `kubectl` is available)

| Check | Description |
|---|---|
| Logged in | Runs `oc whoami`; shows the active username |
| cluster-admin | Runs `oc auth can-i '*' '*' --all-namespaces`; warns if not granted |

At least one of `oc` or `kubectl` must be present. The command exits with a
non-zero status if any required dependency is missing or if the cluster login
check fails. A missing `cluster-admin` role produces a warning but does not
fail the command (namespace-level backups may still work).

```bash
okd-backup deps
```

---

### schedule

Manage automated backup schedules — generate, list, and remove systemd timers
or crontab entries.

```
okd-backup schedule <SUBCOMMAND>
```

| Subcommand | Description |
|---|---|
| `generate` | Generate (and optionally install) a schedule |
| `list` | List all installed okd-backup schedules |
| `remove` | Remove an installed schedule |

---

#### schedule generate

```
okd-backup schedule generate [OPTIONS]
```

| Option | Default | Description |
|---|---|---|
| `--preset` | `daily` | `hourly`, `daily`, `weekly`, `monthly` |
| `--on-calendar EXPR` | — | Custom systemd `OnCalendar` expression |
| `--cron EXPR` | — | Custom cron expression (use with `--type cron`) |
| `--backup-args TEXT` | `backup --all` | Arguments passed to `okd-backup` on each run |
| `--type [systemd\|cron]` | `systemd` | Output format |
| `--user TEXT` | `root` | User to run the service as (systemd only) |
| `--unit-name TEXT` | `okd-backup` | Systemd unit filename prefix |
| `--install` | — | Write files to disk and enable (requires root) |
| `--log-file PATH` | `/var/log/okd-backup.log` | Log file for cron output |

```bash
okd-backup schedule generate
okd-backup schedule generate --preset weekly --type cron
okd-backup schedule generate --on-calendar "Mon..Fri 03:00"
okd-backup schedule generate --install
```

---

#### schedule list

List all installed okd-backup schedules — checks both systemd timers and crontab.

```bash
okd-backup schedule list
```

Shows: timer name, next/last trigger time, active state, and the `ExecStart` command for systemd timers; expression and command for crontab entries.

---

#### schedule remove

Remove an installed schedule.

```
okd-backup schedule remove [OPTIONS]
```

| Option | Default | Description |
|---|---|---|
| `--type [systemd\|cron\|all]` | `all` | Which type to remove |
| `--unit-name TEXT` | `okd-backup` | Systemd unit name |
| `-y` / `--yes` | — | Skip confirmation |

```bash
okd-backup schedule remove              # remove all (systemd + cron)
okd-backup schedule remove --type cron  # only crontab entries
okd-backup schedule remove --type systemd --unit-name okd-backup-weekly
```

After removing the systemd timer, useful commands:

```bash
systemctl status okd-backup.timer
journalctl -u okd-backup.service -f
systemctl start okd-backup.service    # trigger a manual run
```

---

### cleanup

Remove old or unwanted backups.

```
okd-backup cleanup [OPTIONS]
```

| Option | Description |
|---|---|
| `--keep N` | Keep the N most recent backups, remove the rest |
| `--older-than N` | Remove backups older than N days |
| `--backup-id ID` | Remove a specific backup by ID (repeatable) |
| `--empty` | Remove backups with no contents (failed or interrupted runs) |
| `--backup-dir TEXT` | Storage location |
| `--config PATH` | Config file path |
| `--dry-run` | Preview what would be removed without deleting |
| `-y` / `--yes` | Skip the confirmation prompt |

Modes can be combined — for example `--keep 10 --empty` keeps the 10 most
recent and also removes any empty backups regardless of age.

Always shows a preview table with sizes and reasons before deleting.
Asks for confirmation unless `--yes` is passed.

**Examples:**

```bash
# Keep only the 5 most recent
okd-backup cleanup --keep 5

# Remove backups older than 30 days
okd-backup cleanup --older-than 30

# Remove a specific backup
okd-backup cleanup --backup-id 2024-01-15_0200

# Remove multiple specific backups
okd-backup cleanup --backup-id 2024-01-10_0200 --backup-id 2024-01-11_0200

# Remove failed/empty backups
okd-backup cleanup --empty

# Combine: keep last 10, also remove empty ones
okd-backup cleanup --keep 10 --empty

# Preview without deleting
okd-backup cleanup --keep 5 --dry-run

# Non-interactive (useful in scripts)
okd-backup cleanup --keep 5 --yes
```

---

### completion

Print a shell completion script to stdout.

```
okd-backup completion {bash|zsh}
```

Print a shell completion script to stdout.

```
okd-backup completion {bash|zsh}
```

```bash
source <(okd-backup completion bash)
source <(okd-backup completion zsh)
```

See [Shell completion](#shell-completion) for permanent installation.

---

## Can I use this for a full cluster restore?

**Short answer:** yes, with caveats — etcd restore handles the cluster itself, the other commands handle application data on top of a running cluster.

### What this tool restores

| Component | Command | Cluster must be running? |
|---|---|---|
| etcd snapshot (full cluster state) | `restore run --etcd` | No — API server may be down |
| Namespace resources (deployments, services, …) | `restore run --namespace` | Yes |
| PVC data | `restore run --pvcs` | Yes |
| Cluster-wide config (OAuth, ingress, operators) | `restore run --cluster-config` | Yes |

### Disaster recovery procedure

If you have lost all control plane nodes:

**Step 1 — Restore etcd on one control plane node**

Power off all other control plane nodes first, then:

```bash
okd-backup restore run --backup-id 2024-01-15_0200 --etcd \
  --control-plane 192.168.1.10 --ssh-key ~/.ssh/okd_key
```

The tool will SSH in, upload the snapshot, run `cluster-restore.sh`, and print post-restore steps. After it completes:

```bash
# On the control plane node:
sudo systemctl restart kubelet

# From the bastion — wait for the API to come back:
oc get nodes

# Approve any pending CSRs from nodes rejoining:
oc get csr | awk '/Pending/{print $1}' | xargs oc adm certificate approve
```

**Step 2 — Reprovision other control plane nodes**

The etcd restore leaves etcd as a single-member cluster. Other control plane nodes must be reprovisioned to rejoin. Refer to the [OKD disaster recovery docs](https://docs.okd.io/latest/backup_and_restore/control_plane_backup_and_restore/disaster_recovery/scenario-2-restoring-cluster-state.html) for this step.

**Step 3 — Restore application data (optional)**

Once the cluster is running, restore namespace resources and PVC data if needed:

```bash
okd-backup restore run --backup-id 2024-01-15_0200 --cluster-config
okd-backup restore run --backup-id 2024-01-15_0200 --namespace production
```

### What this tool cannot do

- Provision bare-metal or VM nodes
- Bootstrap a new cluster from scratch
- Restore worker node OS state

For a full from-scratch rebuild, install OKD first, then use `restore run --etcd` to restore cluster state, then `restore run --cluster-config` and `restore run --namespace` for application-level data.

---

## Backup storage layout

```
<backup_dir>/
└── 2024-01-15_0200/
    ├── metadata.json                  ← backup_id, date, OKD version, contents
    ├── etcd/
    │   ├── snapshot.db
    │   └── static_kuberesources_*.tar.gz
    ├── namespaces/
    │   ├── production/
    │   │   ├── deployments.yaml
    │   │   ├── services.yaml
    │   │   ├── configmaps.yaml
    │   │   ├── secrets.yaml
    │   │   └── ...
    │   └── staging/
    │       └── ...
    ├── pvcs/
    │   └── production/
    │       └── my-database-pvc/       ← rsync copy of PVC mountpoint
    └── cluster-config/
        ├── oauth.yaml
        ├── ingress.yaml
        ├── operator-subscriptions.yaml
        └── ...
```

`metadata.json` example:

```json
{
  "backup_id": "2024-01-15_0200",
  "created_at": "2024-01-15T02:00:04.123456",
  "cluster_version": "4.14.3",
  "contents": ["etcd", "namespaces", "pvcs", "cluster-config"]
}
```

---

## What gets backed up

### Namespace resources

| Resource | Included |
|---|---|
| Deployments, StatefulSets, DaemonSets | ✔ |
| Services | ✔ |
| ConfigMaps | ✔ |
| Secrets | ✔ (skip with `--no-secrets`) |
| PersistentVolumeClaims | ✔ |
| Routes, Ingresses | ✔ |
| CronJobs, Jobs | ✔ |
| Roles, RoleBindings | ✔ |
| ServiceAccounts | ✔ |
| HorizontalPodAutoscalers | ✔ |
| NetworkPolicies, ResourceQuotas, LimitRanges | ✔ |
| ImageStreams | ✔ |

### Cluster-wide config

| Resource | Restored with |
|---|---|
| OAuth | `restore run --cluster-config` |
| Ingress controller | `restore run --cluster-config` |
| Operator subscriptions & groups | `restore run --cluster-config` |
| CatalogSources | `restore run --cluster-config` |
| StorageClasses | `restore run --cluster-config` |
| ClusterRoles & ClusterRoleBindings | `restore run --cluster-config` |
| ClusterVersion | `restore run --cluster-config --force-config` |
| Custom Resource Definitions | `restore run --cluster-config --force-config` |

---

## Limitations

- **etcd restore on a different cluster is not supported** — etcd snapshots contain cluster-specific certificates and node identities. Use `restore run` for cross-cluster migrations.
- **PVC backup requires the PVC to be Bound** — unbound PVCs are silently skipped.
- **`oc rsync` requires `rsync` and `tar`** inside the debug pod — the tool uses `ubi8` (full image) which ships both.
- **Cluster operators are backed up but not auto-restored** — use `--force-config` explicitly, as applying operator config to a running cluster can cause instability.

---

## Shell completion

```bash
# Load for the current session
source <(okd-backup completion bash)
source <(okd-backup completion zsh)
```

To load permanently:

```bash
# bash — add to ~/.bashrc or ~/.bash_profile
echo 'source <(okd-backup completion bash)' >> ~/.bashrc

# zsh — add to ~/.zshrc
echo 'autoload -U compinit && compinit' >> ~/.zshrc
echo 'source <(okd-backup completion zsh)' >> ~/.zshrc
```

Completion is context-aware and reads your config file automatically:

| What | How |
|---|---|
| `--backup-id` | Lists real backup IDs from your configured `backup_dir` (reads config file, no `--backup-dir` needed) |
| `--namespace` / `--namespaces` | Live query via `oc get namespaces` |
| `--type` | Resource types for `restore run`; `systemd`/`cron`/`all` for `schedule` |
| `config set <key>` | Valid keys: `backup_dir`, `control_plane`, `ssh_key`, `ssh_user` |
| `config set <key> <value>` | Directory, file, hostname, or username completion depending on the key |
| `--map-namespace` | Live namespace pairs e.g. `production:staging` |
| `--unit-name` | Active timer names from `systemctl list-timers` |
| `info <TAB>` | Existing backup IDs (positional argument) |
| `schedule`/`config` subcommands | `generate list remove` / `show set unset path` |

---

## Third-party licenses

okd-backup builds on the following open-source Go modules:

| Module | License | Purpose |
|---|---|---|
| [github.com/spf13/cobra](https://github.com/spf13/cobra) | Apache-2.0 | CLI framework |
| [github.com/spf13/pflag](https://github.com/spf13/pflag) | BSD-3-Clause | Flag parsing (via cobra) |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto) | BSD-3-Clause | SSH client for etcd backup |
| [github.com/pkg/sftp](https://github.com/pkg/sftp) | BSD-2-Clause | SFTP file download |
| [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) | MIT | YAML config file parsing |
| [github.com/kr/fs](https://github.com/kr/fs) | BSD-3-Clause | Filesystem helpers (via sftp) |

Runtime tools called as external processes: `oc` / `kubectl` (Apache-2.0),
`cluster-backup.sh` (Apache-2.0), `rsync` (GPL-3.0).

See [LICENSES.md](LICENSES.md) for full details and license compatibility notes.

---

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/my-change`
3. Make your changes
4. Commit: `git commit -m "feat: description of change"`
6. Open a merge request / pull request
