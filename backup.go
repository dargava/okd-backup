package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// ── Constants ─────────────────────────────────────────────────────────────────

const (
	backupPodPrefix    = "okd-backup-pvc-"
	mountPath          = "/mnt/pvc"
	defaultPVCImage    = "registry.access.redhat.com/ubi8/ubi8:latest"
)

// pvcImage is the container image used for PVC backup/restore pods.
// Set by the backup/restore commands via --pvc-image or the pvc_image config key.
var pvcImage = defaultPVCImage

var namespaceResourceTypes = []string{
	"deployments", "statefulsets", "daemonsets", "services",
	"configmaps", "secrets", "serviceaccounts", "roles", "rolebindings",
	"persistentvolumeclaims", "ingresses", "routes", "cronjobs", "jobs",
	"horizontalpodautoscalers", "networkpolicies", "resourcequotas",
	"limitranges", "imagestreams",
}

var clusterResources = []struct{ Kind, File string }{
	{"oauth", "oauth.yaml"},
	{"ingresses.config.openshift.io", "ingress.yaml"},
	{"proxies.config.openshift.io", "proxies.yaml"},
	{"apiservers.config.openshift.io", "apiservers.yaml"},
	{"schedulers.config.openshift.io", "schedulers.yaml"},
	{"subscriptions.operators.coreos.com --all-namespaces", "operator-subscriptions.yaml"},
	{"catalogsources.operators.coreos.com --all-namespaces", "operator-catalogsources.yaml"},
	{"operatorgroups.operators.coreos.com --all-namespaces", "operator-groups.yaml"},
	{"storageclasses", "storageclasses.yaml"},
	{"clusterroles", "clusterroles.yaml"},
	{"clusterrolebindings", "clusterrolebindings.yaml"},
	{"clusterversion", "clusterversion.yaml"},
	{"clusteroperators", "clusteroperators.yaml"},
	{"customresourcedefinitions", "crds.yaml"},
}

// ── SSH helpers ───────────────────────────────────────────────────────────────

func sshConnect(host, user, keyPath string) (*ssh.Client, error) {
	keyPath = expandHome(keyPath)
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read SSH key %s: %w", keyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse SSH key: %w", err)
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         30 * time.Second,
	}

	client, err := ssh.Dial("tcp", host+":22", config)
	if err != nil {
		return nil, fmt.Errorf("SSH connect to %s: %w", host, err)
	}
	return client, nil
}

func sshRun(client *ssh.Client, cmd string) (string, string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", "", err
	}
	defer sess.Close()

	var stdout, stderr strings.Builder
	sess.Stdout = &stdout
	sess.Stderr = &stderr

	err = sess.Run(cmd)
	return stdout.String(), stderr.String(), err
}

func sftpDownloadDir(client *ssh.Client, remotePath, localPath string) error {
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("SFTP init: %w", err)
	}
	defer sftpClient.Close()

	return sftpWalk(sftpClient, remotePath, localPath)
}

func sftpWalk(sftpClient *sftp.Client, remote, local string) error {
	entries, err := sftpClient.ReadDir(remote)
	if err != nil {
		return fmt.Errorf("read remote dir %s: %w", remote, err)
	}

	for _, entry := range entries {
		rpath := remote + "/" + entry.Name()
		lpath := filepath.Join(local, entry.Name())

		if entry.IsDir() {
			if err := os.MkdirAll(lpath, 0755); err != nil {
				return err
			}
			if err := sftpWalk(sftpClient, rpath, lpath); err != nil {
				return err
			}
			continue
		}

		src, err := sftpClient.Open(rpath)
		if err != nil {
			return fmt.Errorf("open remote %s: %w", rpath, err)
		}

		dst, err := os.Create(lpath)
		if err != nil {
			src.Close()
			return fmt.Errorf("create local %s: %w", lpath, err)
		}

		logVerbose(fmt.Sprintf("    Downloading %s …", entry.Name()))
		_, err = io.Copy(dst, src)
		src.Close()
		dst.Close()
		if err != nil {
			return fmt.Errorf("copy %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func sftpUploadDir(client *ssh.Client, localPath, remotePath string) error {
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("SFTP init: %w", err)
	}
	defer sftpClient.Close()
	return sftpUploadWalk(sftpClient, localPath, remotePath)
}

func sftpUploadWalk(sftpClient *sftp.Client, local, remote string) error {
	entries, err := os.ReadDir(local)
	if err != nil {
		return fmt.Errorf("read local dir %s: %w", local, err)
	}
	for _, entry := range entries {
		lpath := filepath.Join(local, entry.Name())
		rpath := remote + "/" + entry.Name()
		if entry.IsDir() {
			if err := sftpClient.MkdirAll(rpath); err != nil {
				return fmt.Errorf("mkdir remote %s: %w", rpath, err)
			}
			if err := sftpUploadWalk(sftpClient, lpath, rpath); err != nil {
				return err
			}
			continue
		}
		src, err := os.Open(lpath)
		if err != nil {
			return fmt.Errorf("open local %s: %w", lpath, err)
		}
		dst, err := sftpClient.Create(rpath)
		if err != nil {
			src.Close()
			return fmt.Errorf("create remote %s: %w", rpath, err)
		}
		logVerbose(fmt.Sprintf("    Uploading %s …", entry.Name()))
		_, err = io.Copy(dst, src)
		src.Close()
		dst.Close()
		if err != nil {
			return fmt.Errorf("upload %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// ── etcd backup ───────────────────────────────────────────────────────────────

func backupEtcd(ctx *BackupContext, host, sshKeyPath, sshUser string, dryRun bool) error {
	logSection("etcd backup")

	etcdDir := filepath.Join(ctx.Path, "etcd")
	// Remote temp dir matches what the Python version used: /home/<user>/okd-backup-temp
	remoteDir := fmt.Sprintf("/home/%s/okd-backup-temp", sshUser)
	backupScript := "/usr/local/bin/cluster-backup.sh"

	if dryRun {
		logVerbose(fmt.Sprintf("  [dry-run] SSH %s@%s", sshUser, host))
		logVerbose(fmt.Sprintf("  [dry-run] sudo %s %s && sudo chown -R %s:%s %s", backupScript, remoteDir, sshUser, sshUser, remoteDir))
		logVerbose(fmt.Sprintf("  [dry-run] SFTP download %s → %s", remoteDir, etcdDir))
		logVerbose(fmt.Sprintf("  [dry-run] sudo rm -rf %s", remoteDir))
		ctx.AddContent("etcd")
		logSuccess("etcd backup (dry-run)")
		return nil
	}

	if err := os.MkdirAll(etcdDir, 0755); err != nil {
		return err
	}

	logInfo(fmt.Sprintf("  Connecting to %s@%s …", sshUser, host))
	client, err := sshConnect(host, sshUser, sshKeyPath)
	if err != nil {
		return err
	}
	defer client.Close()

	// Run backup script and chown in one command (chown only runs on success).
	// cluster-backup.sh runs as root and writes files owned by root; chown makes
	// them readable by the SSH user so SFTP can download them.
	logInfo("  Running cluster-backup.sh …")
	cmd := fmt.Sprintf(
		"sudo %s %s && sudo chown -R %s:%s %s",
		backupScript, remoteDir, sshUser, sshUser, remoteDir,
	)
	stdout, stderr, err := sshRun(client, cmd)

	// Always log script output in verbose mode
	if stdout != "" {
		logVerbose("  cluster-backup.sh output:\n" + stdout)
	}
	if stderr != "" {
		logVerbose("  cluster-backup.sh stderr:\n" + stderr)
	}

	if err != nil {
		// Show the actual output even without --verbose so the user can diagnose
		if stdout != "" {
			logInfo("  Output:\n" + stdout)
		}
		if stderr != "" {
			logInfo("  Stderr:\n" + stderr)
		}
		// Prefer stderr detail, fall back to stdout, then the Go error string
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(stdout)
		}
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("cluster-backup.sh failed: %s", detail)
	}

	logSuccess("cluster-backup.sh completed")

	logInfo("  Downloading etcd snapshot via SFTP …")
	if err := sftpDownloadDir(client, remoteDir, etcdDir); err != nil {
		return fmt.Errorf("SFTP download: %w", err)
	}

	// Remove the temp directory from the control plane
	logInfo("  Removing temporary files from control plane …")
	cleanCmd := fmt.Sprintf("sudo rm -rf %s", remoteDir)
	_, _, _ = sshRun(client, cleanCmd)

	ctx.AddContent("etcd")
	logSuccess("etcd backup completed")
	return nil
}

// ── namespace backup ──────────────────────────────────────────────────────────

func backupNamespaces(ctx *BackupContext, namespaces []string, includeSecrets bool, dryRun bool) error {
	logSection("Namespace backup")

	if len(namespaces) == 0 {
		namespaces = listNamespaces()
		if len(namespaces) == 0 {
			return fmt.Errorf("no namespaces found")
		}
	}

	types := make([]string, 0, len(namespaceResourceTypes))
	for _, t := range namespaceResourceTypes {
		if !includeSecrets && t == "secrets" {
			continue
		}
		types = append(types, t)
	}

	nsDir := filepath.Join(ctx.Path, "namespaces")
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		return err
	}

	total := len(namespaces)
	width := len(fmt.Sprintf("%d", total))
	if width < 3 {
		width = 3
	}
	fmtStr := fmt.Sprintf("[%%0%dd/%%0%dd]", width, width)

	for i, ns := range namespaces {
		counter := fmt.Sprintf(fmtStr, i+1, total)
		if verboseMode {
			logInfo(fmt.Sprintf("  %s Namespace: %s", counter, ns))
		} else {
			fmt.Printf("\r  %s Namespace: %s\033[K", counter, ns)
		}

		if err := backupNamespace(nsDir, ns, types, dryRun); err != nil {
			logWarning(fmt.Sprintf("Namespace %s: %v", ns, err))
		}
	}

	if !verboseMode && total > 0 {
		fmt.Println()
	}

	ctx.AddContent("namespaces")
	logSuccess("Namespace backup completed")
	return nil
}

func backupNamespace(nsDir, ns string, types []string, dryRun bool) error {
	dir := filepath.Join(nsDir, ns)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	for _, t := range types {
		if dryRun {
			logVerbose(fmt.Sprintf("    [dry-run] oc get %s -n %s -o yaml", t, ns))
			continue
		}

		rc, out, _ := runOc([]string{"get", t, "-o", "yaml"}, ns)
		if rc != 0 || isEmptyResourceList(out) {
			continue
		}

		outFile := filepath.Join(dir, t+".yaml")
		if err := os.WriteFile(outFile, []byte(out+"\n"), 0644); err != nil {
			return err
		}
		logVerbose(fmt.Sprintf("    %s/%s", ns, t))
	}
	return nil
}

func isEmptyResourceList(yamlStr string) bool {
	return strings.Contains(yamlStr, "items: []") || strings.TrimSpace(yamlStr) == ""
}

// ── PVC backup ────────────────────────────────────────────────────────────────

func backupPVCs(ctx *BackupContext, namespaces []string, dryRun bool) error {
	logSection("PVC backup")

	if len(namespaces) == 0 {
		namespaces = listNamespaces()
	}

	pvcsDir := filepath.Join(ctx.Path, "pvcs")
	if err := os.MkdirAll(pvcsDir, 0755); err != nil {
		return err
	}

	anyFound := false
	for _, ns := range namespaces {
		pvcs := getPVCsInNamespace(ns)
		if len(pvcs) == 0 {
			continue
		}
		anyFound = true
		logInfo(fmt.Sprintf("  Namespace %s: %d PVC(s) found", ns, len(pvcs)))

		restricted := isRestrictedNamespace(ns, dryRun)
		if restricted {
			logVerbose(fmt.Sprintf("  %s: PodSecurity restricted — using hardened securityContext", ns))
		}

		for _, pvcName := range pvcs {
			backupSinglePVC(pvcsDir, ns, pvcName, restricted, dryRun)
		}
	}

	if !anyFound {
		logInfo("  No PVCs found in the specified namespaces")
	}

	ctx.AddContent("pvcs")
	logSuccess("PVC backup completed")
	return nil
}

func getPVCsInNamespace(ns string) []string {
	rc, out, _ := runOc([]string{
		"get", "persistentvolumeclaims",
		"-o", "jsonpath={.items[*].metadata.name}",
	}, ns)
	if rc != 0 || out == "" {
		return nil
	}
	return strings.Fields(out)
}

func backupSinglePVC(pvcsDir, ns, pvcName string, restricted, dryRun bool) {
	logInfo(fmt.Sprintf("    PVC: %s", pvcName))
	podName := backupPodPrefix + truncate(pvcName, 30)
	localDir := filepath.Join(pvcsDir, ns, pvcName)

	if dryRun {
		logVerbose(fmt.Sprintf("    [dry-run] create debug pod for %s (restricted=%v)", pvcName, restricted))
		logVerbose(fmt.Sprintf("    [dry-run] oc rsync %s:%s/ %s/", podName, mountPath, localDir))
		return
	}

	if err := os.MkdirAll(localDir, 0755); err != nil {
		logError(fmt.Sprintf("    ✖ %s: create dir: %v", pvcName, err))
		return
	}

	defer deletePod(podName, ns)

	if err := createPVCPod(podName, ns, pvcName, restricted, "backup"); err != nil {
		logError(fmt.Sprintf("    ✖ %s: create pod: %v", pvcName, err))
		return
	}
	if err := waitForPod(podName, ns, 300); err != nil {
		logError(fmt.Sprintf("    ✖ %s: %v", pvcName, err))
		return
	}
	if err := rsyncFromPod(podName, ns, localDir); err != nil {
		logError(fmt.Sprintf("    ✖ %s: rsync: %v", pvcName, err))
		return
	}
	logSuccess(fmt.Sprintf("    ✔ %s backed up", pvcName))
}

// ── cluster-config backup ─────────────────────────────────────────────────────

func backupClusterConfig(ctx *BackupContext, dryRun bool) error {
	logSection("Cluster configuration backup")

	ccDir := filepath.Join(ctx.Path, "cluster-config")
	if err := os.MkdirAll(ccDir, 0755); err != nil {
		return err
	}

	for _, res := range clusterResources {
		if dryRun {
			logVerbose(fmt.Sprintf("  [dry-run] oc get %s -o yaml → %s", res.Kind, res.File))
			continue
		}

		// Kind may include flags like "--all-namespaces"
		parts := strings.Fields(res.Kind)
		args := append([]string{"get"}, parts...)
		args = append(args, "-o", "yaml")

		rc, out, _ := runOc(args, "")
		if rc != 0 || isEmptyResourceList(out) {
			logVerbose(fmt.Sprintf("  %s: not found or empty, skipped", res.File))
			continue
		}

		outFile := filepath.Join(ccDir, res.File)
		if err := os.WriteFile(outFile, []byte(out+"\n"), 0644); err != nil {
			logWarning(fmt.Sprintf("  %s: write error: %v", res.File, err))
			continue
		}
		logVerbose(fmt.Sprintf("  ✔ %s", res.File))
	}

	ctx.AddContent("cluster-config")
	logSuccess("Cluster configuration backup completed")
	return nil
}

// ── Release tools download ────────────────────────────────────────────────────

// currentReleaseImage returns the image reference of the currently running
// cluster version (from clusterversion/version .status.desired.image).
func currentReleaseImage() (string, error) {
	rc, out, _ := runOc([]string{
		"get", "clusterversion", "version",
		"-o", "jsonpath={.status.desired.image}",
	}, "")
	if rc != 0 || out == "" {
		return "", fmt.Errorf("could not read cluster version — is the cluster reachable?")
	}
	return strings.TrimSpace(out), nil
}

// releaseNameFromInfo runs `oc adm release info <image>` and returns the
// human-readable name from the Name: field in the output.
func releaseNameFromInfo(image string) (string, error) {
	rc, out, _ := runOc([]string{"adm", "release", "info", image}, "")
	if rc != 0 {
		return "", fmt.Errorf("oc adm release info failed")
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Name:") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
			if name != "" {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("Name field not found in release info output")
}

// DownloadedTools holds metadata about one downloaded release tools set.
type DownloadedTools struct {
	Name       string
	Image      string
	Downloaded string
	Dir        string
}

// downloadTools extracts OKD release tools into storageRoot/tools/<name>/.
// The name is resolved via `oc adm release info`; falls back to the image tag.
// A .download-complete marker is written on success; its presence causes
// subsequent calls to skip the download unless force is true.
// A release-info.txt file is written with the image, name, and timestamp.
// Unless yes is true, a confirmation prompt is shown before downloading.
func downloadTools(storageRoot, releaseImage string, force, yes, dryRun bool) (string, error) {
	logSection("OKD release tools download")

	logInfo(fmt.Sprintf("  Release image: %s", releaseImage))
	logInfo("  Resolving release name …")

	name, err := releaseNameFromInfo(releaseImage)
	if err != nil {
		// fall back to the tag portion of the image reference
		if i := strings.LastIndex(releaseImage, ":"); i >= 0 && i < len(releaseImage)-1 {
			name = releaseImage[i+1:]
		} else {
			name = releaseImage
		}
		logVerbose(fmt.Sprintf("  release info unavailable, using: %s", name))
	} else {
		logInfo(fmt.Sprintf("  Release name:  %s", name))
	}

	toolsDir := filepath.Join(storageRoot, "tools", name)
	marker := filepath.Join(toolsDir, ".download-complete")

	logInfo(fmt.Sprintf("  Output:        %s", toolsDir))

	if !force {
		if _, err := os.Stat(marker); err == nil {
			logSuccess("Already downloaded — skipping (use --force to re-download)")
			return toolsDir, nil
		}
	}

	if dryRun {
		logInfo(fmt.Sprintf("  [dry-run] oc adm release extract --tools %s --to=%s", releaseImage, toolsDir))
		return toolsDir, nil
	}

	if !yes {
		fmt.Println()
		logInfo(fmt.Sprintf("  About to download release tools for: %s", name))
		logInfo(fmt.Sprintf("  Destination: %s", toolsDir))
		fmt.Print("\nDownload release tools? [y/N] ")
		var ans string
		fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			logInfo("Aborted.")
			return toolsDir, nil
		}
	}

	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		return "", err
	}

	if err := runOcStream([]string{
		"adm", "release", "extract",
		"--tools", releaseImage,
		fmt.Sprintf("--to=%s", toolsDir),
	}); err != nil {
		return "", fmt.Errorf("oc adm release extract: %w", err)
	}

	// Write human-readable info file alongside the tools
	infoContent := fmt.Sprintf("image=%s\nname=%s\ndownloaded=%s\n",
		releaseImage, name, time.Now().UTC().Format(time.RFC3339))
	_ = os.WriteFile(filepath.Join(toolsDir, "release-info.txt"), []byte(infoContent), 0644)

	_ = os.WriteFile(marker, []byte(releaseImage+"\n"), 0644)

	logSuccess("Release tools downloaded successfully")
	return toolsDir, nil
}

// listDownloadedTools returns metadata for every tools set found under
// storageRoot/tools/. Entries without release-info.txt are included with
// only the directory name populated.
func listDownloadedTools(storageRoot string) ([]DownloadedTools, error) {
	toolsRoot := filepath.Join(storageRoot, "tools")
	entries, err := os.ReadDir(toolsRoot)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var result []DownloadedTools
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dt := DownloadedTools{Name: entry.Name(), Dir: filepath.Join(toolsRoot, entry.Name())}

		data, err := os.ReadFile(filepath.Join(dt.Dir, "release-info.txt"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) != 2 {
					continue
				}
				switch parts[0] {
				case "image":
					dt.Image = parts[1]
				case "name":
					dt.Name = parts[1]
				case "downloaded":
					dt.Downloaded = parts[1]
				}
			}
		}
		result = append(result, dt)
	}
	return result, nil
}

// ── Pod helpers ───────────────────────────────────────────────────────────────

func createPVCPod(podName, namespace, pvcName string, restricted bool, mode string) error {
	manifest := buildPodManifest(podName, namespace, pvcName, restricted, mode)
	tmpFile, err := os.CreateTemp("", "okd-backup-pod-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(manifest); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	rc, _, stderr := runOc([]string{"apply", "-f", tmpFile.Name()}, "")
	if rc != 0 {
		return fmt.Errorf("create pod: %s", stderr)
	}
	return nil
}

func buildPodManifest(podName, namespace, pvcName string, restricted bool, mode string) string {
	appLabel := "okd-backup-pvc"
	containerName := "backup"
	if mode == "restore" {
		appLabel = "okd-restore-pvc"
		containerName = "restore"
	}

	podSecCtx := ""
	containerSecCtx := ""
	if restricted {
		podSecCtx = `
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault`
		containerSecCtx = `
      securityContext:
        runAsNonRoot: true
        allowPrivilegeEscalation: false
        seccompProfile:
          type: RuntimeDefault
        capabilities:
          drop: ["ALL"]`
	}

	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    app: %s
spec:
  restartPolicy: Never%s
  volumes:
  - name: pvc-data
    persistentVolumeClaim:
      claimName: %s
  containers:
  - name: %s
    image: %s
    command: ["/bin/sh", "-c", "sleep 3600"]
    volumeMounts:
    - name: pvc-data
      mountPath: %s%s
`, podName, namespace, appLabel, podSecCtx, pvcName, containerName, pvcImage, mountPath, containerSecCtx)
}

func waitForPod(podName, namespace string, timeout int) error {
	logVerbose(fmt.Sprintf("    Waiting for pod %s …", podName))
	rc, _, _ := runOc([]string{
		"wait", "pod/" + podName,
		"--for=condition=Ready",
		fmt.Sprintf("--timeout=%ds", timeout),
	}, namespace)
	if rc == 0 {
		return nil
	}

	// Get failure detail
	_, info, _ := runOc([]string{
		"get", "pod", podName,
		"-o", "jsonpath={.status.phase} {.status.containerStatuses[0].state.waiting.reason}",
	}, namespace)
	parts := strings.Fields(info)
	var detail string
	switch len(parts) {
	case 0:
		detail = fmt.Sprintf("not Ready within %ds", timeout)
	case 1:
		detail = parts[0]
	default:
		detail = parts[1]
	}
	return fmt.Errorf("pod %s failed to start: %s", podName, detail)
}

func deletePod(podName, namespace string) {
	runOc([]string{"delete", "pod", podName, "--ignore-not-found=true", "--wait=false"}, namespace)
}

func rsyncFromPod(podName, namespace, localDir string) error {
	rc, _, stderr := runOc([]string{
		"rsync", podName + ":" + mountPath + "/", localDir + "/", "--progress",
	}, namespace)
	if rc != 0 {
		return fmt.Errorf("rsync failed: %s", stderr)
	}
	return nil
}

func rsyncToPod(podName, namespace, localDir string) error {
	rc, _, stderr := runOc([]string{
		"rsync", localDir + "/", podName + ":" + mountPath + "/", "--progress",
	}, namespace)
	if rc != 0 {
		return fmt.Errorf("rsync failed: %s", stderr)
	}
	return nil
}

func isRestrictedNamespace(ns string, dryRun bool) bool {
	if dryRun {
		return false
	}
	_, out, _ := runOc([]string{
		"get", "namespace", ns,
		"-o", "jsonpath={.metadata.labels.pod-security\\.kubernetes\\.io/enforce}" +
			" {.metadata.annotations.openshift\\.io/sa\\.scc\\.supplemental-groups}",
	}, "")
	return strings.Contains(out, "restricted")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
