package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const restorePodPrefix = "okd-restore-pvc-"

// Safe cluster-config files to restore without --force-config
var safeToRestore = []string{
	"oauth.yaml",
	"ingress.yaml",
	"proxies.yaml",
	"apiservers.yaml",
	"schedulers.yaml",
	"operator-subscriptions.yaml",
	"operator-catalogsources.yaml",
	"operator-groups.yaml",
	"storageclasses.yaml",
	"clusterroles.yaml",
	"clusterrolebindings.yaml",
}

// Force-only cluster-config files (require --force-config)
var forceOnly = []string{
	"clusterversion.yaml",
	"clusteroperators.yaml",
	"crds.yaml",
}

// ── etcd restore ─────────────────────────────────────────────────────────────

func restoreEtcd(ctx *BackupContext, host, sshKeyPath, sshUser string, yes bool, dryRun bool) error {
	logSection("etcd restore")

	if !ctx.HasContent("etcd") {
		logWarning("This backup does not contain etcd data")
		return nil
	}

	etcdDir := filepath.Join(ctx.Path, "etcd")
	files, err := os.ReadDir(etcdDir)
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no etcd backup files found in %s", etcdDir)
	}

	if dryRun {
		logVerbose(fmt.Sprintf("  [dry-run] SSH %s@%s", sshUser, host))
		logVerbose(fmt.Sprintf("  [dry-run] SFTP upload %s → /tmp/etcd-restore-XXXXX/", etcdDir))
		logVerbose("  [dry-run] sudo /usr/local/bin/cluster-restore.sh /tmp/etcd-restore-XXXXX/")
		logSuccess("etcd restore (dry-run)")
		return nil
	}

	if !yes {
		fmt.Println()
		logWarning("etcd restore is DESTRUCTIVE — all current cluster data will be replaced.")
		logWarning("Prerequisites:")
		logWarning("  1. All OTHER control plane nodes must be powered off or fenced first")
		logWarning("  2. The cluster API will be unavailable during restore")
		logWarning("  3. After restore, other control plane nodes must be reprovisioned")
		fmt.Printf("\nProceed with etcd restore on %s? [y/N] ", host)
		var ans string
		fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			logInfo("Aborted.")
			return nil
		}
	}

	logInfo(fmt.Sprintf("  Connecting to %s@%s …", sshUser, host))
	client, err := sshConnect(host, sshUser, sshKeyPath)
	if err != nil {
		return err
	}
	defer client.Close()

	// Create temp dir on remote
	tmpOut, _, err := sshRun(client, "mktemp -d /tmp/etcd-restore-XXXXXXXXXX")
	if err != nil {
		return fmt.Errorf("create remote temp dir: %w", err)
	}
	remoteDir := strings.TrimSpace(tmpOut)
	logInfo(fmt.Sprintf("  Remote temp dir: %s", remoteDir))

	// Upload snapshot and static resource files
	logInfo("  Uploading etcd backup files via SFTP …")
	if err := sftpUploadDir(client, etcdDir, remoteDir); err != nil {
		_, _, _ = sshRun(client, "rm -rf "+remoteDir)
		return fmt.Errorf("SFTP upload: %w", err)
	}

	// Run the OKD restore script
	restoreScript := "/usr/local/bin/cluster-restore.sh"
	logInfo(fmt.Sprintf("  Running %s …", restoreScript))
	logWarning("  The cluster API will be unavailable during this process.")

	stdout, stderr, err := sshRun(client, fmt.Sprintf("sudo %s %s", restoreScript, remoteDir))

	// Always print restore script output — it contains important status messages
	if stdout != "" {
		fmt.Println(stdout)
	}
	if stderr != "" {
		fmt.Fprintln(os.Stderr, stderr)
	}

	_, _, _ = sshRun(client, "sudo rm -rf "+remoteDir)

	if err != nil {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(stdout)
		}
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("cluster-restore.sh failed: %s", detail)
	}

	logSuccess("etcd restore completed")
	logSection("Post-restore steps")
	logInfo("  1. Restart the kubelet on this control plane node:")
	logInfo("       sudo systemctl restart kubelet")
	logInfo("  2. Wait for the API server to come back:")
	logInfo("       oc get nodes")
	logInfo("  3. Force other control plane nodes to rejoin or reprovision them")
	logInfo("  4. Approve any pending CSRs:")
	logInfo("       oc get csr | awk '/Pending/{print $1}' | xargs oc adm certificate approve")
	return nil
}

// ── namespace restore ─────────────────────────────────────────────────────────

func restoreNamespaces(
	ctx *BackupContext,
	namespaces []string,
	resourceTypes []string,
	nsMapping map[string]string,
	yes bool,
	dryRun bool,
) error {
	logSection("Namespace restore")

	if !ctx.HasContent("namespaces") {
		logWarning("This backup does not contain namespace data")
		return nil
	}

	nsDir := filepath.Join(ctx.Path, "namespaces")
	entries, err := os.ReadDir(nsDir)
	if err != nil {
		return fmt.Errorf("read namespaces dir: %w", err)
	}

	type nsWork struct{ src, target string }
	var work []nsWork
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ns := entry.Name()
		if len(namespaces) > 0 && !containsStr(namespaces, ns) {
			continue
		}
		targetNS := ns
		if nsMapping != nil {
			if mapped, ok := nsMapping[ns]; ok {
				targetNS = mapped
			}
		}
		work = append(work, nsWork{src: ns, target: targetNS})
	}

	// Check/create all target namespaces before starting any restore
	for _, w := range work {
		if err := ensureNamespaceExists(w.target, yes, dryRun); err != nil {
			return err
		}
	}

	for _, w := range work {
		if w.src == w.target {
			logInfo(fmt.Sprintf("  Namespace: %s", w.src))
		} else {
			logInfo(fmt.Sprintf("  Namespace: %s → %s", w.src, w.target))
		}
		if err := restoreNamespace(filepath.Join(nsDir, w.src), w.src, w.target, resourceTypes, dryRun); err != nil {
			logWarning(fmt.Sprintf("  %s: %v", w.src, err))
		}
	}

	logSuccess("Namespace restore completed")
	return nil
}

func ensureNamespaceExists(ns string, yes bool, dryRun bool) error {
	rc, _, _ := runOc([]string{"get", "namespace", ns}, "")
	if rc == 0 {
		return nil
	}
	if dryRun {
		logInfo(fmt.Sprintf("  DRY-RUN: namespace %q does not exist — would create it", ns))
		return nil
	}
	if !yes {
		fmt.Printf("  Namespace %q does not exist. Create it? [y/N] ", ns)
		var ans string
		fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			return fmt.Errorf("namespace %q does not exist — aborting", ns)
		}
	}
	rc, out, _ := runOc([]string{"create", "namespace", ns}, "")
	if rc != 0 {
		return fmt.Errorf("create namespace %q: %s", ns, strings.TrimSpace(out))
	}
	logSuccess(fmt.Sprintf("  Created namespace %q", ns))
	return nil
}

func restoreNamespace(nsDir, ns, targetNS string, resourceTypes []string, dryRun bool) error {
	entries, err := os.ReadDir(nsDir)
	if err != nil {
		return err
	}

	// Sort for deterministic order
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		rt := strings.TrimSuffix(entry.Name(), ".yaml")
		if len(resourceTypes) > 0 && !containsStr(resourceTypes, rt) {
			continue
		}

		yamlFile := filepath.Join(nsDir, entry.Name())
		if err := restoreYAMLFile(yamlFile, ns, targetNS, dryRun); err != nil {
			logWarning(fmt.Sprintf("    ✗ %s/%s: %v", ns, entry.Name(), err))
		} else {
			logVerbose(fmt.Sprintf("    ✔ %s/%s", ns, entry.Name()))
		}
	}
	return nil
}

func restoreYAMLFile(yamlFile, srcNS, targetNS string, dryRun bool) error {
	data, err := os.ReadFile(yamlFile)
	if err != nil {
		return err
	}

	var obj map[string]interface{}
	if err := yaml.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("parse YAML: %w", err)
	}
	if obj == nil {
		return nil
	}
	if isEmptyItems(obj) {
		return nil
	}

	obj = stripRuntimeFields(obj)
	if isEmptyItems(obj) { // re-check: filtering may have removed all items
		return nil
	}

	// Apply namespace mapping
	if targetNS != srcNS {
		applyNSMapping(obj, srcNS, targetNS)
	}

	// Write to temp file and apply
	tmpFile, err := os.CreateTemp("", "okd-restore-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	enc := yaml.NewEncoder(tmpFile)
	if err := enc.Encode(obj); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	return applyFile(tmpFile.Name(), dryRun)
}

func stripRuntimeFields(obj map[string]interface{}) map[string]interface{} {
	if meta, ok := obj["metadata"].(map[string]interface{}); ok {
		for _, field := range []string{"resourceVersion", "uid", "creationTimestamp", "generation", "managedFields"} {
			delete(meta, field)
		}
	}
	delete(obj, "status")

	kind, _ := obj["kind"].(string)
	if spec, ok := obj["spec"].(map[string]interface{}); ok {
		// Strip auto-assigned cluster IPs from Services (keep "None" for headless services)
		if kind == "Service" {
			if ip, _ := spec["clusterIP"].(string); ip != "None" {
				delete(spec, "clusterIP")
				delete(spec, "clusterIPs")
			}
		}
		// Strip auto-generated Job selector and UID-based pod template labels
		if kind == "Job" {
			delete(spec, "selector")
			if tmpl, ok := spec["template"].(map[string]interface{}); ok {
				if tmplMeta, ok := tmpl["metadata"].(map[string]interface{}); ok {
					if labels, ok := tmplMeta["labels"].(map[string]interface{}); ok {
						for _, key := range []string{
							"controller-uid", "job-name",
							"batch.kubernetes.io/controller-uid", "batch.kubernetes.io/job-name",
						} {
							delete(labels, key)
						}
					}
				}
			}
		}
		// Strip PVC volume binding so the provisioner assigns a new PV on restore
		if kind == "PersistentVolumeClaim" {
			delete(spec, "volumeName")
		}
	}

	// Strip auto-managed SA token secret references (controller recreates them)
	if kind == "ServiceAccount" {
		delete(obj, "secrets")
	}

	if items, ok := obj["items"].([]interface{}); ok {
		stripped := make([]interface{}, 0, len(items))
		for _, item := range items {
			if m, ok := item.(map[string]interface{}); ok {
				if isAutoManagedSecret(m) {
					continue // controller recreates these; manual apply is forbidden in OKD 4.x
				}
				stripped = append(stripped, stripRuntimeFields(m))
			} else {
				stripped = append(stripped, item)
			}
		}
		obj["items"] = stripped
	}
	return obj
}

func isAutoManagedSecret(obj map[string]interface{}) bool {
	if kind, _ := obj["kind"].(string); kind != "Secret" {
		return false
	}
	t, _ := obj["type"].(string)
	return t == "kubernetes.io/service-account-token" || t == "kubernetes.io/dockercfg"
}

func applyNSMapping(obj map[string]interface{}, srcNS, targetNS string) {
	if meta, ok := obj["metadata"].(map[string]interface{}); ok {
		if ns, _ := meta["namespace"].(string); ns == srcNS {
			meta["namespace"] = targetNS
		}
	}
	if items, ok := obj["items"].([]interface{}); ok {
		for _, item := range items {
			if m, ok := item.(map[string]interface{}); ok {
				applyNSMapping(m, srcNS, targetNS)
			}
		}
	}
}

func isEmptyItems(obj map[string]interface{}) bool {
	items, ok := obj["items"]
	if !ok {
		return false
	}
	list, ok := items.([]interface{})
	return ok && len(list) == 0
}

// ── PVC restore ───────────────────────────────────────────────────────────────

func restorePVCs(ctx *BackupContext, namespaces []string, pvcNames []string, dryRun bool) error {
	logSection("PVC restore")

	if !ctx.HasContent("pvcs") {
		logWarning("This backup does not contain PVC data")
		return nil
	}

	pvcsDir := filepath.Join(ctx.Path, "pvcs")
	nsDirs, err := os.ReadDir(pvcsDir)
	if err != nil {
		return fmt.Errorf("read pvcs dir: %w", err)
	}

	for _, nsEntry := range nsDirs {
		if !nsEntry.IsDir() {
			continue
		}
		ns := nsEntry.Name()
		if len(namespaces) > 0 && !containsStr(namespaces, ns) {
			continue
		}

		logInfo(fmt.Sprintf("  Namespace: %s", ns))
		restricted := isRestrictedNamespace(ns, dryRun)
		if restricted {
			logVerbose(fmt.Sprintf("  %s: PodSecurity restricted — using hardened securityContext", ns))
		}

		nsDir := filepath.Join(pvcsDir, ns)
		pvcDirs, err := os.ReadDir(nsDir)
		if err != nil {
			continue
		}

		for _, pvcEntry := range pvcDirs {
			if !pvcEntry.IsDir() {
				continue
			}
			pvcName := pvcEntry.Name()
			if len(pvcNames) > 0 && !containsStr(pvcNames, pvcName) {
				continue
			}
			restoreSinglePVC(ns, pvcName, filepath.Join(nsDir, pvcName), restricted, dryRun)
		}
	}

	logSuccess("PVC restore completed")
	return nil
}

func restoreSinglePVC(ns, pvcName, localDir string, restricted, dryRun bool) {
	logInfo(fmt.Sprintf("    PVC: %s", pvcName))
	podName := restorePodPrefix + truncate(pvcName, 30)

	if dryRun {
		logVerbose(fmt.Sprintf("    [dry-run] create restore pod for %s (restricted=%v)", pvcName, restricted))
		logVerbose(fmt.Sprintf("    [dry-run] oc rsync %s/ %s:%s/", localDir, podName, mountPath))
		return
	}

	defer deletePod(podName, ns)

	if err := createPVCPod(podName, ns, pvcName, restricted, "restore"); err != nil {
		logError(fmt.Sprintf("    ✖ %s: create pod: %v", pvcName, err))
		return
	}
	if err := waitForPod(podName, ns, 300); err != nil {
		logError(fmt.Sprintf("    ✖ %s: %v", pvcName, err))
		return
	}
	if err := rsyncToPod(podName, ns, localDir); err != nil {
		logError(fmt.Sprintf("    ✖ %s: rsync: %v", pvcName, err))
		return
	}
	logSuccess(fmt.Sprintf("    ✔ %s restored", pvcName))
}

// ── cluster-config restore ────────────────────────────────────────────────────

func restoreClusterConfig(ctx *BackupContext, resources []string, force bool, dryRun bool) error {
	logSection("Cluster configuration restore")

	if !ctx.HasContent("cluster-config") {
		logWarning("This backup does not contain cluster configuration")
		return nil
	}

	ccDir := filepath.Join(ctx.Path, "cluster-config")

	var toRestore []string
	if len(resources) > 0 {
		toRestore = resources
	} else if force {
		toRestore = append(safeToRestore, forceOnly...)
		logWarning("--force-config active: dangerous resources will also be restored")
	} else {
		toRestore = safeToRestore
		// Warn about skipped force-only resources
		var skipped []string
		for _, f := range forceOnly {
			if _, err := os.Stat(filepath.Join(ccDir, f)); err == nil {
				skipped = append(skipped, f)
			}
		}
		if len(skipped) > 0 {
			logWarning(fmt.Sprintf("Skipped (use --force-config to restore): %s", strings.Join(skipped, ", ")))
		}
	}

	for _, filename := range toRestore {
		yamlFile := filepath.Join(ccDir, filename)
		if _, err := os.Stat(yamlFile); os.IsNotExist(err) {
			logVerbose(fmt.Sprintf("  %s: not in backup, skipped", filename))
			continue
		}

		if err := applyFile(yamlFile, dryRun); err != nil {
			logWarning(fmt.Sprintf("  ✗ %s: %v", filename, err))
		} else {
			logVerbose(fmt.Sprintf("  ✔ %s", filename))
		}
	}

	logSuccess("Cluster configuration restore completed")
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
