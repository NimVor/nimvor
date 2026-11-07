package migrate

import (
	"context"
	"strings"
)

// Panel-owned crontab lines (the panel's own housekeeping) are not the customer's jobs and are
// never listed for migration.
var panelCronMarkers = []string{"/usr/local/cpanel", "/usr/local/vesta", "/usr/local/hestia", "/usr/local/directadmin", "/usr/local/cwp", "/usr/local/CyberCP", "/opt/psa", "/opt/cpanel/ea-", "/scripts/", "nimvor"}

// readCrontab lists the jobs in a system user's crontab (Red Hat and Debian spool locations). A user
// without a crontab, or a name that is not a plain login, gives none.
func readCrontab(ctx context.Context, t Transport, user string) []CronJob {
	if !pleskLogin.MatchString(user) || user == "root" {
		return nil
	}
	out, err := t.Exec(ctx, "sh -c "+Shq("cat /var/spool/cron/"+user+" /var/spool/cron/crontabs/"+user+" 2>/dev/null; true"), nil)
	if err != nil {
		return nil
	}
	var jobs []CronJob
	seen := map[CronJob]bool{}
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		j, ok := parseCron(l)
		if !ok || seen[j] || len(j.Command) > 2000 {
			continue
		}
		skip := false
		for _, m := range panelCronMarkers {
			if strings.Contains(j.Command, m) {
				skip = true
			}
		}
		if !skip {
			seen[j] = true
			jobs = append(jobs, j)
		}
	}
	return jobs
}

// addCrontabs fills Cron for accounts that have none yet, from the account's system user.
func addCrontabs(ctx context.Context, t Transport, accts []*Account) {
	for _, a := range accts {
		if len(a.Cron) == 0 {
			a.Cron = append(a.Cron, readCrontab(ctx, t, a.ID)...)
		}
	}
}
