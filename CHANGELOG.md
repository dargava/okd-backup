# Changelog

All notable changes to okd-backup are documented here.

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
