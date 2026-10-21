package hosting

import (
	"context"
	"database/sql"
	"regexp"
)

// CronService pushes a website's stored cron schedule to the server
// (CronService::apply). The jobs themselves are managed through the website's
// page; this is the server-side sync used when the website changes account
// (conversion) or is deleted.
type CronService struct {
	jobs  *CronRepository
	agent AgentCaller
}

// NewCronService builds the service.
func NewCronService(db *sql.DB, agent AgentCaller) *CronService {
	return &CronService{jobs: NewCronRepository(db), agent: agent}
}

// mailableRe matches CronService::$mailable's regex: a recipient address the
// agent's mailer will accept.
var mailableRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}@[A-Za-z0-9-]{1,63}(\.[A-Za-z0-9-]{1,63})+$`)

// Mailable reports whether the address is a valid mail recipient
// (CronService::mailable).
func Mailable(email string) bool {
	return len(email) <= 254 && mailableRe.MatchString(email)
}

// agentCronJob is one entry of the `cron.apply` payload.
type agentCronJob struct {
	ID       int64  `json:"id"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Enabled  bool   `json:"enabled"`
	Email    string `json:"email"`
}

// Apply pushes the stored schedule to the server (CronService::apply). A
// suspended account's jobs do not run; their setting is kept.
func (s *CronService) Apply(ctx context.Context, site *Website) error {
	rows, err := s.jobs.Jobs(ctx, site.ID)
	if err != nil {
		return err
	}
	out := make([]agentCronJob, 0, len(rows))
	for _, j := range rows {
		email := j.Email
		if !Mailable(email) {
			email = ""
		}
		out = append(out, agentCronJob{
			ID:       j.ID,
			Schedule: j.Schedule,
			Command:  j.Command,
			Enabled:  j.Enabled && !site.AccountSuspended,
			Email:    email,
		})
	}
	_, err = s.agent.Call(ctx, "cron.apply", map[string]any{
		"domain": site.Domain,
		"user":   site.SystemUser,
		"root":   site.Root,
		"jobs":   out,
	})
	return err
}

// RemoveFromServer deletes the website's cron file from the server
// (CronService::removeFromServer).
func (s *CronService) RemoveFromServer(ctx context.Context, site *Website) error {
	_, err := s.agent.Call(ctx, "cron.apply", map[string]any{
		"domain": site.Domain,
		"user":   site.SystemUser,
		"root":   site.Root,
		"jobs":   []agentCronJob{},
	})
	return err
}
