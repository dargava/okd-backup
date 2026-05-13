package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ocBin is the oc or kubectl binary in PATH.
var ocBin string

func init() {
	if _, err := exec.LookPath("oc"); err == nil {
		ocBin = "oc"
	} else if _, err := exec.LookPath("kubectl"); err == nil {
		ocBin = "kubectl"
	}
}

// runOc runs an oc/kubectl command and returns (exitCode, stdout, stderr).
// namespace is prepended as -n <ns> when non-empty.
func runOc(args []string, namespace string) (int, string, string) {
	if ocBin == "" {
		return 1, "", "neither oc nor kubectl found in PATH"
	}

	cmdArgs := make([]string, 0, len(args)+2)
	if namespace != "" {
		cmdArgs = append(cmdArgs, "-n", namespace)
	}
	cmdArgs = append(cmdArgs, args...)

	logVerbose(fmt.Sprintf("  $ %s %s", ocBin, strings.Join(cmdArgs, " ")))

	cmd := exec.Command(ocBin, cmdArgs...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	rc := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		} else {
			rc = 1
		}
	}

	if verboseMode && stderr.Len() > 0 {
		logVerbose(stderr.String())
	}

	return rc, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String())
}

// runOcStream runs an oc/kubectl command and streams stdout/stderr directly to
// the terminal. Used for long-running commands like release extraction.
func runOcStream(args []string) error {
	if ocBin == "" {
		return fmt.Errorf("neither oc nor kubectl found in PATH")
	}
	logVerbose(fmt.Sprintf("  $ %s %s", ocBin, strings.Join(args, " ")))
	cmd := exec.Command(ocBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// applyFile runs `oc apply -f <file>` and returns any error.
func applyFile(file string, dryRun bool) error {
	args := []string{"apply", "-f", file}
	if dryRun {
		args = append(args, "--dry-run=client")
	}
	rc, _, stderr := runOc(args, "")
	if rc != 0 {
		return fmt.Errorf("%s", stderr)
	}
	return nil
}

// clusterVersion returns the current cluster version string.
func clusterVersion(dryRun bool) string {
	if dryRun {
		return "dry-run"
	}
	_, out, _ := runOc([]string{"get", "clusterversion", "version",
		"-o", "jsonpath={.status.desired.version}"}, "")
	if out == "" {
		return "unknown"
	}
	return out
}

// requireClusterAccess exits with an error message if the user is not logged in
// or does not have cluster-admin rights.
func requireClusterAccess() {
	if ocBin == "" {
		logWarning("Neither oc nor kubectl found in PATH — cannot connect to the cluster")
		os.Exit(1)
	}

	rc, user, _ := runOc([]string{"whoami"}, "")
	if rc != 0 || user == "" {
		logWarning("Not logged in to the cluster — run: oc login <cluster-url>")
		os.Exit(1)
	}

	rcAdm, outAdm, _ := runOc([]string{"auth", "can-i", "*", "*", "--all-namespaces"}, "")
	if rcAdm != 0 || strings.TrimSpace(outAdm) != "yes" {
		logWarning(fmt.Sprintf(
			"%s does not have cluster-admin rights — "+
				"etcd, PVC, and cluster-config operations require cluster-admin",
			user,
		))
		os.Exit(1)
	}
}
