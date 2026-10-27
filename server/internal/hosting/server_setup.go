package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// ServerSetup sets a server up for a profile (Full, Web only, Minimal): the
// components' packages, PHP, email, DNS, FTP, caches, firewall and its
// protections, fail2ban, hardening, automatic security updates, hosting plans
// with feature lists and weekly backups of every website (ServerSetupService).
//
// Every step is one of the panel's existing actions and is idempotent: what is
// already in place is left alone (an existing plan, schedule, mail or FTP setup
// is never changed), so the setup can run again at any time. A failed step is
// reported and the next ones still run. Nothing is removed.
//
// Firewall changes are guarded by the agent (undone after 2 minutes unless
// kept). The setup keeps them itself, but only after reading the firewall again
// and finding every critical port (SSH, the panel, the agent's listener) open;
// otherwise it lets the change be undone.
type ServerSetup struct {
	agent    AgentCaller
	jobs     *JobTracker
	cfg      *settings.Repository
	db       *sql.DB
	serverID int64
	plans    *PlanRepository
	features *authz.FeatureService
	sched    *BackupScheduler
	audit    Auditor

	// ActorID is the administrator running the setup (the wizard); 0 from the
	// command line.
	ActorID int64
	// Poll is the pause between two reads of a job's log.
	Poll time.Duration

	out       func(string)
	async     bool
	resumed   bool
	attempted []string
	jobMsg    string
}

// NewServerSetup builds the service.
func NewServerSetup(db *sql.DB, serverID int64, agent AgentCaller, cfg *settings.Repository, plans *PlanRepository,
	features *authz.FeatureService, sched *BackupScheduler, audit Auditor) *ServerSetup {
	return &ServerSetup{db: db, serverID: serverID, agent: agent, jobs: NewJobTracker(db), cfg: cfg, plans: plans, features: features,
		sched: sched, audit: audit, Poll: time.Second, out: func(string) {}}
}

// SetupProfiles are the components each profile installs and sets up
// (Server → Components ids).
var SetupProfiles = map[string][]string{
	"minimal": {"web", "php", "database", "firewall", "fail2ban", "certbot"},
	"web":     {"web", "php", "database", "firewall", "fail2ban", "certbot", "redis", "memcached", "ftp"},
	"full":    {"web", "php", "database", "firewall", "fail2ban", "certbot", "redis", "memcached", "ftp", "mail", "dns", "clamav"},
}

// SetupProfileOrder lists the profiles as the wizard offers them.
var SetupProfileOrder = []string{"full", "web", "minimal"}

// SetupProfileNames are the profiles' names.
var SetupProfileNames = map[string]string{"full": "Full hosting server", "web": "Web hosting only", "minimal": "Minimal"}

// SetupPHPDefault are the PHP versions installed by default.
var SetupPHPDefault = []string{"8.3", "8.4"}

// SetupPHPChoices are the PHP versions the wizard offers.
var SetupPHPChoices = []string{"8.0", "8.1", "8.2", "8.3", "8.4"}

type setupStepDef struct{ ID, Label string }

// SetupSteps are all steps in order.
var SetupSteps = []setupStepDef{
	{"packages", "Install the components"}, {"php_repository", "PHP repository"}, {"php", "PHP versions and extensions"},
	{"mail", "Email"}, {"dns", "DNS server"}, {"ftp", "FTP server"}, {"caches", "Redis memory limit"},
	{"services", "Start the services"}, {"firewall", "Firewall"}, {"ports", "Open the components' ports"},
	{"protection", "Firewall protections"}, {"fail2ban", "Fail2ban"}, {"hardening", "Server hardening"},
	{"auto_updates", "Automatic security updates"}, {"plans", "Hosting plans and feature lists"},
	{"backups", "Weekly backups"}, {"profile", "Server profile"},
}

// SetupStepLabel is a step's label ("" for an unknown id).
func SetupStepLabel(id string) string {
	for _, s := range SetupSteps {
		if s.ID == id {
			return s.Label
		}
	}
	return ""
}

// stepProfiles are the steps that do not apply to every profile.
var stepProfiles = map[string][]string{
	"mail": {"full"}, "dns": {"full"}, "ftp": {"web", "full"}, "caches": {"web", "full"}, "protection": {"web", "full"},
}

// StepsFor are the steps a profile runs, in order.
func StepsFor(profile string) []string {
	var out []string
	for _, s := range SetupSteps {
		if only, ok := stepProfiles[s.ID]; ok && !hasStr(only, profile) {
			continue
		}
		out = append(out, s.ID)
	}
	return out
}

func hasStr(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

var setupFeatureLists = []struct {
	Name, Description string
	Features          []string
}{
	{"Hosting", "Websites without shell access.", []string{"php", "mysql", "email", "dns", "file_manager", "ssl", "subdomains", "ftp", "cron", "backups", "apps", "git", "workers", "redis", "logs", "stats", "malware", "wordpress"}},
	{"Hosting with SSH", "Every feature, the SSH shell included.", []string{"php", "mysql", "email", "dns", "file_manager", "ssl", "subdomains", "ftp", "cron", "backups", "apps", "git", "workers", "redis", "logs", "stats", "malware", "wordpress", "ssh"}},
	{"Email only", "Email, DNS and certificates for a domain; no website tools.", []string{"email", "dns", "ssl", "backups"}},
}

// setupPlans: name => description, feature list, limits (0 = unlimited).
var setupPlans = []struct {
	Name, Description, List string
	Limits                  map[string]int
}{
	{"Starter", "One website for a small business or a blog.", "Hosting", map[string]int{
		"disk_quota_mb": 5120, "bandwidth_mb": 51200, "max_websites": 1, "max_subdomains": 5, "max_parked_domains": 2, "max_addon_domains": 0,
		"max_databases": 2, "max_ftp_accounts": 2, "max_email_accounts": 5, "max_email_quota_mb": 1024, "max_cron_jobs": 2, "max_workers": 0, "max_customers": 0,
		"cpu_percent": 100, "memory_mb": 1024, "tasks_max": 128, "io_read_mbps": 0, "io_write_mbps": 0}},
	{"Business", "A few websites with email for a company.", "Hosting", map[string]int{
		"disk_quota_mb": 20480, "bandwidth_mb": 204800, "max_websites": 5, "max_subdomains": 20, "max_parked_domains": 10, "max_addon_domains": 4,
		"max_databases": 10, "max_ftp_accounts": 10, "max_email_accounts": 25, "max_email_quota_mb": 2048, "max_cron_jobs": 10, "max_workers": 2, "max_customers": 0,
		"cpu_percent": 200, "memory_mb": 2048, "tasks_max": 256, "io_read_mbps": 0, "io_write_mbps": 0}},
	{"Pro", "Many websites, workers and SSH for developers and agencies.", "Hosting with SSH", map[string]int{
		"disk_quota_mb": 51200, "bandwidth_mb": 0, "max_websites": 20, "max_subdomains": 50, "max_parked_domains": 20, "max_addon_domains": 19,
		"max_databases": 50, "max_ftp_accounts": 50, "max_email_accounts": 100, "max_email_quota_mb": 5120, "max_cron_jobs": 25, "max_workers": 5, "max_customers": 0,
		"cpu_percent": 400, "memory_mb": 4096, "tasks_max": 512, "io_read_mbps": 0, "io_write_mbps": 0}},
	{"Email only", "Mailboxes for one domain, no website.", "Email only", map[string]int{
		"disk_quota_mb": 10240, "bandwidth_mb": 51200, "max_websites": 1, "max_subdomains": 0, "max_parked_domains": 0, "max_addon_domains": 0,
		"max_databases": 0, "max_ftp_accounts": 0, "max_email_accounts": 50, "max_email_quota_mb": 2048, "max_cron_jobs": 0, "max_workers": 0, "max_customers": 0,
		"cpu_percent": 50, "memory_mb": 256, "tasks_max": 64, "io_read_mbps": 0, "io_write_mbps": 0}},
}

const (
	setupJobTimeout = time.Hour
	// MaxSetupRounds is how many jobs one step may start before it counts as stuck.
	MaxSetupRounds = 3
)

var oneJobSteps = []string{"php_repository", "auto_updates"}

// SetupOptions are the wizard's options.
type SetupOptions struct {
	Hostname        string   `json:"hostname"`
	PHP             []string `json:"php"`
	ThirdPartyRepos bool     `json:"third_party_repos"`
}

var (
	phpVersionRe = regexp.MustCompile(`^[5-9]\.[0-9]{1,2}$`)
	hostRe       = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// ValidateOptions checks the options for a profile (ServerSetupService::options).
func ValidateOptions(profile string, in SetupOptions) (SetupOptions, error) {
	if _, ok := SetupProfiles[profile]; !ok {
		return in, &ValidationError{Fields: map[string]string{"profile": "Choose full, web or minimal."}}
	}
	php := in.PHP
	if php == nil {
		php = SetupPHPDefault
	}
	seen := map[string]bool{}
	var clean []string
	for _, v := range php {
		if phpVersionRe.MatchString(v) && !seen[v] {
			seen[v] = true
			clean = append(clean, v)
		}
	}
	if len(clean) == 0 || len(clean) != len(php) {
		return in, &ValidationError{Fields: map[string]string{"php": "Choose at least one PHP version, e.g. 8.3,8.4."}}
	}
	sort.Slice(clean, func(i, j int) bool { return versionLess(clean[i], clean[j]) })
	host := strings.ToLower(strings.TrimSpace(in.Hostname))
	if host != "" && (len(host) < 4 || len(host) > 253 || !hostRe.MatchString(host)) {
		return in, &ValidationError{Fields: map[string]string{"hostname": "Enter the server's full host name, e.g. server.example.com."}}
	}
	return SetupOptions{Hostname: host, PHP: clean, ThirdPartyRepos: in.ThirdPartyRepos}, nil
}

// SetupState is the last setup run, remembered in the server_setup setting.
type SetupState struct {
	Profile    string                    `json:"profile"`
	Options    SetupOptions              `json:"options"`
	StartedAt  string                    `json:"started_at"`
	FinishedAt *string                   `json:"finished_at"`
	Steps      map[string]SetupStepState `json:"steps"`
	Attempted  map[string][]string       `json:"attempted,omitempty"`
	Via        string                    `json:"via,omitempty"`
}

// SetupStepState is one step's remembered result.
type SetupStepState struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Last is the last setup run (nil when there was none).
func (s *ServerSetup) Last(ctx context.Context) *SetupState {
	raw, _ := s.cfg.String(ctx, "server_setup", "")
	if raw == "" {
		return nil
	}
	var st SetupState
	if json.Unmarshal([]byte(raw), &st) != nil || st.Profile == "" {
		return nil
	}
	return &st
}

func (s *ServerSetup) save(ctx context.Context, st *SetupState) {
	blob, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = s.cfg.Set(ctx, "server_setup", string(blob), s.ActorID)
}

func (s *ServerSetup) say(line string) { s.out(line) }

func utcNow() string { return time.Now().UTC().Format(time.RFC3339) }

func clip(v string, n int) string {
	if r := []rune(v); len(r) > n {
		return string(r[:n])
	}
	return v
}

// errJobStarted: in the wizard a step that starts a job returns instead of
// waiting; the browser follows the job and calls the step again.
type errJobStarted struct{ Job, Msg string }

func (e *errJobStarted) Error() string { return e.Msg }

// SetupResult is one step's outcome.
type SetupResult struct {
	ID, Label, Status, Detail, JobID string
}

// Run runs the whole setup for a profile from the command line: each step in
// turn, jobs waited for, progress written to out. only restricts the steps.
func (s *ServerSetup) Run(ctx context.Context, profile string, opts SetupOptions, only []string, out func(string)) ([]SetupResult, error) {
	o, err := ValidateOptions(profile, opts)
	if err != nil {
		return nil, err
	}
	if out != nil {
		s.out = out
	}
	s.async = false
	state := &SetupState{Profile: profile, Options: o, StartedAt: utcNow(), Steps: map[string]SetupStepState{}}
	var results []SetupResult
	for _, id := range StepsFor(profile) {
		if len(only) > 0 && !hasStr(only, id) {
			continue
		}
		s.say("")
		s.say("== " + SetupStepLabel(id))
		status, detail := s.call(ctx, profile, o, id)
		word := map[string]string{"ok": "done", "skip": "nothing to do", "fail": "FAILED"}[status]
		s.say(fmt.Sprintf("   %s: %s", word, detail))
		detail = clip(detail, 300)
		results = append(results, SetupResult{ID: id, Label: SetupStepLabel(id), Status: status, Detail: detail})
		state.Steps[id] = SetupStepState{Status: status, Detail: detail}
		s.save(ctx, state)
	}
	fin := utcNow()
	state.FinishedAt = &fin
	s.save(ctx, state)
	var failed []string
	for _, r := range results {
		if r.Status == "fail" {
			failed = append(failed, r.ID)
		}
	}
	s.record(ctx, failed == nil, map[string]any{"profile": profile, "failed": failed})
	return results, nil
}

func (s *ServerSetup) record(ctx context.Context, ok bool, details map[string]any) {
	if s.audit == nil {
		return
	}
	result := "success"
	if !ok {
		result = "failure"
	}
	s.audit(ctx, "server.setup", "server", strconv.FormatInt(s.serverID, 10), result, details)
}

// call runs one step, its errors turned into a failed result.
func (s *ServerSetup) call(ctx context.Context, profile string, o SetupOptions, id string) (status, detail string) {
	var err error
	switch id {
	case "packages":
		status, detail, err = s.stepPackages(ctx, profile, o)
	case "php_repository":
		status, detail, err = s.stepPhpRepository(ctx, profile, o)
	case "php":
		status, detail, err = s.stepPhp(ctx, profile, o)
	case "mail":
		status, detail, err = s.stepMail(ctx, profile, o)
	case "dns":
		status, detail, err = s.stepDns(ctx, profile, o)
	case "ftp":
		status, detail, err = s.stepFtp(ctx, profile, o)
	case "caches":
		status, detail, err = s.stepCaches(ctx, profile, o)
	case "services":
		status, detail, err = s.stepServices(ctx, profile, o)
	case "firewall":
		status, detail, err = s.stepFirewall(ctx, profile, o)
	case "ports":
		status, detail, err = s.stepPorts(ctx, profile, o)
	case "protection":
		status, detail, err = s.stepProtection(ctx, profile, o)
	case "fail2ban":
		status, detail, err = s.stepFail2ban(ctx, profile, o)
	case "hardening":
		status, detail, err = s.stepHardening(ctx, profile, o)
	case "auto_updates":
		status, detail, err = s.stepAutoUpdates(ctx, profile, o)
	case "plans":
		status, detail, err = s.stepPlans(ctx, profile, o)
	case "backups":
		status, detail, err = s.stepBackups(ctx, profile, o)
	case "profile":
		status, detail, err = s.stepProfile(ctx, profile, o)
	default:
		return "fail", "Unknown step."
	}
	if err == nil {
		return status, detail
	}
	var started *errJobStarted
	if errors.As(err, &started) {
		return "running", started.Job // the caller reads the job id from here
	}
	return "fail", failText(err)
}

// failText is the detail of a failed step.
func failText(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) {
		parts := make([]string, 0, len(ve.Fields))
		for _, m := range ve.Fields {
			parts = append(parts, m)
		}
		sort.Strings(parts)
		return strings.Join(parts, " ")
	}
	var ue *UserError
	if errors.As(err, &ue) {
		return ue.What
	}
	return strings.TrimSpace(agentMessage(err))
}

// RunStep runs one step for the setup wizard, which calls the steps from the
// browser: a step that starts a job returns "running" with the job id; the
// browser follows the job and calls the step again with that job id (afterJob),
// and the step then checks what is left. The result is remembered with the
// profile.
func (s *ServerSetup) RunStep(ctx context.Context, profile string, in SetupOptions, id, afterJob string, round int) (SetupResult, error) {
	o, err := ValidateOptions(profile, in)
	if err != nil {
		return SetupResult{}, err
	}
	steps := StepsFor(profile)
	if !hasStr(steps, id) {
		return SetupResult{}, &ValidationError{Fields: map[string]string{"step": "This step is not part of the profile."}}
	}
	state := s.Last(ctx)
	first := id == steps[0] && afterJob == ""
	if first || state == nil || state.Profile != profile || !sameOptions(state.Options, o) {
		state = &SetupState{Profile: profile, Options: o, StartedAt: utcNow(), Steps: map[string]SetupStepState{}, Via: "wizard"}
	}
	if state.Steps == nil {
		state.Steps = map[string]SetupStepState{}
	}
	if afterJob == "" {
		s.attempted = nil
	} else {
		s.attempted = append([]string(nil), state.Attempted[id]...)
	}
	res, err := s.stepAfter(ctx, profile, o, id, afterJob, round)
	if err != nil {
		return SetupResult{}, err
	}
	if state.Attempted == nil {
		state.Attempted = map[string][]string{}
	}
	state.Attempted[id] = s.attempted
	res.ID, res.Label = id, SetupStepLabel(id)
	if res.Status == "running" {
		s.save(ctx, state)
		return res, nil
	}
	state.Steps[id] = SetupStepState{Status: res.Status, Detail: res.Detail}
	if id == steps[len(steps)-1] {
		fin := utcNow()
		state.FinishedAt = &fin
		var failed []string
		for k, v := range state.Steps {
			if v.Status == "fail" {
				failed = append(failed, k)
			}
		}
		sort.Strings(failed)
		s.record(ctx, failed == nil, map[string]any{"profile": profile, "failed": failed, "via": "wizard"})
	}
	s.save(ctx, state)
	return res, nil
}

func sameOptions(a, b SetupOptions) bool {
	if a.Hostname != b.Hostname || a.ThirdPartyRepos != b.ThirdPartyRepos || len(a.PHP) != len(b.PHP) {
		return false
	}
	for i := range a.PHP {
		if a.PHP[i] != b.PHP[i] {
			return false
		}
	}
	return true
}

func (s *ServerSetup) stepAfter(ctx context.Context, profile string, o SetupOptions, id, afterJob string, round int) (SetupResult, error) {
	if afterJob != "" {
		if !JobIDRe.MatchString(afterJob) {
			return SetupResult{}, &ValidationError{Fields: map[string]string{"after_job": "Unknown job."}}
		}
		job, err := s.callMap(ctx, "jobs.get", map[string]any{"id": afterJob}, 60)
		if err != nil {
			return SetupResult{}, err
		}
		switch js(job, "status") {
		case "succeeded":
		case "failed":
			e := js(job, "error")
			if e == "" {
				e = "see its log"
			}
			return SetupResult{Status: "fail", Detail: "The job failed: " + clip(e, 250)}, nil
		default:
			return SetupResult{Status: "running", Detail: "Still running.", JobID: afterJob}, nil
		}
		if hasStr(oneJobSteps, id) {
			return SetupResult{Status: "ok", Detail: "Done (see the job log)."}, nil
		}
	}
	s.async, s.resumed = true, afterJob != ""
	defer func() { s.async, s.resumed = false, false }()
	var status, detail string
	var err error
	func() {
		st, d := s.call(ctx, profile, o, id)
		status, detail = st, d
	}()
	if status == "running" {
		if round >= MaxSetupRounds {
			return SetupResult{Status: "fail", Detail: fmt.Sprintf("Still not done after %d jobs: see the job logs, then run the setup again.", MaxSetupRounds)}, nil
		}
		return SetupResult{Status: "running", Detail: s.jobMsg, JobID: detail}, nil
	}
	// Verified after its job: nothing left to do means done.
	if afterJob != "" && status == "skip" {
		status = "ok"
	}
	_ = err
	return SetupResult{Status: status, Detail: clip(detail, 300)}, nil
}

// --- agent helpers --------------------------------------------------------

func (s *ServerSetup) callMap(ctx context.Context, action string, params any, secs int) (map[string]any, error) {
	raw, err := s.agent.Call(ctx, action, params, time.Duration(secs)*time.Second)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func (s *ServerSetup) callList(ctx context.Context, action string, params any, secs int) ([]any, error) {
	raw, err := s.agent.Call(ctx, action, params, time.Duration(secs)*time.Second)
	if err != nil {
		return nil, err
	}
	var l []any
	_ = json.Unmarshal(raw, &l)
	return l, nil
}

func js(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func jb(m map[string]any, k string) bool {
	switch v := m[k].(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != "" && v != "0" && v != "false"
	}
	return false
}

func jm(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func jl(v any) []any { l, _ := v.([]any); return l }

func jstrs(v any) []string {
	var out []string
	for _, x := range jl(v) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		} else if f, ok := x.(float64); ok {
			out = append(out, strconv.FormatFloat(f, 'f', -1, 64))
		}
	}
	return out
}

func diff(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !hasStr(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func uniq(a []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range a {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// jobMsg is the text of the job a step just started (wizard mode).
// (Kept on the service because call() returns only status and detail.)

// wait waits for an agent job, printing its log; in the wizard it returns
// errJobStarted at once and the browser follows the job.
func (s *ServerSetup) wait(ctx context.Context, job, what string) error {
	var uid *int64
	if s.ActorID > 0 {
		id := s.ActorID
		uid = &id
	}
	_ = s.jobs.Track(ctx, s.serverID, job, what, "server", strconv.FormatInt(s.serverID, 10), uid)
	if s.async {
		s.jobMsg = what + "…"
		return &errJobStarted{Job: job, Msg: what + "…"}
	}
	offset := 0
	until := time.Now().Add(setupJobTimeout)
	var res struct {
		Log    string `json:"log"`
		Offset int    `json:"offset"`
		Done   bool   `json:"done"`
		Job    struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"job"`
	}
	for {
		time.Sleep(s.Poll)
		raw, err := s.jobs.Poll(ctx, s.agent, s.serverID, job, offset)
		if err != nil {
			return err
		}
		res.Log, res.Done = "", false
		_ = json.Unmarshal(raw, &res)
		for _, l := range strings.Split(strings.TrimRight(res.Log, "\n"), "\n") {
			if l != "" {
				s.say("   | " + l)
			}
		}
		if res.Offset > 0 {
			offset = res.Offset
		}
		if res.Done || time.Now().After(until) {
			break
		}
	}
	if !res.Done {
		return &UserError{What: what + ": still running after an hour (job " + job + ")."}
	}
	if res.Job.Status != "succeeded" {
		e := res.Job.Error
		if e == "" {
			e = "see the log above"
		}
		return &UserError{What: what + " failed: " + e}
	}
	return nil
}

// components is components.status by id.
func (s *ServerSetup) components(ctx context.Context) (map[string]map[string]any, error) {
	l, err := s.callList(ctx, "components.status", map[string]any{}, 60)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for _, c := range l {
		m := jm(c)
		out[js(m, "id")] = m
	}
	return out, nil
}

// todo is the items of a component's step that are left to do.
func todoItems(c map[string]any, step string) []string {
	for _, st := range jl(c["steps"]) {
		m := jm(st)
		if js(m, "id") == step && js(m, "status") == "todo" {
			return jstrs(m["items"])
		}
	}
	return nil
}

func stepTodo(c map[string]any, step string) bool {
	for _, st := range jl(c["steps"]) {
		m := jm(st)
		if js(m, "id") == step {
			return js(m, "status") == "todo"
		}
	}
	return false
}

func (s *ServerSetup) orderedComponents(ctx context.Context, profile string) ([]string, map[string]map[string]any, error) {
	comps, err := s.components(ctx)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(comps))
	for id := range comps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, comps, nil
}

func (s *ServerSetup) stepPackages(ctx context.Context, profile string, _ SetupOptions) (string, string, error) {
	ids, comps, err := s.orderedComponents(ctx, profile)
	if err != nil {
		return "", "", err
	}
	var missing []string
	for _, id := range ids {
		if id != "php" && hasStr(SetupProfiles[profile], id) {
			missing = append(missing, todoItems(comps[id], "installed")...)
		}
	}
	missing = uniq(missing)
	if len(missing) == 0 {
		return "skip", "Every component of the profile is installed.", nil
	}
	// The wizard installs a batch per job: what its earlier jobs installed
	// without success is reported, not tried again.
	left := diff(missing, s.attempted)
	if len(left) == 0 {
		return "fail", "Still not installed after their job: " + strings.Join(missing, ", ") + ". See the job log, or install them under Server → Packages.", nil
	}
	s.say("   Installing: " + strings.Join(left, ", "))
	for i := 0; i < len(left); i += 10 {
		chunk := left[i:min(i+10, len(left))]
		s.attempted = uniq(append(s.attempted, chunk...))
		res, err := s.callMap(ctx, "packages.install", map[string]any{"names": chunk}, 120)
		if err != nil {
			return "", "", err
		}
		if err := s.wait(ctx, js(res, "job_id"), "Install "+strings.Join(chunk, ", ")); err != nil {
			return "", "", err
		}
	}
	return "ok", "Installed " + strings.Join(left, ", ") + ".", nil
}

func (s *ServerSetup) installedPHP(ctx context.Context) ([]string, error) {
	res, err := s.callMap(ctx, "discovery.php", map[string]any{}, 60)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range jl(res["versions"]) {
		m := jm(v)
		if js(m, "fpm_binary") != "" {
			out = append(out, js(m, "version"))
		}
	}
	return out, nil
}

func (s *ServerSetup) stepPhpRepository(ctx context.Context, _ string, o SetupOptions) (string, string, error) {
	avail, err := s.callList(ctx, "php.available", map[string]any{}, 60)
	if err != nil {
		return "", "", err
	}
	installed, err := s.installedPHP(ctx)
	if err != nil {
		return "", "", err
	}
	var availS []string
	for _, a := range avail {
		availS = append(availS, fmt.Sprint(a))
	}
	missing := diff(diff(o.PHP, availS), installed)
	if len(missing) == 0 {
		return "skip", "PHP " + strings.Join(o.PHP, ", ") + " come from the configured repositories.", nil
	}
	if !o.ThirdPartyRepos {
		return "skip", "PHP " + strings.Join(missing, ", ") + " is not offered by this system's repositories, and third-party repositories are not allowed: only the offered versions are installed.", nil
	}
	repo, err := s.callMap(ctx, "php.repository", map[string]any{}, 60)
	if err != nil {
		return "", "", err
	}
	name := js(repo, "name")
	if name == "" {
		name = "The PHP repository"
	}
	if jb(repo, "enabled") {
		return "skip", name + " is enabled; PHP " + strings.Join(missing, ", ") + " is not offered by it.", nil
	}
	if !jb(repo, "supported") {
		return "skip", "No PHP repository is supported on this system: PHP " + strings.Join(missing, ", ") + " cannot be installed.", nil
	}
	s.say("   Adding " + name + " for PHP " + strings.Join(missing, ", ") + " (third-party repository; see the installation guide).")
	res, err := s.callMap(ctx, "php.repository.enable", map[string]any{}, 120)
	if err != nil {
		return "", "", err
	}
	if err := s.wait(ctx, js(res, "job_id"), "Enable the PHP repository"); err != nil {
		return "", "", err
	}
	return "ok", name + " added.", nil
}

func (s *ServerSetup) stepPhp(ctx context.Context, _ string, o SetupOptions) (string, string, error) {
	installed, err := s.installedPHP(ctx)
	if err != nil {
		return "", "", err
	}
	availL, err := s.callList(ctx, "php.available", map[string]any{}, 60)
	if err != nil {
		return "", "", err
	}
	var avail []string
	for _, a := range availL {
		avail = append(avail, fmt.Sprint(a))
	}
	var want []string
	for _, v := range o.PHP {
		if hasStr(avail, v) || hasStr(installed, v) {
			want = append(want, v)
		}
	}
	if len(want) == 0 {
		return "fail", "None of PHP " + strings.Join(o.PHP, ", ") + " is offered by the repositories.", nil
	}
	// Nothing is removed: versions and extensions in use stay.
	versions := uniq(append(append([]string{}, installed...), want...))
	sort.Slice(versions, func(i, j int) bool { return versionLess(versions[i], versions[j]) })
	catalog, err := s.callMap(ctx, "php.catalog", map[string]any{}, 60)
	if err != nil {
		return "", "", err
	}
	exts := jstrs(catalog["defaults"])
	for _, v := range installed {
		list, err := s.callList(ctx, "php.extensions", map[string]any{"version": v}, 60)
		if err != nil {
			return "", "", err
		}
		for _, e := range list {
			m := jm(e)
			if jb(m, "installed") && jb(m, "enabled") {
				exts = append(exts, js(m, "name"))
			}
		}
	}
	exts = uniq(exts)
	profileData := map[string]any{"versions": versions, "extensions": exts}
	plan, err := s.callMap(ctx, "php.profile_plan", profileData, 120)
	if err != nil {
		return "", "", err
	}
	changes := jb(plan, "changes")
	if changes && s.resumed {
		// Applied, yet still different: applying again would not help.
		return "fail", "Applied, but still to do: " + pendingPHP(plan) + ". See the job log; the PHP Manager shows the same review.", nil
	}
	if !changes {
		s.rememberPhp(ctx, versions, exts, "")
		return "skip", "PHP " + strings.Join(versions, ", ") + " with the default extensions are installed.", nil
	}
	apply := map[string]any{"versions": versions, "extensions": exts, "confirm_removals": false}
	res, err := s.callMap(ctx, "php.profile_apply", apply, 120)
	if err != nil {
		return "", "", err
	}
	job := js(res, "job_id")
	if err := s.wait(ctx, job, "Apply PHP profile"); err != nil {
		return "", "", err
	}
	s.rememberPhp(ctx, versions, exts, job)
	detail := fmt.Sprintf("PHP %s with %d extensions.", strings.Join(versions, ", "), len(exts))
	if skipped := diff(o.PHP, want); len(skipped) > 0 {
		detail += " Not offered: PHP " + strings.Join(skipped, ", ") + "."
	}
	return "ok", detail, nil
}

// pendingPHP is what a PHP plan still changes, briefly.
func pendingPHP(plan map[string]any) string {
	var out []string
	for _, it := range jl(plan["items"]) {
		m := jm(it)
		act := js(m, "action")
		if act == "unchanged" || act == "blocked" {
			continue
		}
		t := "PHP " + js(m, "version")
		if e := js(m, "extension"); e != "" {
			t += " " + e
		}
		t += " (" + act
		if d := js(m, "detail"); d != "" {
			t += ": " + d
		}
		out = append(out, t+")")
	}
	more := ""
	if len(out) > 6 {
		more = fmt.Sprintf(" and %d more", len(out)-6)
		out = out[:6]
	}
	return strings.Join(out, ", ") + more
}

func (s *ServerSetup) rememberPhp(ctx context.Context, versions, exts []string, job string) {
	if cur, _ := s.cfg.String(ctx, "php_profile", ""); cur != "" && job == "" {
		return
	}
	p := map[string]any{"versions": versions, "extensions": exts, "applied_at": time.Now().UTC().Format("2006-01-02 15:04")}
	if job != "" {
		p["job_id"] = job
	}
	if blob, err := json.Marshal(p); err == nil {
		_ = s.cfg.Set(ctx, "php_profile", string(blob), s.ActorID)
	}
}

func (s *ServerSetup) stepMail(ctx context.Context, _ string, o SetupOptions) (string, string, error) {
	comps, err := s.components(ctx)
	if err != nil {
		return "", "", err
	}
	c := comps["mail"]
	if !jb(c, "installed") {
		return "fail", "The mail packages are not installed (see the first step).", nil
	}
	if !stepTodo(c, "setup") {
		return "skip", "Email is set up already (its settings are left as they are).", nil
	}
	host := o.Hostname
	if host == "" {
		h, _ := os.Hostname()
		host = strings.ToLower(h)
	}
	if !strings.Contains(host, ".") || strings.HasSuffix(host, ".localdomain") || strings.HasSuffix(host, ".local") {
		return "fail", "The server has no full host name (" + host + "). Enter one in the options, or set up email on the Email page.", nil
	}
	// Choosing a profile with email is the consent to WhaCP editing Postfix's
	// and Dovecot's configuration (backups are kept).
	if _, err := s.callMap(ctx, "mail.configure", map[string]any{"hostname": host, "confirm_edit_package_config": true}, 300); err != nil {
		return "", "", err
	}
	return "ok", "Postfix, Dovecot and OpenDKIM set up for " + host + ".", nil
}

func (s *ServerSetup) stepDns(ctx context.Context, _ string, _ SetupOptions) (string, string, error) {
	comps, err := s.components(ctx)
	if err != nil {
		return "", "", err
	}
	c := comps["dns"]
	if !jb(c, "installed") {
		return "fail", "BIND is not installed (see the first step).", nil
	}
	if !stepTodo(c, "setup") {
		return "skip", "DNS hosting is on already.", nil
	}
	// Authoritative only: WhaCP's configuration answers for the hosted zones,
	// without recursion.
	if _, err := s.callMap(ctx, "dns.configure", map[string]any{"confirm_edit_package_config": true}, 120); err != nil {
		return "", "", err
	}
	return "ok", "BIND answers for the zones hosted here (authoritative, no recursion).", nil
}

func (s *ServerSetup) stepFtp(ctx context.Context, _ string, _ SetupOptions) (string, string, error) {
	comps, err := s.components(ctx)
	if err != nil {
		return "", "", err
	}
	c := comps["ftp"]
	if !jb(c, "installed") {
		return "fail", "pure-ftpd is not installed (see the first step).", nil
	}
	if !stepTodo(c, "setup") {
		return "skip", "The FTP server is set up already.", nil
	}
	// The defaults: TLS required, passive ports 30000-30049. Another running
	// FTP server is never replaced here.
	if _, err := s.callMap(ctx, "ftp.configure", map[string]any{}, 120); err != nil {
		return "", "", err
	}
	return "ok", "FTP server set up (TLS required; passive ports 30000-30049).", nil
}

func (s *ServerSetup) stepCaches(ctx context.Context, _ string, _ SetupOptions) (string, string, error) {
	comps, err := s.components(ctx)
	if err != nil {
		return "", "", err
	}
	if !jb(comps["redis"], "installed") {
		return "fail", "Redis is not installed (see the first step).", nil
	}
	st, err := s.callMap(ctx, "redis.info", map[string]any{}, 60)
	if err != nil {
		return "", "", err
	}
	max := js(st, "maxmemory")
	if max == "" {
		return "skip", "Redis is not answering yet; set its memory limit on the Redis page.", nil
	}
	if max != "0" {
		return "skip", "Redis has a memory limit already.", nil
	}
	// Unlimited Redis memory can push websites out of memory.
	if _, err := s.callMap(ctx, "redis.configure", map[string]any{"maxmemory_mb": 256, "policy": "allkeys-lru"}, 60); err != nil {
		return "", "", err
	}
	return "ok", "Redis keeps at most 256 MB (least recently used keys are evicted).", nil
}

func (s *ServerSetup) stepServices(ctx context.Context, profile string, _ SetupOptions) (string, string, error) {
	ids, comps, err := s.orderedComponents(ctx, profile)
	if err != nil {
		return "", "", err
	}
	var done []string
	for _, id := range ids {
		if id == "firewall" || !hasStr(SetupProfiles[profile], id) {
			continue
		}
		for _, unit := range todoItems(comps[id], "running") {
			if _, err := s.callMap(ctx, "services.control", map[string]any{"unit": unit, "op": "start"}, 90); err != nil {
				return "", "", err
			}
			done = append(done, "started "+unit)
		}
		for _, unit := range todoItems(comps[id], "boot") {
			if _, err := s.callMap(ctx, "services.control", map[string]any{"unit": unit, "op": "enable"}, 60); err != nil {
				return "", "", err
			}
			done = append(done, unit+" at boot")
		}
	}
	if len(done) == 0 {
		return "skip", "Every service runs and starts at boot.", nil
	}
	d := strings.Join(done, ", ")
	return "ok", strings.ToUpper(d[:1]) + d[1:] + ".", nil
}

// keep keeps a guarded firewall change once every critical port (SSH, the
// panel, the agent's listener) is open; otherwise it is undone now.
func (s *ServerSetup) keep(ctx context.Context, res map[string]any, what string) error {
	pending := jm(res["pending"])
	if !jb(res, "changed") || js(pending, "id") == "" {
		return nil
	}
	fw, err := s.callMap(ctx, "firewall.status", map[string]any{}, 60)
	if err != nil {
		return err
	}
	var closed []string
	// With the firewall off nothing is blocked.
	if jb(fw, "active") {
		for _, r := range jl(fw["required"]) {
			m := jm(r)
			if jb(m, "critical") && !jb(m, "open") {
				closed = append(closed, strings.TrimSpace(js(m, "service")+" "+js(m, "port")+"/"+js(m, "proto")))
			}
		}
	}
	if len(closed) > 0 {
		_, _ = s.callMap(ctx, "security.undo", map[string]any{"id": js(pending, "id")}, 90)
		return &UserError{What: what + ": undone, because " + strings.Join(closed, ", ") + " would not be reachable. Allow it on the Firewall page and run the setup again."}
	}
	if _, err := s.callMap(ctx, "security.confirm", map[string]any{"id": js(pending, "id")}, 30); err != nil {
		return err
	}
	if s.audit != nil {
		s.audit(ctx, "security.confirm", "server", "security", "success", map[string]any{"what": what, "via": "setup"})
	}
	return nil
}

func (s *ServerSetup) stepFirewall(ctx context.Context, _ string, _ SetupOptions) (string, string, error) {
	fw, err := s.callMap(ctx, "firewall.status", map[string]any{}, 60)
	if err != nil {
		return "", "", err
	}
	if !jb(fw, "installed") {
		return "fail", "No firewall is installed (UFW, or firewalld on the RHEL family). Install it and run the setup again.", nil
	}
	if jb(fw, "active") {
		return "skip", "The firewall is on.", nil
	}
	// Enabling opens SSH, the panel and the agent's listener first, and the
	// ports of services listening now.
	res, err := s.callMap(ctx, "firewall.set_enabled", map[string]any{"enabled": true, "allow_services": true}, 120)
	if err != nil {
		return "", "", err
	}
	if err := s.keep(ctx, res, "Turning on the firewall"); err != nil {
		return "", "", err
	}
	if s.audit != nil {
		s.audit(ctx, "firewall.enable", "server", "security", "success", map[string]any{"via": "setup"})
	}
	after, err := s.callMap(ctx, "firewall.status", map[string]any{}, 60)
	if err != nil {
		return "", "", err
	}
	if !jb(after, "active") {
		return "fail", "The firewall was turned on but reads as off: see the Firewall page.", nil
	}
	return "ok", "The firewall is on; SSH, the panel and the listening services are allowed.", nil
}

var commentRe1, commentRe2 = regexp.MustCompile(`[^A-Za-z0-9 ._:/-]+`), regexp.MustCompile(` {2,}`)

func (s *ServerSetup) stepPorts(ctx context.Context, profile string, _ SetupOptions) (string, string, error) {
	ids, comps, err := s.orderedComponents(ctx, profile)
	if err != nil {
		return "", "", err
	}
	var opened []string
	for _, id := range ids {
		if !hasStr(SetupProfiles[profile], id) {
			continue
		}
		items := todoItems(comps[id], "ports")
		if len(items) == 0 {
			continue
		}
		var ports []map[string]any
		for _, p := range items {
			port, proto, _ := strings.Cut(p, "/")
			if proto == "" {
				proto = "tcp"
			}
			ports = append(ports, map[string]any{"port": port, "proto": proto})
		}
		name := js(comps[id], "name")
		comment := clip(strings.TrimSpace(commentRe2.ReplaceAllString(commentRe1.ReplaceAllString(name, " "), " ")), 60)
		res, err := s.callMap(ctx, "firewall.allow_ports", map[string]any{"ports": ports, "comment": comment}, 90)
		if err != nil {
			return "", "", err
		}
		if err := s.keep(ctx, res, "Opening the ports of "+name); err != nil {
			return "", "", err
		}
		if s.audit != nil {
			s.audit(ctx, "firewall.allow_ports", "server", "security", "success", map[string]any{"ports": items, "via": "setup"})
		}
		opened = append(opened, name+" ("+strings.Join(items, ", ")+")")
	}
	if len(opened) == 0 {
		return "skip", "The components' ports are open (or the firewall is off).", nil
	}
	return "ok", "Opened: " + strings.Join(opened, "; ") + ".", nil
}

func (s *ServerSetup) stepProtection(ctx context.Context, _ string, _ SetupOptions) (string, string, error) {
	st, err := s.callMap(ctx, "firewall.protection", map[string]any{}, 60)
	if err != nil {
		return "", "", err
	}
	if jb(jm(st["config"]), "enabled") {
		return "skip", "The firewall protections are on (left as configured).", nil
	}
	// Conservative: invalid packets and port-scan flag combinations, and a
	// generous limit of new connections per address.
	res, err := s.callMap(ctx, "firewall.protection_set", map[string]any{"enabled": true, "invalid": true, "syn_rate": 100,
		"conn_limits": []any{}, "port_flood": []any{}, "icmp_rate": 0, "exempt": []any{}}, 120)
	if err != nil {
		return "", "", err
	}
	if err := s.keep(ctx, res, "The firewall protections"); err != nil {
		return "", "", err
	}
	return "ok", "Invalid packets are dropped; one address may open 100 new connections per second.", nil
}

func (s *ServerSetup) stepFail2ban(ctx context.Context, _ string, _ SetupOptions) (string, string, error) {
	st, err := s.callMap(ctx, "fail2ban.status", map[string]any{}, 60)
	if err != nil {
		return "", "", err
	}
	if !jb(st, "installed") {
		return "fail", "fail2ban is not installed (see the first step).", nil
	}
	if jb(st, "managed_config") {
		return "skip", "WhaCP's fail2ban jails are configured (left as they are).", nil
	}
	jails := []string{"sshd", "recidive", "nginx-http-auth", "nginx-botsearch"}
	if _, err := s.callMap(ctx, "fail2ban.configure", map[string]any{"jails": jails, "ban_minutes": 60, "find_minutes": 10, "max_retry": 5, "ignore": []any{}}, 120); err != nil {
		return "", "", err
	}
	return "ok", "Jails " + strings.Join(jails, ", ") + ": 5 failures in 10 minutes ban for an hour.", nil
}

func (s *ServerSetup) stepHardening(ctx context.Context, profile string, _ SetupOptions) (string, string, error) {
	var done []string
	k, err := s.callMap(ctx, "security.fix_kernel", map[string]any{}, 60)
	if err != nil {
		return "", "", err
	}
	if changed := jstrs(k["changed"]); len(changed) == 0 {
		done = append(done, "kernel settings already recommended")
	} else {
		done = append(done, "kernel settings: "+strings.Join(changed, ", "))
	}
	if profile == "full" {
		// Only the mail server (and root, the panel) may reach remote SMTP
		// ports: websites send through it, so spam from a hacked website is
		// visible in the mail queue and logs.
		st, err := s.callMap(ctx, "hardening.status", map[string]any{}, 60)
		if err != nil {
			return "", "", err
		}
		if !jb(jm(st["smtp"]), "enabled") {
			if _, err := s.callMap(ctx, "hardening.smtp", map[string]any{"enabled": true}, 60); err != nil {
				return "", "", err
			}
			done = append(done, "SMTP restrictions on")
		} else {
			done = append(done, "SMTP restrictions already on")
		}
	}
	d := strings.Join(done, "; ")
	return "ok", strings.ToUpper(d[:1]) + d[1:] + ".", nil
}

func (s *ServerSetup) stepAutoUpdates(ctx context.Context, _ string, _ SetupOptions) (string, string, error) {
	res, err := s.callMap(ctx, "security.auto_updates", map[string]any{"enabled": true}, 60)
	if err != nil {
		return "", "", err
	}
	if job := js(res, "job_id"); job != "" {
		if err := s.wait(ctx, job, "Automatic security updates"); err != nil {
			return "", "", err
		}
	}
	return "ok", "Security updates are installed automatically (no automatic reboot).", nil
}

func (s *ServerSetup) stepPlans(ctx context.Context, _ string, _ SetupOptions) (string, string, error) {
	var made []string
	lists := map[string]int64{}
	for _, fl := range setupFeatureLists {
		var id int64
		err := s.db.QueryRowContext(ctx, `SELECT id FROM feature_lists WHERE name = ?`, fl.Name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			id, err = s.features.Save(ctx, nil, fl.Name, fl.Description, fl.Features, 0)
			if err != nil {
				return "", "", err
			}
			if s.audit != nil {
				s.audit(ctx, "feature_list.save", "feature_list", strconv.FormatInt(id, 10), "success", map[string]any{"name": fl.Name, "via": "setup"})
			}
			made = append(made, "feature list "+fl.Name)
		} else if err != nil {
			return "", "", err
		}
		lists[fl.Name] = id
	}
	actor := &identity.User{ID: 0, RoleScope: "all"}
	for _, p := range setupPlans {
		var existing int64
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM plans WHERE owner_id IS NULL AND name = ?`, p.Name).Scan(&existing); err == nil {
			continue
		}
		fields, limits := map[string]int{}, map[string]int{}
		for _, f := range PlanFields {
			fields[f] = p.Limits[f]
		}
		for col := range PlanLimitColumns {
			limits[col] = p.Limits[col]
		}
		list := lists[p.List]
		if _, err := s.plans.Save(ctx, actor, PlanSave{Name: p.Name, Description: p.Description, Fields: fields, Limits: limits, FeatureList: &list}); err != nil {
			return "", "", err
		}
		made = append(made, "plan "+p.Name)
	}
	if len(made) == 0 {
		return "skip", "The plans and feature lists exist (left as they are).", nil
	}
	return "ok", "Created " + strings.Join(made, ", ") + ".", nil
}

func (s *ServerSetup) stepBackups(ctx context.Context, _ string, _ SetupOptions) (string, string, error) {
	existing, err := s.sched.ListServer(ctx)
	if err != nil {
		return "", "", err
	}
	var made []string
	hasAll, hasServer := false, false
	for _, e := range existing {
		hasAll = hasAll || e.AllSites
		hasServer = hasServer || e.Kind == "server"
	}
	if !hasAll {
		// Sunday 02:00, four weeks kept, on this server (a remote storage can be
		// added to the schedule later).
		if _, err := s.sched.CreateServer(ctx, ServerScheduleInput{Scope: "all_sites", Frequency: "weekly", Weekday: 0, Hour: 2, Retention: 4,
			IncludeFiles: true, IncludeDatabases: true, Encrypt: true}); err != nil {
			return "", "", err
		}
		made = append(made, "every website, Sundays at 02:00, 4 kept per website")
	}
	if !hasServer {
		if _, err := s.sched.CreateServer(ctx, ServerScheduleInput{Scope: "server", Frequency: "weekly", Weekday: 0, Hour: 1, Retention: 4}); err != nil {
			return "", "", err
		}
		made = append(made, "server configuration, Sundays at 01:00, 4 kept")
	}
	if len(made) == 0 {
		return "skip", "Backup schedules exist (left as they are).", nil
	}
	return "ok", "Weekly backups: " + strings.Join(made, "; ") + ". Backups are kept on this server: add a remote storage under Backups for copies elsewhere.", nil
}

func (s *ServerSetup) stepProfile(ctx context.Context, profile string, _ SetupOptions) (string, string, error) {
	comps := SetupProfiles[profile]
	if err := s.cfg.Set(ctx, "components_expected", strings.Join(comps, ","), s.ActorID); err != nil {
		return "", "", err
	}
	return "ok", SetupProfileNames[profile] + ": Server → Components checks " + strings.Join(comps, ", ") + ".", nil
}
