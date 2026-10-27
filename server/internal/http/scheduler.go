package http

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/notify"
)

// The scheduler is one pass of background work, run every few minutes by
// whacp-scheduler.timer as `whacp schedule:run` (bin/whacp schedule:run in the
// PHP panel). Each pass reports what it did, one line each. Passes ported so
// far are listed in schedulerPasses; the others of the PHP scheduler
// (monitoring samples and alerts, scheduled backups, antivirus, the nightly
// housekeeping) are not part of this panel yet.

// schedulerUser acts for work no administrator started.
var schedulerUser = &identity.User{ID: 0, Username: "scheduler", RoleScope: "all", RoleName: "admin"}

type schedulerPass struct {
	name string
	run  func(ctx context.Context, s *Server, r *http.Request) []string
}

var schedulerPasses = []schedulerPass{
	{"backups", func(ctx context.Context, s *Server, r *http.Request) []string {
		return s.backupScheduleRun(ctx, r)
	}},
	{"auto SSL", func(ctx context.Context, s *Server, r *http.Request) []string {
		return s.autoSslService(r, schedulerUser).Run(ctx)
	}},
}

// autoSslService builds the AutoSSL service bound to the actor.
func (s *Server) autoSslService(r *http.Request, user *identity.User) *hosting.AutoSslService {
	return hosting.NewAutoSslService(s.db, s.currentServerID(r), s.settings, s.agent,
		s.sslService(r, user), hosting.NewWebsiteRepository(s.db), s.siteDnsService(r, user),
		hosting.NewSiteOverview(s.db, s.agent, s.settings, s.databaseService(r, user)))
}

// RunScheduler runs one scheduler pass and records when it ran. Output lines
// are prefixed with the pass; a pass that panics is reported and the rest run.
func (s *Server) RunScheduler(ctx context.Context) []string {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return []string{"scheduler: " + err.Error()}
	}
	var out []string
	for _, p := range schedulerPasses {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					s.log.Error("scheduler pass panicked", "pass", p.name, "panic", rec)
					out = append(out, p.name+": failed unexpectedly (see the panel log)")
				}
			}()
			out = append(out, p.run(ctx, s, r)...)
		}()
	}
	// Doctor and the AutoSSL page read when it last ran.
	_ = s.settings.Set(ctx, hosting.SchedulerRunKey, time.Now().UTC().Format(time.RFC3339), 0)
	return out
}

// backupScheduleRun starts due scheduled backups and applies retention.
func (s *Server) backupScheduleRun(ctx context.Context, r *http.Request) []string {
	notes := notify.NewRepository(s.db)
	return s.backupSched(r, schedulerUser).Run(ctx, hosting.ScheduleRunner{
		Backups: s.backupsAdminService(r, schedulerUser),
		Sites:   hosting.NewWebsiteRepository(s.db),
		Jobs:    hosting.NewJobTracker(s.db),
		Notify: func(ctx context.Context, websiteID sql.NullInt64, severity, title, body, link, key string) {
			_, _ = notes.Store(ctx, websiteID, "job", severity, title, body, link, key)
		},
	})
}
