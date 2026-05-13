# Changelog

All notable changes to okd-backup are documented here.

---

## [0.11.6] — 2026-05-13

### Changed
- `deps`: `Required for` column now uses plain white, matching the `Date` column style

---

## [0.11.5] — 2026-05-13

### Changed
- Backup list: `Cluster` column now uses the same color as `Date` (plain white)
  instead of dim/grey

---

## [0.11.4] — 2026-05-13

### Changed
- All tables now use Unicode box-drawing characters (`┌`, `─`, `┬`, `┐`, `│`,
  `├`, `┼`, `┤`, `└`, `┴`, `┘`) instead of ASCII `+`/`-`/`|`

---

## [0.11.3] — 2026-05-13

### Added
- Examples section in `--help` output for all commands and subcommands

---

## [0.11.2] — 2026-05-13

### Added
- `backup list` subcommand — list all available backups directly from the
  `backup` command group (same output as `list` / `restore list`)

---

## [0.11.1] — 2026-05-13

### Added
- `download`: confirmation prompt before extracting tools; `--yes` / `-y` skips it

---

## [0.11.0] — 2026-05-13

### Added
- `download list` subcommand — lists all downloaded release tool sets with name,
  download timestamp, and source image reference
- `download` now writes `release-info.txt` (key=value) in each tools directory
  containing the image reference, human-readable name, and download timestamp

### Changed
- `download --release-image` is now optional; omitting it auto-detects the
  current cluster version via `oc get clusterversion`
- Tools directory is now named after the human-readable release name (from
  `oc adm release info`) instead of the image tag, so SHA256 references also
  produce a readable directory name

---

## [0.10.0] — 2026-05-13

### Changed
- `config show` now displays **all** known config keys (including `pvc_image`),
  with a `Source` column showing `set` for values from the config file and
  `default` for built-in defaults; keys with no default (e.g. `control_plane`)
  show `—`; works even when no config file exists
- `config set` help text updated to include `pvc_image` with its default

---

## [0.9.9] — 2026-05-13

### Added
- `list tree <backup-id>` — display the full directory tree of a specific backup
  using Unicode box-drawing characters; no external `tree` binary required

---

## [0.9.8] — 2026-05-13

### Changed
- `download`: tools now stored at `<backup-dir>/tools/<release-tag>/` — shared across
  all backups, independent of any specific backup ID; removed `--backup-id` flag
- `download`: skips extraction automatically if `.download-complete` marker exists;
  added `--force` flag to override
- Storage layout: `tools/` directory sits alongside backup entries at the backup root,
  not inside a timestamped backup directory

---

## [0.9.7] — 2026-05-13

### Added
- `download` command — runs `oc adm release extract --tools <image> --to=<backup>/tools/`
  and stores the extracted binaries in a `tools/` subdirectory of a backup; use
  `--backup-id` to add tools to an existing backup or omit to create a new entry;
  output is streamed in real time; no live cluster API required

---

## [0.9.6] — 2026-05-13

### Added
- `--pvc-image` flag on `backup` and `restore run` — override the container image
  used for PVC backup/restore pods (useful when `registry.access.redhat.com` is
  unreachable from the cluster); also settable via `pvc_image` in the config file
- `pvc_image` added as a valid `config set` key and to `okd-backup.yaml.example`

---

## [0.9.5] — 2026-05-13

### Fixed
- Backup: `--pvcs` alone no longer triggers a redundant namespace backup;
  namespace backup now only runs when `--all` or `--namespaces` is explicitly set

### Changed
- Default `--ssh-key` flag value and config file fallback updated from
  `~/.ssh/id_rsa` to `~/.ssh/id_ed25519`; updated in README and CLAUDE.md examples

---

## [0.9.4] — 2026-05-12

### Fixed
- Restore: skip `kubernetes.io/service-account-token` and `kubernetes.io/dockercfg`
  secrets — OKD 4.x forbids manually creating these; they are recreated
  automatically by the SA controller
- Restore: strip the `secrets` field from `ServiceAccount` objects — it contains
  stale references to token secrets that no longer exist; the controller
  repopulates it on its own
- Restore: strip `spec.volumeName` from `PersistentVolumeClaim` objects — allows
  the storage provisioner to assign a new PV rather than failing to bind to a
  PV that does not exist in the target cluster
- Restore: re-check for empty items list after filtering (a `secrets.yaml`
  containing only auto-managed secrets now produces no error instead of applying
  an empty list)

### Documentation
- README: added "What is automatically skipped or modified during restore" table
  and "Known limitations" table to the `restore run` section

---

## [0.9.3] — 2026-05-12

### Added
- `restore run --etcd` — restore an etcd snapshot to a control plane node via SSH;
  uploads the snapshot and static-resources files via SFTP, runs
  `cluster-restore.sh` on the node, streams output, and prints post-restore
  steps (kubelet restart, CSR approval). Does not require a live cluster API.
  Accepts `--control-plane`, `--ssh-key`, `--ssh-user` (fall back to config file).
  Shows a destructive-action warning and `[y/N]` prompt; `--yes` skips it.
- README: added "Can I use this for a full cluster restore?" section covering
  the full DR procedure end-to-end

---

## [0.9.2] — 2026-05-12

### Added
- `restore run` now checks whether each target namespace exists before starting
  any restore work; if a namespace is missing it prompts `[y/N]` to create it
  and aborts if the answer is no — pass `--yes` / `-y` to create automatically
  without prompting (useful in scripts)

---

## [0.9.1] — 2026-05-12

### Fixed
- Job restore: strip `spec.selector` and the auto-generated pod template labels
  (`controller-uid`, `job-name`, `batch.kubernetes.io/controller-uid`,
  `batch.kubernetes.io/job-name`) which are set by the Job controller and are
  immutable once the Job exists — applying them to a new cluster caused
  "selector not auto-generated" and label-mismatch errors
- Service restore: strip `spec.clusterIP` / `spec.clusterIPs` which are
  cluster-assigned and already in use in the target cluster (headless services
  with `clusterIP: None` are preserved correctly)

---

## [0.9.0] — 2026-05-12

### Added
- `list` top-level command — shortcut for `restore list`; lists all available
  backups without having to type the `restore` prefix; bash and zsh completion
  updated accordingly

---

## [0.8.1] — 2026-05-11

### Changed
- `--version` flag replaced with `version` subcommand (`okd-backup version`),
  matching the `oc`/`kubectl` UX; bash and zsh completion updated accordingly

### Added
- `Makefile` — run `make` to build the binary, `make clean` to remove it

---

## [0.8.0] — 2026-05-11

### Added
- Initial release as a single static Go binary — build with
  `go build -ldflags="-s -w" -o okd-backup .`, no runtime dependencies required
- `go.mod` / `go.sum` — Go module definition and dependency checksums
- Source files: `main.go`, `logger.go`, `table.go`, `oc.go`, `config.go`,
  `storage.go`, `detect.go`, `schedule.go`, `backup.go`, `restore.go`
- All commands from the feature-complete CLI:
  - `backup` — etcd, namespaces, pvcs, cluster-config, `--all`
  - `restore list` / `restore run` — selective restore with namespace mapping
  - `info` — size breakdown of a backup
  - `cleanup` — remove old, empty, or specific backups
  - `schedule generate` / `schedule list` / `schedule remove` — systemd and cron
  - `detect` — find and TCP-probe control plane nodes; `--save` to config
  - `deps` — check external binaries and Go module versions
  - `config show` / `config set` / `config unset` / `config path`
  - `completion` — bash and zsh tab-completion scripts
