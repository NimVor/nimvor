package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// CronService manages a website's cron jobs (CronService.php). The panel
// stores the jobs; after every change the complete schedule of the website is
// sent to the agent (cron.apply), which owns one /etc/cron.d file per
// website. Jobs always run as the website's system account. When the agent
// apply fails, the stored jobs are restored as they were.
type CronService struct {
	jobs   *CronRepository
	agent  AgentCaller
	limits *PlanLimits
	audit  Auditor
}

// NewCronService builds the service.
func NewCronService(db *sql.DB, agent AgentCaller, limits *PlanLimits, audit Auditor) *CronService {
	return &CronService{jobs: NewCronRepository(db), agent: agent, limits: limits, audit: audit}
}

// PresetSchedules are the common schedules offered in the form, in display
// order (CronService::PRESETS).
var PresetSchedules = [][2]string{
	{"* * * * *", "Every minute"},
	{"*/5 * * * *", "Every 5 minutes"},
	{"*/15 * * * *", "Every 15 minutes"},
	{"0 * * * *", "Every hour"},
	{"0 3 * * *", "Every day at 03:00"},
	{"0 3 * * 0", "Every Sunday at 03:00"},
	{"0 3 1 * *", "First day of the month at 03:00"},
}

var presetLabels = map[string]string{
	"* * * * *":     "Every minute",
	"*/5 * * * *":   "Every 5 minutes",
	"*/15 * * * *":  "Every 15 minutes",
	"0 * * * *":     "Every hour",
	"0 3 * * *":     "Every day at 03:00",
	"0 3 * * 0":     "Every Sunday at 03:00",
	"0 3 1 * *":     "First day of the month at 03:00",
}

// PresetLabel is the human name of a preset schedule ("" for a custom one).
func PresetLabel(expr string) string { return presetLabels[expr] }

// CronJobInput is the validated, normalised cron form input
// (CronService::validate's return).
type CronJobInput struct {
	Schedule    string
	Command     string
	Description string
	Enabled     bool
	EmailOutput bool
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

func (s *CronService) recordAudit(ctx context.Context, action string, site *Website, result string, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, "website", fmt.Sprintf("%d", site.ID), result, details)
	}
}

// cronFieldRe is one field of a five-field schedule (CronService::
// validSchedule's per-field regex).
var cronFieldRe = regexp.MustCompile(`^(\*|[0-9]{1,2}(-[0-9]{1,2})?)(/[0-9]{1,2})?(,(\*|[0-9]{1,2}(-[0-9]{1,2})?)(/[0-9]{1,2})?)*$`)

var cronNumberRe = regexp.MustCompile(`[0-9]+`)

// ValidSchedule reports whether the schedule is one the agent accepts: a
// macro or five space-separated fields with numbers in range (names like mon
// or jan are not supported). Mirrors CronService::validSchedule.
func ValidSchedule(s string) bool {
	switch s {
	case "@hourly", "@daily", "@midnight", "@weekly", "@monthly", "@yearly", "@annually", "@reboot":
		return true
	}
	f := strings.Fields(s)
	if len(f) != 5 {
		return false
	}
	limits := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i, field := range f {
		if !cronFieldRe.MatchString(field) {
			return false
		}
		for _, item := range strings.Split(field, ",") {
			base := strings.Split(item, "/")[0]
			for _, n := range cronNumberRe.FindAllString(base, -1) {
				v := 0
				fmt.Sscanf(n, "%d", &v)
				if v < limits[i][0] || v > limits[i][1] {
					return false
				}
			}
		}
	}
	return true
}

// cronControlRe rejects control characters in a command, except the tab
// (CronService::validate's /[\x00-\x08\x0a-\x1f\x7f]/).
var cronControlRe = regexp.MustCompile(`[\x00-\x08\x0a-\x1f\x7f]`)

// Validate checks the cron form input; returns the normalised values
// (CronService::validate). schedule "custom" (or empty) means the custom
// field is used.
func (s *CronService) Validate(input map[string]string) (*CronJobInput, error) {
	ve := &ValidationError{}
	schedule := strings.Join(strings.Fields(input["schedule"]), " ")
	if schedule == "custom" || schedule == "" {
		schedule = strings.Join(strings.Fields(input["schedule_custom"]), " ")
	}
	if !ValidSchedule(schedule) {
		ve.Add("schedule", "Use five fields (minute hour day month weekday), e.g. */5 * * * *, or @hourly/@daily/@weekly/@monthly. Names like mon or jan are not supported.")
	}
	command := strings.TrimSpace(input["command"])
	if command == "" || len(command) > 2000 || cronControlRe.MatchString(command) {
		ve.Add("command", "Enter the command on a single line (up to 2000 characters).")
	}
	description := strings.TrimSpace(input["description"])
	if len([]rune(description)) > 200 {
		ve.Add("description", "Keep the description under 200 characters.")
	}
	if ve.Any() {
		return nil, ve
	}
	return &CronJobInput{
		Schedule:    schedule,
		Command:     command,
		Description: description,
		Enabled:     input["enabled"] == "1",
		EmailOutput: input["email_output"] == "1",
	}, nil
}

// Save creates (id 0) or updates a job, then pushes the schedule to the
// server (CronService::save). accountEmail is the saving user's email: it
// receives the output when mailing is turned on.
func (s *CronService) Save(ctx context.Context, site *Website, id int64, input map[string]string, accountEmail string) error {
	v, err := s.Validate(input)
	if err != nil {
		return err
	}
	email := ""
	var current *cronJobRow
	if id != 0 {
		current, err = s.jobs.FindFull(ctx, site.ID, id)
		if err != nil {
			return err
		}
	}
	if v.EmailOutput {
		if current != nil && Mailable(current.Email) {
			email = current.Email // unchanged: keep who receives it
		} else if !Mailable(accountEmail) {
			ve := &ValidationError{}
			ve.Add("email_output", "Your account has no usable email address for the output.")
			return ve
		} else {
			email = accountEmail
		}
	}
	now := cronNow()
	before, err := s.jobs.AllFull(ctx, site.ID)
	if err != nil {
		return err
	}
	action := "cron.update"
	if id == 0 {
		if len(before) >= 200 {
			ve := &ValidationError{}
			ve.Add("command", "A website can have at most 200 cron jobs.")
			return ve
		}
		if err := s.limits.AssertCanCreate(ctx, "cron_jobs", ownerOrNil(site.OwnerID), 1); err != nil {
			return err
		}
		if _, err := s.jobs.Insert(ctx, site, v.Schedule, v.Command, v.Description, email, now, v.Enabled); err != nil {
			return err
		}
		action = "cron.create"
	} else {
		if err := s.jobs.Update(ctx, site.ID, id, v.Schedule, v.Command, v.Description, email, now, v.Enabled); err != nil {
			return err
		}
	}
	details := map[string]any{"schedule": v.Schedule, "command": v.Command, "enabled": v.Enabled, "email_output": v.EmailOutput}
	return s.applyOrRevert(ctx, site, before, action, details)
}

// SetEnabled pauses or resumes one job (CronService::setEnabled).
func (s *CronService) SetEnabled(ctx context.Context, site *Website, id int64, enabled bool) error {
	before, err := s.jobs.AllFull(ctx, site.ID)
	if err != nil {
		return err
	}
	if err := s.jobs.SetEnabled(ctx, site.ID, id, enabled, cronNow()); err != nil {
		return err
	}
	action := "cron.disable"
	if enabled {
		action = "cron.enable"
	}
	return s.applyOrRevert(ctx, site, before, action, map[string]any{"id": id})
}

// Delete removes one job (CronService::delete).
func (s *CronService) Delete(ctx context.Context, site *Website, id int64) error {
	before, err := s.jobs.AllFull(ctx, site.ID)
	if err != nil {
		return err
	}
	if err := s.jobs.DeleteJob(ctx, site.ID, id); err != nil {
		return err
	}
	return s.applyOrRevert(ctx, site, before, "cron.delete", map[string]any{"id": id})
}

// Run runs a job now on the server; returns the agent job id
// (CronService::run).
func (s *CronService) Run(ctx context.Context, site *Website, job *CronJob) (string, error) {
	if site.AccountSuspended {
		return "", &UserError{What: "The account is suspended; its cron jobs do not run."}
	}
	res, err := s.agent.Call(ctx, "cron.run", map[string]any{
		"domain":  site.Domain,
		"user":    site.SystemUser,
		"root":    site.Root,
		"id":      job.ID,
		"command": job.Command,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	if err := s.jobs.SetLastRun(ctx, job.ID, out.JobID); err != nil {
		return "", err
	}
	s.recordAudit(ctx, "cron.run", site, "success", map[string]any{"domain": site.Domain, "job": job.ID})
	return out.JobID, nil
}

// CronRun is one framed run of a job's log (CronService::parseRuns' entry).
type CronRun struct {
	Start  *string
	End    *string
	Exit   *int
	Manual bool
	Output string
}

// CronLogResult is the log page's data (CronService::runs' return).
type CronLogResult struct {
	Runs      []CronRun
	Truncated bool
	Size      int
}

var cronRunStartRe = regexp.MustCompile(`^### whacp-run start (\S+)( manual)?$`)
var cronRunEndRe = regexp.MustCompile(`^### whacp-run end (\S+) exit=(\d+)$`)

// ParseRuns splits the end of a job's log into runs, newest first. Each run
// is framed by "### whacp-run start …" and "### whacp-run end … exit=N";
// output from before the framing is kept as one earlier entry. Mirrors
// CronService::parseRuns.
func ParseRuns(log string, max int) []CronRun {
	if max <= 0 {
		max = 200
	}
	var runs []CronRun
	var cur *CronRun
	loose := ""
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if m := cronRunStartRe.FindStringSubmatch(line); m != nil {
			if cur != nil {
				runs = append(runs, *cur)
			}
			start := m[1]
			manual := m[2] == " manual"
			cur = &CronRun{Start: &start, Manual: manual, Output: ""}
		} else if m := cronRunEndRe.FindStringSubmatch(line); m != nil && cur != nil {
			end := m[1]
			exit := 0
			fmt.Sscanf(m[2], "%d", &exit)
			cur.End = &end
			cur.Exit = &exit
			runs = append(runs, *cur)
			cur = nil
		} else if cur != nil {
			cur.Output += line + "\n"
		} else if line != "" {
			loose += line + "\n"
		}
	}
	if cur != nil {
		runs = append(runs, *cur)
	}
	if loose != "" {
		runs = append([]CronRun{{Output: strings.TrimRight(loose, "\n")}}, runs...)
	}
	for i := range runs {
		runs[i].Output = strings.TrimRight(runs[i].Output, "\n")
	}
	// Newest first.
	out := make([]CronRun, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- {
		out = append(out, runs[i])
		if len(out) == max {
			break
		}
	}
	return out
}

// Runs reads the end of a job's log from the agent (CronService::runs).
func (s *CronService) Runs(ctx context.Context, site *Website, jobID int64) (CronLogResult, error) {
	res, err := s.agent.Call(ctx, "cron.log", map[string]any{
		"domain": site.Domain,
		"root":   site.Root,
		"id":     jobID,
	})
	if err != nil {
		return CronLogResult{}, err
	}
	var raw struct {
		Content   string `json:"content"`
		Size      int    `json:"size"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(res, &raw); err != nil {
		return CronLogResult{}, err
	}
	return CronLogResult{Runs: ParseRuns(raw.Content, 200), Truncated: raw.Truncated, Size: raw.Size}, nil
}

// applyOrRevert pushes the stored schedule to the server; on failure the
// website's jobs are restored as they were (CronService::applyOrRevert).
func (s *CronService) applyOrRevert(ctx context.Context, site *Website, before []cronJobRow, action string, details map[string]any) error {
	err := s.Apply(ctx, site)
	if err != nil {
		if rerr := s.jobs.Restore(ctx, site.ID, before); rerr != nil {
			return fmt.Errorf("the cron schedule was not saved (%v) and the stored jobs could not be restored (%v)", err, rerr)
		}
		d := map[string]any{"domain": site.Domain, "error": err.Error()}
		for k, v := range details {
			d[k] = v
		}
		s.recordAudit(ctx, action, site, "failure", d)
		return err
	}
	d := map[string]any{"domain": site.Domain}
	for k, v := range details {
		d[k] = v
	}
	s.recordAudit(ctx, action, site, "success", d)
	return nil
}

// ownerOrNil is nil for a website without an owner (staff-owned, unlimited).
func ownerOrNil(o sql.NullInt64) *int64 {
	if !o.Valid {
		return nil
	}
	v := o.Int64
	return &v
}

// Find returns one of the website's jobs by id (nil = not one of this
// website's jobs) (CronService::find).
func (s *CronService) Find(ctx context.Context, site *Website, id int64) (*CronJob, error) {
	rows, err := s.jobs.Jobs(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].ID == id {
			return &rows[i], nil
		}
	}
	return nil, nil
}
