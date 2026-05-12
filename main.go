package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const version = "0.9.4"

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── Root command ──────────────────────────────────────────────────────────────

var rootCmd = &cobra.Command{
	Use:   "okd-backup",
	Short: "OKD cluster backup and restore tool",
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(backupCmd)
	rootCmd.AddCommand(restoreCmd)
	restoreCmd.AddCommand(restoreListCmd)
	restoreCmd.AddCommand(restoreRunCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(infoCmd)
	rootCmd.AddCommand(cleanupCmd)
	rootCmd.AddCommand(scheduleCmd)
	scheduleCmd.AddCommand(scheduleGenerateCmd)
	scheduleCmd.AddCommand(scheduleListCmd)
	scheduleCmd.AddCommand(scheduleRemoveCmd)
	rootCmd.AddCommand(detectCmd)
	rootCmd.AddCommand(depsCmd)
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configUnsetCmd)
	configCmd.AddCommand(configPathCmd)
	rootCmd.AddCommand(completionCmd)
}

// ── version command ───────────────────────────────────────────────────────────

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of okd-backup",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("okd-backup version %s\n", version)
	},
}

// ── backup command ────────────────────────────────────────────────────────────

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Create a backup of the OKD cluster",
	Long:  "Create a backup of the OKD cluster.",
	RunE:  runBackup,
}

var (
	backupDoAll        bool
	backupDoEtcd       bool
	backupNamespacesS  string
	backupDoPVCs       bool
	backupClusterConf  bool
	backupNoSecrets    bool
	backupDir          string
	backupControlPlane string
	backupSSHKey       string
	backupSSHUser      string
	backupDryRun       bool
	backupVerbose      bool
	backupConfig       string
)

func init() {
	f := backupCmd.Flags()
	f.BoolVar(&backupDoAll, "all", false, "Back up etcd, namespaces, and cluster config (excludes PVCs)")
	f.BoolVar(&backupDoEtcd, "etcd", false, "etcd snapshot (requires SSH to control plane)")
	f.StringVar(&backupNamespacesS, "namespaces", "", "Comma-separated list of namespaces")
	f.BoolVar(&backupDoPVCs, "pvcs", false, "Back up PVC data (not included in --all; use explicitly)")
	f.BoolVar(&backupClusterConf, "cluster-config", false, "Cluster-wide configuration (OAuth, ingress, operators)")
	f.BoolVar(&backupNoSecrets, "no-secrets", false, "Skip secrets")
	f.StringVar(&backupDir, "backup-dir", defaultBackupDir, "Storage location for backups")
	f.StringVar(&backupControlPlane, "control-plane", "", "Hostname or IP of a control plane node (required for --etcd)")
	f.StringVar(&backupSSHKey, "ssh-key", "~/.ssh/id_rsa", "SSH private key for control plane access")
	f.StringVar(&backupSSHUser, "ssh-user", "core", "SSH user for control plane access")
	f.BoolVar(&backupDryRun, "dry-run", false, "Simulate without making changes")
	f.BoolVar(&backupVerbose, "verbose", false, "Show oc/kubectl command output")
	f.StringVar(&backupConfig, "config", "", "Config file path")
}

func runBackup(cmd *cobra.Command, args []string) error {
	if !backupDoAll && !backupDoEtcd && backupNamespacesS == "" && !backupDoPVCs && !backupClusterConf {
		logError("Specify what to back up. Use --all or a specific option.")
		logInfo("Tip: okd-backup backup --help")
		os.Exit(1)
	}

	setVerbose(backupVerbose)
	requireClusterAccess()

	if backupDryRun {
		logWarning("DRY-RUN mode active — no changes will be made")
	}

	// Fill missing options from config file
	cfg, _ := loadConfig(backupConfig)
	if backupControlPlane == "" {
		backupControlPlane = cfg["control_plane"]
	}
	if backupSSHKey == "~/.ssh/id_rsa" && cfg["ssh_key"] != "" {
		backupSSHKey = cfg["ssh_key"]
	}
	if backupSSHUser == "core" && cfg["ssh_user"] != "" {
		backupSSHUser = cfg["ssh_user"]
	}

	// Auto-detect control plane if etcd backup is requested and no host given
	if (backupDoAll || backupDoEtcd) && backupControlPlane == "" {
		logInfo("No --control-plane specified, attempting auto-detection …")
		backupControlPlane = detectControlPlane(22, 3)
		if backupControlPlane == "" {
			logError("--control-plane is required for an etcd backup and auto-detection found no reachable node.\nRun 'okd-backup detect' to see available control plane nodes.")
			os.Exit(1)
		}
		logInfo(fmt.Sprintf("Auto-detected control plane: %s", backupControlPlane))
	}

	storage, err := NewBackupStorage(resolveBackupDir(backupDir, backupConfig))
	if err != nil {
		return err
	}

	cv := clusterVersion(backupDryRun)
	ctx, err := storage.NewBackup(cv)
	if err != nil {
		return err
	}

	logInfo(fmt.Sprintf("Backup ID: %s", ctx.Metadata.BackupID))

	// etcd
	if backupDoAll || backupDoEtcd {
		if err := backupEtcd(ctx, backupControlPlane, backupSSHKey, backupSSHUser, backupDryRun); err != nil {
			logError(fmt.Sprintf("etcd backup failed: %v", err))
		}
	}

	// Namespaces
	var nsList []string
	if backupNamespacesS != "" {
		for _, n := range strings.Split(backupNamespacesS, ",") {
			nsList = append(nsList, strings.TrimSpace(n))
		}
	}
	if backupDoAll || backupNamespacesS != "" || backupDoPVCs {
		if err := backupNamespaces(ctx, nsList, !backupNoSecrets, backupDryRun); err != nil {
			logError(fmt.Sprintf("namespace backup failed: %v", err))
		}
	}

	// PVCs — excluded from --all; must be requested explicitly with --pvcs
	if backupDoPVCs {
		if err := backupPVCs(ctx, nsList, backupDryRun); err != nil {
			logError(fmt.Sprintf("PVC backup failed: %v", err))
		}
	}

	// Cluster configuration
	if backupDoAll || backupClusterConf {
		if err := backupClusterConfig(ctx, backupDryRun); err != nil {
			logError(fmt.Sprintf("cluster-config backup failed: %v", err))
		}
	}

	logSection("Done")
	fmt.Println(ctx.Summary())
	return nil
}

// ── restore command ───────────────────────────────────────────────────────────

var restoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore resources from a backup",
}

// restore list
var (
	rListBackupDir string
	rListUnit      string
	rListConfig    string
)

var restoreListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available backups",
	RunE:  runRestoreList,
}

func init() {
	f := restoreListCmd.Flags()
	f.StringVar(&rListBackupDir, "backup-dir", defaultBackupDir, "Storage location for backups")
	f.StringVar(&rListUnit, "unit", "GB", "Unit for backup size column (GB or MB)")
	f.StringVar(&rListConfig, "config", "", "Config file path")
}

func runRestoreList(cmd *cobra.Command, args []string) error {
	storage, err := NewBackupStorage(resolveBackupDir(rListBackupDir, rListConfig))
	if err != nil {
		return err
	}

	backups, err := storage.ListBackups()
	if err != nil {
		return err
	}

	if len(backups) == 0 {
		logWarning(fmt.Sprintf("No backups found in %s", storage.Root))
		return nil
	}

	unit := strings.ToUpper(rListUnit)
	var divisor float64 = 1024 * 1024 * 1024
	if unit == "MB" {
		divisor = 1024 * 1024
	}

	t := newTable("Available backups")
	t.addColumn("Backup ID", cyan)
	t.addColumn("Date")
	t.addColumn("Cluster", dim)
	t.addColumn("Contents", green)
	t.addColumn(fmt.Sprintf("Size (%s)", unit), nil, "right")

	for _, b := range backups {
		sizeStr := "—"
		ctx, err := storage.OpenBackup(b.BackupID)
		if err == nil {
			sizeBytes := ctx.DiskSize()
			sizeStr = fmt.Sprintf("%.2f", float64(sizeBytes)/divisor)
		}

		date := b.CreatedAt
		if len(date) > 19 {
			date = date[:19]
		}
		contents := strings.Join(b.Contents, ", ")
		if contents == "" {
			contents = "—"
		}

		t.addRow(b.BackupID, date, b.ClusterVersion, contents, sizeStr)
	}

	t.print()
	return nil
}

// restore run
var (
	rRunBackupID      string
	rRunNamespace     string
	rRunNamespaces    string
	rRunResourceType  string
	rRunEtcd          bool
	rRunPVCs          bool
	rRunClusterConfig bool
	rRunForceConfig   bool
	rRunMapNamespace  string
	rRunControlPlane  string
	rRunSSHKey        string
	rRunSSHUser       string
	rRunBackupDir     string
	rRunDryRun        bool
	rRunVerbose       bool
	rRunYes           bool
	rRunConfig        string
)

var restoreRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Restore resources from a backup",
	RunE:  runRestoreRun,
}

func init() {
	f := restoreRunCmd.Flags()
	f.StringVar(&rRunBackupID, "backup-id", "", "Backup ID to restore from (see: restore list)")
	_ = restoreRunCmd.MarkFlagRequired("backup-id")
	f.StringVar(&rRunNamespace, "namespace", "", "Restore a single namespace")
	f.StringVar(&rRunNamespaces, "namespaces", "", "Comma-separated list of namespaces")
	f.StringVar(&rRunResourceType, "type", "", "Restore a single resource type (e.g. deployments)")
	f.BoolVar(&rRunEtcd, "etcd", false, "Restore etcd snapshot (requires SSH to control plane; cluster API may be down)")
	f.BoolVar(&rRunPVCs, "pvcs", false, "Restore PVC data")
	f.BoolVar(&rRunClusterConfig, "cluster-config", false, "Restore cluster configuration")
	f.BoolVar(&rRunForceConfig, "force-config", false, "Also restore dangerous cluster-config resources")
	f.StringVar(&rRunMapNamespace, "map-namespace", "", "Namespace mapping: 'old:new'")
	f.StringVar(&rRunControlPlane, "control-plane", "", "Hostname or IP of a control plane node (required for --etcd)")
	f.StringVar(&rRunSSHKey, "ssh-key", "~/.ssh/id_rsa", "SSH private key for control plane access")
	f.StringVar(&rRunSSHUser, "ssh-user", "core", "SSH user for control plane access")
	f.StringVar(&rRunBackupDir, "backup-dir", defaultBackupDir, "Storage location for backups")
	f.BoolVar(&rRunDryRun, "dry-run", false, "Simulate without making changes")
	f.BoolVar(&rRunVerbose, "verbose", false, "Show oc/kubectl command output")
	f.BoolVarP(&rRunYes, "yes", "y", false, "Auto-create missing target namespaces; skip etcd restore confirmation")
	f.StringVar(&rRunConfig, "config", "", "Config file path")
}

func runRestoreRun(cmd *cobra.Command, args []string) error {
	setVerbose(rRunVerbose)

	// etcd restore does not require a live cluster — the API server may be down
	if !rRunEtcd {
		requireClusterAccess()
	}

	if rRunDryRun {
		logWarning("DRY-RUN mode active — no changes will be made")
	}

	// Fill SSH params from config (only needed for etcd restore)
	if rRunEtcd {
		cfg, _ := loadConfig(rRunConfig)
		if rRunControlPlane == "" {
			rRunControlPlane = cfg["control_plane"]
		}
		if rRunSSHKey == "~/.ssh/id_rsa" && cfg["ssh_key"] != "" {
			rRunSSHKey = cfg["ssh_key"]
		}
		if rRunSSHUser == "core" && cfg["ssh_user"] != "" {
			rRunSSHUser = cfg["ssh_user"]
		}
		if rRunControlPlane == "" {
			logError("--control-plane is required for etcd restore")
			os.Exit(1)
		}
	}

	storage, err := NewBackupStorage(resolveBackupDir(rRunBackupDir, rRunConfig))
	if err != nil {
		return err
	}

	ctx, err := storage.OpenBackup(rRunBackupID)
	if err != nil {
		logError(err.Error())
		logInfo("Use 'okd-backup restore list' to see available backups")
		os.Exit(1)
	}

	logInfo(fmt.Sprintf("Backup loaded: %s", rRunBackupID))
	fmt.Println(ctx.Summary())

	// etcd restore
	if rRunEtcd {
		if err := restoreEtcd(ctx, rRunControlPlane, rRunSSHKey, rRunSSHUser, rRunYes, rRunDryRun); err != nil {
			logError(fmt.Sprintf("etcd restore failed: %v", err))
		}
	}

	// Namespace mapping
	var nsMapping map[string]string
	if rRunMapNamespace != "" {
		parts := strings.SplitN(rRunMapNamespace, ":", 2)
		if len(parts) != 2 {
			logError("--map-namespace format: 'old:new'")
			os.Exit(1)
		}
		nsMapping = map[string]string{strings.TrimSpace(parts[0]): strings.TrimSpace(parts[1])}
		logInfo(fmt.Sprintf("Namespace mapping: %s → %s", parts[0], parts[1]))
	}

	// Build namespace list
	var nsList []string
	if rRunNamespace != "" {
		nsList = []string{rRunNamespace}
	} else if rRunNamespaces != "" {
		for _, n := range strings.Split(rRunNamespaces, ",") {
			nsList = append(nsList, strings.TrimSpace(n))
		}
	}

	var rtList []string
	if rRunResourceType != "" {
		rtList = []string{rRunResourceType}
	}

	// Namespace resources — skipped when only --etcd is set
	if len(nsList) > 0 || rRunResourceType != "" || (!rRunEtcd && !rRunPVCs && !rRunClusterConfig) {
		if err := restoreNamespaces(ctx, nsList, rtList, nsMapping, rRunYes, rRunDryRun); err != nil {
			logError(fmt.Sprintf("namespace restore failed: %v", err))
		}
	}

	if rRunPVCs {
		if err := restorePVCs(ctx, nsList, nil, rRunDryRun); err != nil {
			logError(fmt.Sprintf("PVC restore failed: %v", err))
		}
	}

	if rRunClusterConfig {
		if err := restoreClusterConfig(ctx, nil, rRunForceConfig, rRunDryRun); err != nil {
			logError(fmt.Sprintf("cluster-config restore failed: %v", err))
		}
	}

	logSection("Restore completed")
	return nil
}

// ── list command ──────────────────────────────────────────────────────────────

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available backups",
	RunE:  runRestoreList,
}

func init() {
	f := listCmd.Flags()
	f.StringVar(&rListBackupDir, "backup-dir", defaultBackupDir, "Storage location for backups")
	f.StringVar(&rListUnit, "unit", "GB", "Unit for backup size column (GB or MB)")
	f.StringVar(&rListConfig, "config", "", "Config file path")
}

// ── info command ──────────────────────────────────────────────────────────────

var (
	infoBackupDir string
	infoUnit      string
	infoConfig    string
)

var infoCmd = &cobra.Command{
	Use:   "info <backup-id>",
	Short: "Show details of a specific backup",
	Args:  cobra.ExactArgs(1),
	RunE:  runInfo,
}

func init() {
	f := infoCmd.Flags()
	f.StringVar(&infoBackupDir, "backup-dir", defaultBackupDir, "Storage location for backups")
	f.StringVar(&infoUnit, "unit", "GB", "Unit for size column (GB or MB)")
	f.StringVar(&infoConfig, "config", "", "Config file path")
}

func runInfo(cmd *cobra.Command, args []string) error {
	backupID := args[0]
	storage, err := NewBackupStorage(resolveBackupDir(infoBackupDir, infoConfig))
	if err != nil {
		return err
	}

	ctx, err := storage.OpenBackup(backupID)
	if err != nil {
		logError(err.Error())
		os.Exit(1)
	}

	fmt.Println(ctx.Summary())
	fmt.Println()

	unit := strings.ToUpper(infoUnit)
	var divisor float64 = 1024 * 1024 * 1024
	if unit == "MB" {
		divisor = 1024 * 1024
	}

	t := newTable("Contents")
	t.addColumn("Directory", cyan)
	t.addColumn(fmt.Sprintf("Size (%s)", unit), nil, "right")

	entries, _ := os.ReadDir(ctx.Path)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		size := dirSize(filepath.Join(ctx.Path, entry.Name()))
		t.addRow(entry.Name(), fmt.Sprintf("%.2f", float64(size)/divisor))
	}
	t.print()
	return nil
}

func dirSize(path string) int64 {
	var total int64
	filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// ── cleanup command ───────────────────────────────────────────────────────────

var (
	cleanupKeep      int
	cleanupOlderThan int
	cleanupIDs       []string
	cleanupEmpty     bool
	cleanupBackupDir string
	cleanupConfig    string
	cleanupDryRun    bool
	cleanupYes       bool
)

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove old or unwanted backups",
	Long: `Remove old or unwanted backups.

Modes (can be combined):
  --keep N          Keep the N most recent, remove everything older
  --older-than N    Remove backups older than N days
  --backup-id ID    Remove a specific backup by ID (use multiple times)
  --empty           Remove backups that have no contents (failed runs)`,
	RunE: runCleanup,
}

func init() {
	f := cleanupCmd.Flags()
	f.IntVar(&cleanupKeep, "keep", 0, "Keep the N most recent backups, remove the rest")
	f.IntVar(&cleanupOlderThan, "older-than", 0, "Remove backups older than N days")
	f.StringArrayVar(&cleanupIDs, "backup-id", nil, "Remove a specific backup by ID (repeatable)")
	f.BoolVar(&cleanupEmpty, "empty", false, "Remove backups with no contents")
	f.StringVar(&cleanupBackupDir, "backup-dir", defaultBackupDir, "Storage location for backups")
	f.StringVar(&cleanupConfig, "config", "", "Config file path")
	f.BoolVar(&cleanupDryRun, "dry-run", false, "Show what would be removed without deleting")
	f.BoolVarP(&cleanupYes, "yes", "y", false, "Skip confirmation prompt")
}

type cleanupEntry struct {
	meta    BackupMetadata
	reasons []string
}

func runCleanup(cmd *cobra.Command, args []string) error {
	if cleanupKeep == 0 && cleanupOlderThan == 0 && len(cleanupIDs) == 0 && !cleanupEmpty {
		logError("Specify at least one removal criterion.\n  --keep N        keep the N most recent\n  --older-than N  remove backups older than N days\n  --backup-id ID  remove a specific backup\n  --empty         remove backups with no contents")
		os.Exit(1)
	}

	if cleanupDryRun {
		logWarning("DRY-RUN mode — nothing will be deleted")
	}

	storage, err := NewBackupStorage(resolveBackupDir(cleanupBackupDir, cleanupConfig))
	if err != nil {
		return err
	}

	allBackups, err := storage.ListBackups()
	if err != nil {
		return err
	}
	if len(allBackups) == 0 {
		logInfo("No backups found.")
		return nil
	}

	byID := map[string]*cleanupEntry{}
	var ordered []string

	addEntry := func(meta BackupMetadata, reason string) {
		if _, exists := byID[meta.BackupID]; !exists {
			byID[meta.BackupID] = &cleanupEntry{meta: meta}
			ordered = append(ordered, meta.BackupID)
		}
		byID[meta.BackupID].reasons = append(byID[meta.BackupID].reasons, reason)
	}

	// --keep N
	if cleanupKeep > 0 {
		for i, b := range allBackups {
			if i >= cleanupKeep {
				addEntry(b, fmt.Sprintf("outside keep-last-%d", cleanupKeep))
			}
		}
	}

	// --older-than N
	if cleanupOlderThan > 0 {
		cutoff := time.Now().Add(-time.Duration(cleanupOlderThan) * 24 * time.Hour)
		for _, b := range allBackups {
			t, err := time.Parse(time.RFC3339, b.CreatedAt)
			if err != nil {
				continue
			}
			if t.Before(cutoff) {
				addEntry(b, fmt.Sprintf("older than %d days", cleanupOlderThan))
			}
		}
	}

	// --backup-id
	if len(cleanupIDs) > 0 {
		existingIDs := map[string]BackupMetadata{}
		for _, b := range allBackups {
			existingIDs[b.BackupID] = b
		}
		for _, id := range cleanupIDs {
			if b, ok := existingIDs[id]; ok {
				addEntry(b, "specified with --backup-id")
			} else {
				logWarning(fmt.Sprintf("Backup not found: %q — skipping", id))
			}
		}
	}

	// --empty
	if cleanupEmpty {
		for _, b := range allBackups {
			if len(b.Contents) == 0 {
				addEntry(b, "no contents (empty/failed backup)")
			}
		}
	}

	if len(ordered) == 0 {
		logSuccess("Nothing to remove.")
		return nil
	}

	// Preview table
	logSection("Backups to remove")
	t := newTable("")
	t.addColumn("Backup ID", cyan)
	t.addColumn("Date")
	t.addColumn("Contents", dim)
	t.addColumn("Size", yellow)
	t.addColumn("Reason", red)

	var totalBytes int64
	for _, id := range ordered {
		e := byID[id]
		sizeStr := "?"
		ctx, err := storage.OpenBackup(id)
		if err == nil {
			sz := ctx.DiskSize()
			totalBytes += sz
			sizeStr = fmtSize(sz)
		}
		date := e.meta.CreatedAt
		if len(date) > 19 {
			date = date[:19]
		}
		contents := strings.Join(e.meta.Contents, ", ")
		if contents == "" {
			contents = "—"
		}
		t.addRow(id, date, contents, sizeStr, strings.Join(e.reasons, ", "))
	}
	t.print()

	fmt.Printf("\n  %s%d backup(s)%s — %s%s%s will be freed\n\n",
		colorBold, len(ordered), colorReset,
		colorYellow, fmtSize(totalBytes), colorReset)

	if cleanupDryRun {
		logInfo("DRY-RUN — no files were deleted.")
		return nil
	}

	// Confirm
	if !cleanupYes {
		fmt.Print("Proceed with deletion? [y/N] ")
		var ans string
		fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			logInfo("Aborted.")
			return nil
		}
	}

	removed := 0
	for _, id := range ordered {
		backupPath := filepath.Join(storage.Root, id)
		if err := os.RemoveAll(backupPath); err != nil {
			logError(fmt.Sprintf("Failed to remove %s: %v", id, err))
		} else {
			logSuccess(fmt.Sprintf("Removed: %s", id))
			removed++
		}
	}

	logSection("Done")
	logSuccess(fmt.Sprintf("Removed %d backup(s), freed %s.", removed, fmtSize(totalBytes)))
	return nil
}

// ── schedule command ──────────────────────────────────────────────────────────

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage automated backup schedules (systemd timers or crontab entries)",
}

// schedule generate
var (
	sgPreset      string
	sgOnCalendar  string
	sgCronExpr    string
	sgBackupArgs  string
	sgUser        string
	sgUnitName    string
	sgInstall     bool
	sgType        string
	sgLogFile     string
)

var scheduleGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate (and optionally install) a backup schedule",
	RunE:  runScheduleGenerate,
}

func init() {
	f := scheduleGenerateCmd.Flags()
	f.StringVar(&sgPreset, "preset", "daily", "Built-in schedule preset (hourly|daily|weekly|monthly)")
	f.StringVar(&sgOnCalendar, "on-calendar", "", "Custom systemd OnCalendar expression")
	f.StringVar(&sgCronExpr, "cron", "", "Custom cron expression, e.g. '0 3 * * *'")
	f.StringVar(&sgBackupArgs, "backup-args", "backup --all", "okd-backup arguments to run on schedule")
	f.StringVar(&sgUser, "user", "root", "User to run the service as (systemd only)")
	f.StringVar(&sgUnitName, "unit-name", "okd-backup", "Systemd unit name")
	f.BoolVar(&sgInstall, "install", false, "Write unit files and enable the timer (requires root)")
	f.StringVar(&sgType, "type", "systemd", "Output type: systemd or cron")
	f.StringVar(&sgLogFile, "log-file", "", "Log file for cron output (cron type only)")
}

func runScheduleGenerate(cmd *cobra.Command, args []string) error {
	if sgType == "systemd" {
		p := presets[sgPreset]
		calendar := sgOnCalendar
		label := sgOnCalendar
		if calendar == "" {
			calendar = p.Systemd
			label = p.Label
		}
		runScheduleSystemd(calendar, sgBackupArgs, sgUser, sgUnitName, label, sgInstall)
	} else {
		p := presets[sgPreset]
		cron := sgCronExpr
		label := sgCronExpr
		if cron == "" {
			cron = p.Cron
			label = p.Label
		}
		runScheduleCron(cron, sgBackupArgs, label, sgLogFile, sgInstall)
	}
	return nil
}

func runScheduleSystemd(onCalendar, backupArgs, user, unitName, label string, install bool) {
	serviceName := unitName + ".service"
	timerName := unitName + ".timer"
	unitDir := "/etc/systemd/system"

	serviceContent := renderService(backupArgs, user)
	timerContent := renderTimer(onCalendar)

	logSection("systemd timer")
	logInfo(fmt.Sprintf("Schedule: %s  (%s)", label, onCalendar))
	logInfo(fmt.Sprintf("Command:  okd-backup %s", backupArgs))
	fmt.Println()

	fmt.Printf("%s── %s ──%s\n", colorCyan, serviceName, colorReset)
	fmt.Println(serviceContent)
	fmt.Printf("%s── %s ──%s\n", colorCyan, timerName, colorReset)
	fmt.Println(timerContent)

	if install {
		installSystemdUnits(unitDir, unitName, serviceName, timerName, serviceContent, timerContent)
	} else {
		logDim("To install manually:")
		logDim(fmt.Sprintf("  cat > %s/%s", unitDir, serviceName))
		logDim(fmt.Sprintf("  cat > %s/%s", unitDir, timerName))
		logDim("  systemctl daemon-reload")
		logDim(fmt.Sprintf("  systemctl enable --now %s", timerName))
		fmt.Println()
		logDim("Or re-run with --install to do this automatically.")
	}
}

func installSystemdUnits(unitDir, unitName, serviceName, timerName, serviceContent, timerContent string) {
	if _, err := os.Stat(unitDir); os.IsNotExist(err) {
		logError(fmt.Sprintf("%s does not exist — is this a systemd system?", unitDir))
		os.Exit(1)
	}

	servicePath := filepath.Join(unitDir, serviceName)
	timerPath := filepath.Join(unitDir, timerName)

	if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
		logError(fmt.Sprintf("write %s: %v", servicePath, err))
		os.Exit(1)
	}
	logSuccess(fmt.Sprintf("Written: %s", servicePath))

	if err := os.WriteFile(timerPath, []byte(timerContent), 0644); err != nil {
		logError(fmt.Sprintf("write %s: %v", timerPath, err))
		os.Exit(1)
	}
	logSuccess(fmt.Sprintf("Written: %s", timerPath))

	for _, args := range [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "--now", timerName},
	} {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			logError(fmt.Sprintf("Command failed: %s\n%s", strings.Join(args, " "), out))
			os.Exit(1)
		}
		logSuccess(fmt.Sprintf("  %s", strings.Join(args, " ")))
	}

	logSuccess(fmt.Sprintf("Timer %s is active.", timerName))
	fmt.Println()
	logDim(fmt.Sprintf("Check status:  systemctl status %s", timerName))
	logDim(fmt.Sprintf("View logs:     journalctl -u %s -f", strings.Replace(timerName, ".timer", ".service", 1)))
	logDim(fmt.Sprintf("Run now:       systemctl start %s", strings.Replace(timerName, ".timer", ".service", 1)))
}

func runScheduleCron(cronExpr, backupArgs, label, logFile string, install bool) {
	entry := renderCrontab(cronExpr, backupArgs, logFile)

	logSection("crontab entry")
	logInfo(fmt.Sprintf("Schedule: %s  (%s)", label, cronExpr))
	logInfo(fmt.Sprintf("Command:  okd-backup %s", backupArgs))
	fmt.Println()
	fmt.Printf("%s%s%s\n\n", colorCyan, entry, colorReset)

	if install {
		out, _ := exec.Command("crontab", "-l").Output()
		existing := string(out)
		if strings.Contains(existing, entry) {
			logWarning("This crontab entry already exists — not adding again.")
			return
		}
		newCrontab := strings.TrimRight(existing, "\n") + "\n" + entry + "\n"
		proc := exec.Command("crontab", "-")
		proc.Stdin = strings.NewReader(newCrontab)
		if out, err := proc.CombinedOutput(); err != nil {
			logError(fmt.Sprintf("crontab install failed: %s", out))
			os.Exit(1)
		}
		logSuccess("Crontab entry installed.")
		logDim("View with:  crontab -l")
		logDim("Edit with:  crontab -e")
	} else {
		logDim("To install, run with --install or add manually:")
		logDim("  crontab -e")
		logDim("Or re-run with --install to do this automatically.")
	}
}

// schedule list
var scheduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed okd-backup schedules (systemd timers and crontab entries)",
	RunE:  runScheduleList,
}

func runScheduleList(cmd *cobra.Command, args []string) error {
	foundAny := false

	// systemd timers
	result, err := exec.Command("systemctl", "list-timers", "--all", "--no-pager", "--output=json").Output()
	if err == nil {
		// Parse JSON output
		timers := parseSystemdTimersJSON(string(result))
		var okdTimers []map[string]string
		for _, t := range timers {
			if strings.Contains(t["unit"], "okd-backup") {
				okdTimers = append(okdTimers, t)
			}
		}

		if len(okdTimers) > 0 {
			foundAny = true
			logSection("systemd timers")
			tb := newTable("")
			tb.addColumn("Timer", cyan)
			tb.addColumn("Next trigger")
			tb.addColumn("Last trigger", dim)
			tb.addColumn("State", green)

			for _, t := range okdTimers {
				unit := t["unit"]
				next := t["next"]
				if next == "" {
					next = "—"
				}
				last := t["last"]
				if last == "" {
					last = "—"
				}

				stateOut, _ := exec.Command("systemctl", "is-active", unit).Output()
				state := strings.TrimSpace(string(stateOut))
				if state == "" {
					state = "unknown"
				}
				stateFmt := state
				if state == "active" {
					stateFmt = green(state)
				} else {
					stateFmt = dim(state)
				}
				tb.addRow(unit, next, last, stateFmt)
			}
			tb.print()

			// Show ExecStart from each service unit
			for _, t := range okdTimers {
				service := strings.Replace(t["unit"], ".timer", ".service", 1)
				svcOut, err := exec.Command("systemctl", "cat", service).Output()
				if err == nil {
					for _, line := range strings.Split(string(svcOut), "\n") {
						if strings.HasPrefix(strings.TrimSpace(line), "ExecStart=") {
							logDim(fmt.Sprintf("%s: %s", service, strings.TrimSpace(line)))
						}
					}
				}
			}
		}
	} else {
		// Fallback: scan unit files directly
		unitDir := "/etc/systemd/system"
		if files, err := filepath.Glob(filepath.Join(unitDir, "okd-backup*.timer")); err == nil && len(files) > 0 {
			foundAny = true
			logSection("systemd timers (from unit files)")
			for _, f := range files {
				fmt.Printf("%s%s%s\n", colorCyan, filepath.Base(f), colorReset)
				data, _ := os.ReadFile(f)
				fmt.Println(string(data))
			}
		}
	}

	// crontab entries
	cronOut, err := exec.Command("crontab", "-l").Output()
	if err == nil {
		var okdLines []string
		for _, line := range strings.Split(string(cronOut), "\n") {
			if strings.Contains(line, "okd-backup") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				okdLines = append(okdLines, line)
			}
		}
		if len(okdLines) > 0 {
			foundAny = true
			logSection("crontab entries")
			tb := newTable("")
			tb.addColumn("Expression", yellow)
			tb.addColumn("Command")
			for _, line := range okdLines {
				parts := strings.Fields(line)
				if len(parts) >= 6 {
					expr := strings.Join(parts[:5], " ")
					cmd := strings.Join(parts[5:], " ")
					tb.addRow(expr, cmd)
				} else {
					tb.addRow(line, "")
				}
			}
			tb.print()
		}
	}

	if !foundAny {
		logInfo("No okd-backup schedules found.")
		logDim("Install one with: okd-backup schedule generate --install")
	}
	return nil
}

// parseSystemdTimersJSON parses systemd list-timers --output=json output.
func parseSystemdTimersJSON(jsonStr string) []map[string]string {
	var entries []struct {
		Unit string `json:"unit"`
		Next string `json:"next"`
		Last string `json:"last"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		return nil
	}
	result := make([]map[string]string, len(entries))
	for i, e := range entries {
		result[i] = map[string]string{
			"unit": e.Unit,
			"next": e.Next,
			"last": e.Last,
		}
	}
	return result
}

func extractJSONStr(obj, key string) string {
	needle := `"` + key + `":"`
	idx := strings.Index(obj, needle)
	if idx < 0 {
		return ""
	}
	rest := obj[idx+len(needle):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// schedule remove
var (
	srUnitName string
	srType     string
	srYes      bool
)

var scheduleRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove an installed okd-backup schedule",
	RunE:  runScheduleRemove,
}

func init() {
	f := scheduleRemoveCmd.Flags()
	f.StringVar(&srUnitName, "unit-name", "okd-backup", "Systemd unit name to remove")
	f.StringVar(&srType, "type", "all", "Which schedule type to remove (systemd|cron|all)")
	f.BoolVarP(&srYes, "yes", "y", false, "Skip confirmation prompt")
}

func runScheduleRemove(cmd *cobra.Command, args []string) error {
	removedAnything := false

	if srType == "systemd" || srType == "all" {
		if removeSystemdSchedule(srUnitName, srYes) {
			removedAnything = true
		}
	}
	if srType == "cron" || srType == "all" {
		if removeCronSchedule(srYes) {
			removedAnything = true
		}
	}

	if !removedAnything {
		logInfo("Nothing was removed.")
	}
	return nil
}

func removeSystemdSchedule(unitName string, yes bool) bool {
	unitDir := "/etc/systemd/system"
	timerPath := filepath.Join(unitDir, unitName+".timer")
	servicePath := filepath.Join(unitDir, unitName+".service")

	var existing []string
	for _, p := range []string{timerPath, servicePath} {
		if _, err := os.Stat(p); err == nil {
			existing = append(existing, p)
		}
	}
	if len(existing) == 0 {
		logVerbose(fmt.Sprintf("No systemd unit files found for %q", unitName))
		return false
	}

	logSection("Remove systemd timer")
	for _, p := range existing {
		logDim(fmt.Sprintf("  %s", p))
	}

	if !yes {
		fmt.Print("Remove these unit files and disable the timer? [y/N] ")
		var ans string
		fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			logInfo("Aborted.")
			return false
		}
	}

	// Stop and disable
	for _, args := range [][]string{
		{"systemctl", "disable", "--now", unitName + ".timer"},
		{"systemctl", "daemon-reload"},
	} {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			logVerbose(fmt.Sprintf("  %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out))))
		} else {
			logVerbose(fmt.Sprintf("  %s", strings.Join(args, " ")))
		}
	}

	for _, p := range existing {
		if err := os.Remove(p); err != nil {
			logError(fmt.Sprintf("delete %s: %v", p, err))
		} else {
			logSuccess(fmt.Sprintf("Deleted: %s", p))
		}
	}

	exec.Command("systemctl", "daemon-reload").Run()
	return true
}

func removeCronSchedule(yes bool) bool {
	cronOut, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		logVerbose("No crontab found for current user")
		return false
	}

	lines := strings.Split(string(cronOut), "\n")
	var remaining, removed []string
	for _, line := range lines {
		if strings.Contains(line, "okd-backup") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			removed = append(removed, line)
		} else {
			remaining = append(remaining, line)
		}
	}

	if len(removed) == 0 {
		logVerbose("No okd-backup entries found in crontab")
		return false
	}

	logSection("Remove crontab entries")
	for _, line := range removed {
		logDim(fmt.Sprintf("  %s", line))
	}

	if !yes {
		fmt.Print("Remove these crontab entries? [y/N] ")
		var ans string
		fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			logInfo("Aborted.")
			return false
		}
	}

	newCrontab := strings.Join(remaining, "\n")
	proc := exec.Command("crontab", "-")
	proc.Stdin = strings.NewReader(newCrontab)
	if out, err := proc.CombinedOutput(); err != nil {
		logError(fmt.Sprintf("crontab update failed: %s", out))
		os.Exit(1)
	}

	logSuccess(fmt.Sprintf("Removed %d crontab entry/entries.", len(removed)))
	return true
}

// ── detect command ────────────────────────────────────────────────────────────

var (
	detectSSHPort int
	detectTimeout int
	detectSave    bool
	detectConfig  string
)

var detectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Detect control plane nodes in the cluster",
	Long: `Detect control plane nodes in the cluster.

Queries the cluster for nodes with the control-plane or master role,
checks which ones are reachable via SSH, and optionally saves the result
to the config file.`,
	RunE: runDetect,
}

func init() {
	f := detectCmd.Flags()
	f.IntVar(&detectSSHPort, "ssh-port", 22, "SSH port to probe for reachability")
	f.IntVar(&detectTimeout, "timeout", 3, "TCP connect timeout in seconds")
	f.BoolVar(&detectSave, "save", false, "Save the detected control plane to the config file")
	f.StringVar(&detectConfig, "config", "", "Config file path (used with --save)")
}

func runDetect(cmd *cobra.Command, args []string) error {
	requireClusterAccess()
	logSection("Control plane detection")

	nodes, err := listControlPlaneNodes()
	if err != nil || len(nodes) == 0 {
		logWarning("No control plane nodes found in the cluster.")
		logInfo("Make sure you are logged in: oc whoami")
		return nil
	}

	tb := newTable("")
	tb.addColumn("Node", cyan)
	tb.addColumn("Addresses")
	tb.addColumn(fmt.Sprintf("SSH :%d", detectSSHPort), green)

	var firstReachable string
	for _, node := range nodes {
		var reachable []string
		for _, addr := range node.Addresses {
			if isReachable(addr, detectSSHPort, detectTimeout) {
				reachable = append(reachable, addr)
			}
		}
		status := red("✖")
		if len(reachable) > 0 {
			status = green("✔")
			if firstReachable == "" {
				firstReachable = reachable[0]
			}
		}
		tb.addRow(node.Name, strings.Join(node.Addresses, ", "), status)
	}
	tb.print()

	if firstReachable != "" {
		fmt.Printf("\n%sReachable control plane:%s %s\n", colorGreen, colorReset, firstReachable)

		if detectSave {
			existing, _ := loadConfig(detectConfig)
			if existing == nil {
				existing = map[string]string{}
			}
			existing["control_plane"] = firstReachable
			target := defaultConfigPath
			if detectConfig != "" {
				target = detectConfig
			}
			written, err := saveConfig(existing, target)
			if err != nil {
				logError(fmt.Sprintf("save config: %v", err))
				os.Exit(1)
			}
			logSuccess(fmt.Sprintf("Saved control_plane = %q to %s", firstReachable, written))
		} else {
			logInfo(fmt.Sprintf("Run 'okd-backup config set control_plane %s' to save, or use --save.", firstReachable))
		}
	} else {
		logWarning(fmt.Sprintf("No control plane node was reachable on port %d.\nCheck your network access or use --ssh-port if SSH runs on a non-standard port.", detectSSHPort))
	}
	return nil
}

// ── deps command ──────────────────────────────────────────────────────────────

var depsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Show required external dependencies and check their availability",
	RunE:  runDeps,
}

func runDeps(cmd *cobra.Command, args []string) error {
	type binaryDep struct {
		Name        string
		Purpose     string
		RequiredFor string
		VersionFn   func() string
	}

	binaries := []binaryDep{
		{"oc", "Cluster interaction (preferred)", "all cluster operations", ocClientVersion},
		{"kubectl", "Cluster interaction (fallback)", "all cluster operations", kubectlClientVersion},
		{"rsync", "PVC data transfer", "backup/restore --pvcs", rsyncVersion},
		{"systemctl", "systemd schedule management", "schedule --type systemd", systemctlVersion},
		{"crontab", "cron schedule management", "schedule --type cron", nil},
	}

	logSection("External binaries")
	binTable := newTable("")
	binTable.addColumn("Binary", cyan)
	binTable.addColumn("Purpose")
	binTable.addColumn("Required for", dim)
	binTable.addColumn("Status")

	ocFound := isInPath("oc")
	kubectlFound := isInPath("kubectl")

	for _, b := range binaries {
		found := isInPath(b.Name)
		var status string
		if found {
			ver := ""
			if b.VersionFn != nil {
				ver = b.VersionFn()
			}
			if ver != "" {
				status = green("✔") + "  " + ver
			} else {
				status = green("✔  found")
			}
		} else {
			status = red("✖  not found")
		}
		binTable.addRow(b.Name, b.Purpose, b.RequiredFor, status)
	}
	binTable.print()

	// Go binary info (replaces Python packages section)
	logSection("Runtime")
	rtTable := newTable("")
	rtTable.addColumn("Item", cyan)
	rtTable.addColumn("Status")

	bi, ok := debug.ReadBuildInfo()
	if ok {
		rtTable.addRow("Go version", green("✔")+"  "+bi.GoVersion)
		rtTable.addRow("Build", green("✔")+"  "+bi.Path+" v"+version)
		for _, dep := range bi.Deps {
			if dep.Replace != nil {
				rtTable.addRow(dep.Path, green("✔")+"  "+dep.Replace.Version+" (replaced)")
			} else {
				rtTable.addRow(dep.Path, green("✔")+"  "+dep.Version)
			}
		}
	} else {
		rtTable.addRow("Runtime", green("✔  static Go binary"))
	}
	rtTable.print()

	// Cluster access (if oc/kubectl available)
	loggedIn := false
	isAdmin := false
	whoamiUser := ""

	if ocFound || kubectlFound {
		logSection("Cluster access")
		clTable := newTable("")
		clTable.addColumn("Check", cyan)
		clTable.addColumn("Status")

		rc, user, _ := runOc([]string{"whoami"}, "")
		loggedIn = rc == 0 && user != ""
		whoamiUser = user

		if loggedIn {
			clTable.addRow("Logged in", green("✔")+"  "+user)
			rc2, out2, _ := runOc([]string{"auth", "can-i", "*", "*", "--all-namespaces"}, "")
			isAdmin = rc2 == 0 && strings.TrimSpace(out2) == "yes"
			if isAdmin {
				clTable.addRow("cluster-admin", green("✔  yes"))
			} else {
				clTable.addRow("cluster-admin", yellow("⚠  no"))
			}
		} else {
			clTable.addRow("Logged in", red("✖  not logged in"))
			clTable.addRow("cluster-admin", dim("n/a"))
		}
		clTable.print()
	}

	// Summary
	fmt.Println()
	ok2 := true

	if !ocFound && !kubectlFound {
		logError("Neither oc nor kubectl found in PATH — install oc or kubectl before using okd-backup")
		ok2 = false
	} else if !ocFound {
		logInfo("oc not found — kubectl will be used as fallback (some OpenShift-specific features may be unavailable)")
	}

	if ocFound || kubectlFound {
		if !loggedIn {
			logWarning("Not logged in to the cluster — run: oc login <cluster-url>")
			ok2 = false
		} else if !isAdmin {
			logWarning(fmt.Sprintf("%s does not have cluster-admin — etcd backup and cluster-config operations will fail; namespace-level backups may still work", whoamiUser))
		}
	}

	if ok2 {
		logSuccess("All required dependencies are available")
	} else {
		os.Exit(1)
	}
	return nil
}

func isInPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func binaryVersion(args ...string) string {
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func ocClientVersion() string {
	if !isInPath("oc") {
		return ""
	}
	out, err := exec.Command("oc", "version", "--client", "-o", "json").Output()
	if err == nil {
		v := extractJSONStr(string(out), "releaseClientVersion")
		if v != "" {
			return v
		}
	}
	line := binaryVersion("oc", "version", "--client")
	// Extract version number
	for _, part := range strings.Fields(line) {
		if len(part) > 0 && (part[0] == 'v' || (part[0] >= '0' && part[0] <= '9')) {
			return part
		}
	}
	return line
}

func kubectlClientVersion() string {
	if !isInPath("kubectl") {
		return ""
	}
	out, err := exec.Command("kubectl", "version", "--client", "-o", "json").Output()
	if err == nil {
		v := extractJSONStr(string(out), "gitVersion")
		if v != "" {
			return v
		}
	}
	return binaryVersion("kubectl", "version", "--client")
}

func rsyncVersion() string {
	line := binaryVersion("rsync", "--version")
	for _, part := range strings.Fields(line) {
		if len(part) > 0 && part[0] >= '0' && part[0] <= '9' {
			return part
		}
	}
	return ""
}

func systemctlVersion() string {
	line := binaryVersion("systemctl", "--version")
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// ── config command ────────────────────────────────────────────────────────────

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage the okd-backup configuration file",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the current configuration and where it was loaded from",
	RunE:  runConfigShow,
}

var cfgShowConfig string

func init() {
	configShowCmd.Flags().StringVar(&cfgShowConfig, "config", "", "Config file path")
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	found, err := findConfigFile(cfgShowConfig)
	if err != nil {
		logError(err.Error())
		os.Exit(1)
	}
	if found == "" {
		logWarning("No config file found.")
		logInfo("Create one with: okd-backup config set backup_dir <path>  or copy okd-backup.yaml.example to okd-backup.yaml")
		return nil
	}

	logInfo(fmt.Sprintf("Config file: %s", found))
	values, err := loadConfig(cfgShowConfig)
	if err != nil {
		logError(err.Error())
		os.Exit(1)
	}

	if len(values) == 0 {
		logInfo("Config file is empty.")
		return nil
	}

	tb := newTable("")
	tb.addColumn("Key", cyan)
	tb.addColumn("Value")

	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		tb.addRow(k, values[k])
	}
	tb.print()
	return nil
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Long: `Set a configuration value.

Keys:
  backup_dir     Default backup storage directory
  control_plane  Hostname or IP of the control plane node
  ssh_key        Path to the SSH private key
  ssh_user       SSH user for control plane access (default: core)`,
	Args: cobra.ExactArgs(2),
	RunE: runConfigSet,
}

var cfgSetConfig string

func init() {
	configSetCmd.Flags().StringVar(&cfgSetConfig, "config", "", "Config file path")
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	key, value := args[0], args[1]
	if !validConfigKeys[key] {
		validList := make([]string, 0, len(validConfigKeys))
		for k := range validConfigKeys {
			validList = append(validList, k)
		}
		sort.Strings(validList)
		logError(fmt.Sprintf("Invalid key %q. Valid keys: %s", key, strings.Join(validList, ", ")))
		os.Exit(1)
	}

	target := defaultConfigPath
	if cfgSetConfig != "" {
		abs, err := filepath.Abs(expandHome(cfgSetConfig))
		if err != nil {
			return err
		}
		target = abs
	}

	existing := map[string]string{}
	if _, err := os.Stat(target); err == nil {
		existing, _ = loadConfig(target)
	}
	if existing == nil {
		existing = map[string]string{}
	}

	existing[key] = value
	written, err := saveConfig(existing, target)
	if err != nil {
		logError(fmt.Sprintf("Could not write config: %v", err))
		os.Exit(1)
	}

	logSuccess(fmt.Sprintf("Set %s = %q", key, value))
	logDim(fmt.Sprintf("Saved to: %s", written))
	return nil
}

var configUnsetCmd = &cobra.Command{
	Use:   "unset <key>",
	Short: "Remove a key from the configuration file",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigUnset,
}

var cfgUnsetConfig string

func init() {
	configUnsetCmd.Flags().StringVar(&cfgUnsetConfig, "config", "", "Config file path")
}

func runConfigUnset(cmd *cobra.Command, args []string) error {
	key := args[0]
	if !validConfigKeys[key] {
		logError(fmt.Sprintf("Invalid key %q", key))
		os.Exit(1)
	}

	target := defaultConfigPath
	if cfgUnsetConfig != "" {
		target = expandHome(cfgUnsetConfig)
	}

	existing := map[string]string{}
	if _, err := os.Stat(target); err == nil {
		existing, _ = loadConfig(target)
	}

	if _, ok := existing[key]; !ok {
		logWarning(fmt.Sprintf("%q is not set in the config file", key))
		return nil
	}

	delete(existing, key)
	if _, err := saveConfig(existing, target); err != nil {
		return err
	}
	logSuccess(fmt.Sprintf("Removed %q from config", key))
	logDim(fmt.Sprintf("Saved to: %s", target))
	return nil
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Show the config file search order and which file would be used",
	RunE:  runConfigPath,
}

func runConfigPath(cmd *cobra.Command, args []string) error {
	fmt.Printf("%sConfig file search order:%s\n", colorBold+colorBlue, colorReset)

	localAbs, _ := filepath.Abs(localConfigPath)
	_, localExists := os.Stat(localConfigPath)
	_, defaultExists := os.Stat(defaultConfigPath)

	envPath := os.Getenv("OKD_BACKUP_CONFIG")

	rows := []struct {
		label  string
		path   string
		exists bool
	}{
		{"--config flag", "(not set)", false},
		{"$OKD_BACKUP_CONFIG", func() string {
			if envPath != "" {
				return envPath
			}
			return "(not set)"
		}(), false},
		{"./okd-backup.yaml", localAbs, localExists == nil},
		{"~/.config/okd-backup/config.yaml", defaultConfigPath, defaultExists == nil},
	}

	for _, r := range rows {
		status := dim("not found")
		if r.exists {
			status = green("✔ exists")
		}
		fmt.Printf("  %-35s %-50s %s\n", r.label, r.path, status)
	}

	fmt.Println()
	active, _ := findConfigFile("")
	if active != "" {
		fmt.Printf("%sActive config:%s %s\n", colorGreen, colorReset, active)
	} else {
		fmt.Printf("%sNo config file found — using built-in defaults%s\n", colorDim, colorReset)
	}
	return nil
}

// ── completion command ────────────────────────────────────────────────────────

var completionCmd = &cobra.Command{
	Use:   "completion <shell>",
	Short: "Print shell completion script to stdout",
	Long: `Print shell completion script to stdout.

Usage:
  source <(okd-backup completion bash)
  source <(okd-backup completion zsh)

To load permanently, add to your shell profile:
  # ~/.bashrc or ~/.bash_profile
  source <(okd-backup completion bash)

  # ~/.zshrc
  autoload -U compinit && compinit
  source <(okd-backup completion zsh)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			fmt.Println(strings.TrimSpace(bashCompletion))
		case "zsh":
			fmt.Println(strings.TrimSpace(zshCompletion))
		default:
			logError(fmt.Sprintf("Unknown shell %q — use bash or zsh", args[0]))
			os.Exit(1)
		}
		return nil
	},
}

const bashCompletion = `
# bash completion for okd-backup
# Usage: source <(okd-backup completion bash)

_okd_backup_completion() {
    local cur prev words cword
    _init_completion 2>/dev/null || {
        COMPREPLY=()
        cur="${COMP_WORDS[COMP_CWORD]}"
        prev="${COMP_WORDS[COMP_CWORD-1]}"
        words=("${COMP_WORDS[@]}")
        cword=$COMP_CWORD
    }

    local top_commands="backup restore list info completion detect deps config schedule cleanup version --help"
    local restore_subcommands="list run"
    local schedule_subcommands="generate list remove"
    local config_subcommands="show set unset path"
    local config_keys="backup_dir control_plane ssh_key ssh_user"

    _okd_backup_get_backup_dir() {
        local dir="/mnt/nfs/okd-backups"
        local i
        for (( i=1; i<${#words[@]}; i++ )); do
            if [[ "${words[$i]}" == "--backup-dir" && -n "${words[$i+1]}" ]]; then
                dir="${words[$i+1]}"
                echo "$dir"; return
            fi
        done
        local cfg_file=""
        for (( i=1; i<${#words[@]}; i++ )); do
            if [[ "${words[$i]}" == "--config" && -n "${words[$i+1]}" ]]; then
                cfg_file="${words[$i+1]}"
                break
            fi
        done
        [[ -z "$cfg_file" ]] && cfg_file="${OKD_BACKUP_CONFIG:-}"
        [[ -z "$cfg_file" && -f "./okd-backup.yaml" ]] && cfg_file="./okd-backup.yaml"
        [[ -z "$cfg_file" ]] && cfg_file="$HOME/.config/okd-backup/config.yaml"
        if [[ -f "$cfg_file" ]]; then
            local val
            val=$(grep -E '^\s*backup_dir\s*:' "$cfg_file" 2>/dev/null \
                  | head -1 | sed 's/.*:\s*//' | tr -d '"'"'"' ')
            [[ -n "$val" ]] && dir="$val"
        fi
        [[ ! -d "$dir" ]] && dir="$(pwd)"
        echo "$dir"
    }

    _okd_backup_list_ids() {
        local dir
        dir=$(_okd_backup_get_backup_dir)
        if [[ -d "$dir" ]]; then
            ls -1 "$dir" 2>/dev/null | grep -E '^[0-9]{4}-[0-9]{2}-[0-9]{2}_[0-9]{4}$'
        fi
    }

    _okd_backup_list_namespaces() {
        oc get namespaces -o jsonpath='{.items[*].metadata.name}' 2>/dev/null \
        || kubectl get namespaces -o jsonpath='{.items[*].metadata.name}' 2>/dev/null
    }

    local cmd="" subcmd=""
    local i
    for (( i=1; i<cword; i++ )); do
        case "${words[$i]}" in
            backup|info|completion|detect|deps|cleanup)
                cmd="${words[$i]}"; break ;;
            restore|schedule|config)
                cmd="${words[$i]}" ;;
            list|run)
                [[ "$cmd" == "restore" ]] && { subcmd="${words[$i]}"; break; } ;;
            generate|remove)
                [[ "$cmd" == "schedule" ]] && { subcmd="${words[$i]}"; cmd="schedule_${subcmd}"; break; } ;;
            show|set|unset|path)
                [[ "$cmd" == "config" ]] && { subcmd="${words[$i]}"; cmd="config_${subcmd}"; break; } ;;
        esac
    done

    case "$prev" in
        --backup-dir) _filedir -d; return ;;
        --config)     _filedir; return ;;
        --control-plane) COMPREPLY=( $(compgen -A hostname -- "$cur") ); return ;;
        --ssh-key)    _filedir; return ;;
        --ssh-user)   COMPREPLY=( $(compgen -u -- "$cur") ); return ;;
        --log-file)   _filedir; return ;;
        --namespace|--namespaces)
            local ns_list
            ns_list=$(_okd_backup_list_namespaces)
            COMPREPLY=( $(compgen -W "$ns_list" -- "$cur") ); return ;;
        --backup-id)
            local ids
            ids=$(_okd_backup_list_ids)
            COMPREPLY=( $(compgen -W "$ids" -- "$cur") ); return ;;
        --type)
            case "$cmd" in
                restore_run)
                    local resource_types="deployments statefulsets daemonsets services \
                        configmaps secrets serviceaccounts roles rolebindings \
                        persistentvolumeclaims ingresses routes cronjobs jobs \
                        horizontalpodautoscalers networkpolicies resourcequotas \
                        limitranges imagestreams"
                    COMPREPLY=( $(compgen -W "$resource_types" -- "$cur") ) ;;
                schedule_generate) COMPREPLY=( $(compgen -W "systemd cron" -- "$cur") ) ;;
                schedule_remove)   COMPREPLY=( $(compgen -W "systemd cron all" -- "$cur") ) ;;
                *) COMPREPLY=( $(compgen -W "systemd cron" -- "$cur") ) ;;
            esac
            return ;;
        --unit)   COMPREPLY=( $(compgen -W "GB MB" -- "$cur") ); return ;;
        --preset) COMPREPLY=( $(compgen -W "hourly daily weekly monthly" -- "$cur") ); return ;;
        --keep|--older-than|--ssh-port|--timeout) COMPREPLY=(); return ;;
        --unit-name)
            local unit_names
            unit_names=$(systemctl list-timers --all --no-pager 2>/dev/null \
                | awk '/okd-backup/{print $NF}' | sed 's/\.timer$//')
            [[ -z "$unit_names" ]] && unit_names="okd-backup"
            COMPREPLY=( $(compgen -W "$unit_names" -- "$cur") ); return ;;
    esac

    case "$cmd" in
        "") COMPREPLY=( $(compgen -W "$top_commands" -- "$cur") ) ;;

        backup)
            local opts="--all --etcd --namespaces --pvcs --cluster-config \
                --no-secrets --backup-dir --control-plane --ssh-key --ssh-user \
                --config --dry-run --verbose --help"
            COMPREPLY=( $(compgen -W "$opts" -- "$cur") ) ;;

        restore)
            if [[ -z "$subcmd" ]]; then
                COMPREPLY=( $(compgen -W "$restore_subcommands --help" -- "$cur") )
            elif [[ "$subcmd" == "list" ]]; then
                COMPREPLY=( $(compgen -W "--backup-dir --unit --config --help" -- "$cur") )
            elif [[ "$subcmd" == "run" ]]; then
                local opts="--backup-id --namespace --namespaces --type --pvcs \
                    --cluster-config --force-config --map-namespace \
                    --backup-dir --config --dry-run --verbose --help"
                COMPREPLY=( $(compgen -W "$opts" -- "$cur") )
            fi ;;

        list)
            COMPREPLY=( $(compgen -W "--backup-dir --unit --config --help" -- "$cur") ) ;;

        info)
            case "$cur" in
                -*) COMPREPLY=( $(compgen -W "--backup-dir --unit --config --help" -- "$cur") ) ;;
                *)  local ids; ids=$(_okd_backup_list_ids)
                    COMPREPLY=( $(compgen -W "$ids" -- "$cur") ) ;;
            esac ;;

        cleanup)
            local opts="--keep --older-than --backup-id --empty \
                --backup-dir --config --dry-run --yes --help"
            COMPREPLY=( $(compgen -W "$opts" -- "$cur") ) ;;

        detect)
            COMPREPLY=( $(compgen -W "--ssh-port --timeout --save --config --help" -- "$cur") ) ;;

        deps)
            COMPREPLY=( $(compgen -W "--help" -- "$cur") ) ;;

        schedule)
            if [[ -z "$subcmd" ]]; then
                COMPREPLY=( $(compgen -W "$schedule_subcommands --help" -- "$cur") )
            fi ;;

        schedule_generate)
            local opts="--preset --on-calendar --cron --backup-args \
                --user --unit-name --install --type --log-file --help"
            COMPREPLY=( $(compgen -W "$opts" -- "$cur") ) ;;

        schedule_list)   COMPREPLY=( $(compgen -W "--help" -- "$cur") ) ;;
        schedule_remove) COMPREPLY=( $(compgen -W "--unit-name --type --yes --help" -- "$cur") ) ;;

        config)
            if [[ -z "$subcmd" ]]; then
                COMPREPLY=( $(compgen -W "$config_subcommands --help" -- "$cur") )
            fi ;;

        config_set)
            local set_key=""
            for (( i=1; i<cword; i++ )); do
                [[ "${words[$i]}" == "set" ]] && { set_key="${words[$i+1]:-}"; break; }
            done
            if [[ -z "$set_key" || "$cur" == "$set_key" ]]; then
                COMPREPLY=( $(compgen -W "$config_keys" -- "$cur") )
            else
                case "$set_key" in
                    backup_dir)    _filedir -d ;;
                    ssh_key)       _filedir ;;
                    control_plane) COMPREPLY=( $(compgen -A hostname -- "$cur") ) ;;
                    ssh_user)      COMPREPLY=( $(compgen -u -- "$cur") ) ;;
                esac
            fi ;;

        config_unset)         COMPREPLY=( $(compgen -W "$config_keys" -- "$cur") ) ;;
        config_show|config_path) COMPREPLY=( $(compgen -W "--config --help" -- "$cur") ) ;;
        completion)           COMPREPLY=( $(compgen -W "bash zsh" -- "$cur") ) ;;
    esac
}

complete -F _okd_backup_completion okd-backup
`

const zshCompletion = `
# zsh completion for okd-backup
# Usage: source <(okd-backup completion zsh)

_okd_backup() {
    local state line
    typeset -A opt_args

    local resource_types=(
        deployments statefulsets daemonsets services configmaps secrets
        serviceaccounts roles rolebindings persistentvolumeclaims
        ingresses routes cronjobs jobs horizontalpodautoscalers
        networkpolicies resourcequotas limitranges imagestreams
    )

    local config_keys=(backup_dir control_plane ssh_key ssh_user)

    local _backup_dir="/mnt/nfs/okd-backups"
    if [[ -n "${opt_args[--backup-dir]}" ]]; then
        _backup_dir="${opt_args[--backup-dir]}"
    else
        local _cfg="${OKD_BACKUP_CONFIG:-}"
        [[ -z "$_cfg" && -f "./okd-backup.yaml" ]] && _cfg="./okd-backup.yaml"
        [[ -z "$_cfg" ]] && _cfg="$HOME/.config/okd-backup/config.yaml"
        if [[ -f "$_cfg" ]]; then
            local _val
            _val=$(grep -E '^\s*backup_dir\s*:' "$_cfg" 2>/dev/null \
                   | head -1 | sed 's/.*:\s*//' | tr -d '"'"'"' ')
            [[ -n "$_val" ]] && _backup_dir="$_val"
        fi
        [[ ! -d "$_backup_dir" ]] && _backup_dir="$(pwd)"
    fi

    _arguments \
        '--help[Show help]' \
        ':command:->command' \
        '*::args:->args'

    case $state in
        command)
            local commands=(
                'backup:Create a backup of the OKD cluster'
                'restore:Restore resources from a backup'
                'list:List all available backups'
                'info:Show details of a specific backup'
                'detect:Detect control plane nodes in the cluster'
                'deps:Show required dependencies and check availability'
                'config:Manage the configuration file'
                'schedule:Manage automated backup schedules'
                'cleanup:Remove old or unwanted backups'
                'completion:Generate shell completion script'
                'version:Print the version of okd-backup'
            )
            _describe 'command' commands
            ;;

        args)
            case $line[1] in
                backup)
                    _arguments \
                        '--all[Back up everything]' \
                        '--etcd[etcd snapshot]' \
                        '--namespaces[Namespaces]:ns:->namespaces' \
                        '--pvcs[Back up PVC data]' \
                        '--cluster-config[Cluster-wide config]' \
                        '--no-secrets[Skip secrets]' \
                        '--backup-dir[Storage location]:dir:_files -/' \
                        '--control-plane[Control plane host]:host:_hosts' \
                        '--ssh-key[SSH key]:key:_files' \
                        '--ssh-user[SSH user]:user:_users' \
                        '--config[Config file]:file:_files' \
                        '--dry-run[Simulate]' \
                        '--verbose[Show oc output]'
                    ;;

                restore)
                    local subcommands=(
                        'list:List all available backups'
                        'run:Restore resources from a backup'
                    )
                    _arguments ':subcommand:->subcommand' '*::args:->subargs'
                    case $state in
                        subcommand) _describe 'subcommand' subcommands ;;
                        subargs)
                            case $line[1] in
                                list)
                                    _arguments \
                                        '--backup-dir[Storage location]:dir:_files -/' \
                                        '--unit[Size unit]:unit:(GB MB)' \
                                        '--config[Config file]:file:_files'
                                    ;;
                                run)
                                    _arguments \
                                        '--backup-id[Backup ID]:id:->backup_ids' \
                                        '--namespace[Namespace]:ns:->namespaces' \
                                        '--namespaces[Namespaces]:ns:->namespaces' \
                                        '--type[Resource type]:type:('"${resource_types[*]}"')' \
                                        '--pvcs[Restore PVC data]' \
                                        '--cluster-config[Restore cluster config]' \
                                        '--force-config[Include dangerous resources]' \
                                        '--map-namespace[Namespace mapping old:new]:map:->ns_map' \
                                        '--backup-dir[Storage location]:dir:_files -/' \
                                        '--config[Config file]:file:_files' \
                                        '--dry-run[Simulate]' \
                                        '--verbose[Show oc output]'
                                    ;;
                            esac
                            ;;
                    esac
                    ;;

                list)
                    _arguments \
                        '--backup-dir[Storage location]:dir:_files -/' \
                        '--unit[Size unit]:unit:(GB MB)' \
                        '--config[Config file]:file:_files'
                    ;;

                info)
                    _arguments \
                        ':backup_id:->backup_ids' \
                        '--backup-dir[Storage location]:dir:_files -/' \
                        '--unit[Size unit]:unit:(GB MB)' \
                        '--config[Config file]:file:_files'
                    ;;

                cleanup)
                    _arguments \
                        '--keep[Keep N most recent]:n:' \
                        '--older-than[Remove older than N days]:days:' \
                        '--backup-id[Backup ID to remove]:id:->backup_ids' \
                        '--empty[Remove empty backups]' \
                        '--backup-dir[Storage location]:dir:_files -/' \
                        '--config[Config file]:file:_files' \
                        '--dry-run[Preview only]' \
                        '(-y --yes)'{-y,--yes}'[Skip confirmation]'
                    ;;

                detect)
                    _arguments \
                        '--ssh-port[SSH port]:port:' \
                        '--timeout[TCP timeout]:seconds:' \
                        '--save[Save to config]' \
                        '--config[Config file]:file:_files'
                    ;;

                deps) ;;

                schedule)
                    local sched_subcmds=(
                        'generate:Generate and optionally install a schedule'
                        'list:List installed schedules'
                        'remove:Remove an installed schedule'
                    )
                    _arguments ':subcommand:->sched_sub' '*::args:->sched_args'
                    case $state in
                        sched_sub) _describe 'subcommand' sched_subcmds ;;
                        sched_args)
                            case $line[1] in
                                generate)
                                    _arguments \
                                        '--preset[Preset]:p:(hourly daily weekly monthly)' \
                                        '--on-calendar[OnCalendar expr]:e:' \
                                        '--cron[Cron expr]:e:' \
                                        '--backup-args[Backup args]:a:' \
                                        '--user[User]:u:_users' \
                                        '--unit-name[Unit name]:n:' \
                                        '--install[Install]' \
                                        '--type[Type]:t:(systemd cron)' \
                                        '--log-file[Log file]:f:_files'
                                    ;;
                                list) ;;
                                remove)
                                    _arguments \
                                        '--unit-name[Unit name]:n:' \
                                        '--type[Type]:t:(systemd cron all)' \
                                        '(-y --yes)'{-y,--yes}'[Skip confirm]'
                                    ;;
                            esac
                            ;;
                    esac
                    ;;

                config)
                    local cfg_subcmds=(
                        'show:Show active configuration'
                        'set:Set a configuration value'
                        'unset:Remove a configuration key'
                        'path:Show config file search order'
                    )
                    _arguments ':subcommand:->cfg_sub' '*::args:->cfg_args'
                    case $state in
                        cfg_sub) _describe 'subcommand' cfg_subcmds ;;
                        cfg_args)
                            case $line[1] in
                                set)
                                    _arguments \
                                        ':key:(backup_dir control_plane ssh_key ssh_user)' \
                                        ':value:->config_value'
                                    case $state in
                                        config_value)
                                            case $line[1] in
                                                backup_dir)    _files -/ ;;
                                                ssh_key)       _files ;;
                                                control_plane) _hosts ;;
                                                ssh_user)      _users ;;
                                            esac
                                            ;;
                                    esac
                                    ;;
                                unset)
                                    _arguments ':key:(backup_dir control_plane ssh_key ssh_user)'
                                    ;;
                                show|path)
                                    _arguments '--config[Config file]:file:_files'
                                    ;;
                            esac
                            ;;
                    esac
                    ;;

                completion)
                    _arguments ':shell:(bash zsh)'
                    ;;

                version) ;;

            esac

            case $state in
                namespaces)
                    local ns_list
                    ns_list=(${(f)"$(oc get namespaces \
                        -o jsonpath='{.items[*].metadata.name}' 2>/dev/null \
                        | tr ' ' '\n')"})
                    _values 'namespace' $ns_list
                    ;;
                backup_ids)
                    local ids
                    ids=(${(f)"$(ls -1 $_backup_dir 2>/dev/null \
                        | grep -E '^[0-9]{4}-[0-9]{2}-[0-9]{2}_[0-9]{4}$')"})
                    _values 'backup_id' $ids
                    ;;
                ns_map)
                    local ns_list
                    ns_list=(${(f)"$(oc get namespaces \
                        -o jsonpath='{.items[*].metadata.name}' 2>/dev/null \
                        | tr ' ' '\n')"})
                    local pairs=()
                    for ns in $ns_list; do
                        pairs+=("$ns:staging" "$ns:dev")
                    done
                    _values 'mapping' $pairs
                    ;;
            esac
            ;;
    esac
}

compdef _okd_backup okd-backup
`
