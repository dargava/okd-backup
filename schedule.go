package main

import "fmt"

// Preset holds schedule expressions for systemd and cron.
type Preset struct {
	Label  string
	Systemd string
	Cron   string
}

var presets = map[string]Preset{
	"hourly":  {Label: "Every hour", Systemd: "hourly", Cron: "0 * * * *"},
	"daily":   {Label: "Daily at 02:00", Systemd: "daily", Cron: "0 2 * * *"},
	"weekly":  {Label: "Weekly on Sunday at 02:00", Systemd: "weekly", Cron: "0 2 * * 0"},
	"monthly": {Label: "Monthly on the 1st at 02:00", Systemd: "monthly", Cron: "0 2 1 * *"},
}

// renderService produces a systemd service unit file for okd-backup.
func renderService(backupArgs, user string) string {
	return fmt.Sprintf(`[Unit]
Description=OKD cluster backup
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
User=%s
ExecStart=/usr/local/bin/okd-backup %s
StandardOutput=journal
StandardError=journal
`, user, backupArgs)
}

// renderTimer produces a systemd timer unit file.
func renderTimer(onCalendar string) string {
	return fmt.Sprintf(`[Unit]
Description=OKD cluster backup timer

[Timer]
OnCalendar=%s
Persistent=true

[Install]
WantedBy=timers.target
`, onCalendar)
}

// renderCrontab produces a crontab entry line.
func renderCrontab(cronExpr, backupArgs, logFile string) string {
	cmd := fmt.Sprintf("/usr/local/bin/okd-backup %s", backupArgs)
	if logFile != "" {
		cmd = fmt.Sprintf("%s >> %s 2>&1", cmd, logFile)
	}
	return fmt.Sprintf("%s %s", cronExpr, cmd)
}
