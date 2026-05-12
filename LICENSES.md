# Third-party licenses

okd-backup depends on the following open-source packages.
All licenses are compatible with use in private and commercial environments.

---

## Go module dependencies

### github.com/spf13/cobra
- **License:** Apache-2.0
- **Author:** Steve Francia
- **Homepage:** https://github.com/spf13/cobra
- **Use:** CLI framework — commands, flags, help text

### github.com/spf13/pflag
- **License:** BSD-3-Clause
- **Author:** Steve Francia
- **Homepage:** https://github.com/spf13/pflag
- **Use:** POSIX-compliant flag parsing (pulled in by cobra)

### golang.org/x/crypto
- **License:** BSD-3-Clause
- **Author:** The Go Authors
- **Homepage:** https://pkg.go.dev/golang.org/x/crypto
- **Use:** SSH client connection to control plane for etcd backup

### golang.org/x/sys
- **License:** BSD-3-Clause
- **Author:** The Go Authors
- **Homepage:** https://pkg.go.dev/golang.org/x/sys
- **Use:** Low-level system calls (pulled in by golang.org/x/crypto)

### github.com/pkg/sftp
- **License:** BSD-2-Clause
- **Author:** Dave Cheney
- **Homepage:** https://github.com/pkg/sftp
- **Use:** SFTP file download of etcd snapshots from control plane

### github.com/kr/fs
- **License:** BSD-3-Clause
- **Author:** Keith Rarick
- **Homepage:** https://github.com/kr/fs
- **Use:** Filesystem walker (pulled in by github.com/pkg/sftp)

### github.com/inconshreveable/mousetrap
- **License:** Apache-2.0
- **Author:** Alan Shreve
- **Homepage:** https://github.com/inconshreveable/mousetrap
- **Use:** Windows console detection (pulled in by cobra)

### gopkg.in/yaml.v3
- **License:** MIT
- **Author:** Canonical Ltd. / Kirill Simonov
- **Homepage:** https://github.com/go-yaml/yaml
- **Use:** YAML config file parsing and writing

---

## External tools

These tools are called by okd-backup as external processes at runtime.

### oc (OpenShift CLI)
- **License:** Apache-2.0
- **Author:** Red Hat
- **Homepage:** https://github.com/openshift/oc
- **Use:** All cluster API interactions — get, apply, rsync, wait

### kubectl (fallback)
- **License:** Apache-2.0
- **Author:** The Kubernetes Authors
- **Homepage:** https://kubernetes.io/docs/reference/kubectl/
- **Use:** Fallback when `oc` is not available

### cluster-backup.sh
- **License:** Apache-2.0
- **Author:** Red Hat / OKD community
- **Homepage:** https://github.com/openshift/cluster-etcd-operator
- **Use:** Creates etcd snapshots on the control plane node — invoked via SSH

### rsync
- **License:** GPL-3.0
- **Author:** Andrew Tridgell, Wayne Davison
- **Homepage:** https://rsync.samba.org/
- **Use:** Copying PVC data between cluster pods and local storage via `oc rsync`

---

## License summary

| Package | License | Type |
|---|---|---|
| github.com/spf13/cobra | Apache-2.0 | Permissive |
| github.com/spf13/pflag | BSD-3-Clause | Permissive |
| golang.org/x/crypto | BSD-3-Clause | Permissive |
| golang.org/x/sys | BSD-3-Clause | Permissive |
| github.com/pkg/sftp | BSD-2-Clause | Permissive |
| github.com/kr/fs | BSD-3-Clause | Permissive |
| github.com/inconshreveable/mousetrap | Apache-2.0 | Permissive |
| gopkg.in/yaml.v3 | MIT | Permissive |
| oc | Apache-2.0 | Permissive |
| kubectl | Apache-2.0 | Permissive |
| cluster-backup.sh | Apache-2.0 | Permissive |
| rsync | GPL-3.0 | Copyleft (runtime tool only) |

> **rsync note:** rsync is called as an external process, not linked or bundled.
> Its GPL-3.0 license does not extend to okd-backup's own code.
