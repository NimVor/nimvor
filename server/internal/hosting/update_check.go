package hosting

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// UpdateCheckService answers "is a newer WhaCP available?". The agent reports
// the installed checkout (commit, branch, remote; it has no network access
// itself); the panel asks GitHub for the branch's newest commit. The answer is
// kept in a setting, refreshed by the scheduler every six hours or on request,
// so pages never wait for GitHub. A remote that is not on GitHub, or a private
// repository, is reported as "unknown", never as "up to date".
type UpdateCheckService struct {
	agent AgentCaller
	cfg   *settings.Repository
	// Fetch returns the HTTP status and body of a URL (tests replace it).
	Fetch func(ctx context.Context, url string) (int, string)
}

// UpdateCheckKey is the setting that remembers the last check.
const UpdateCheckKey = "_update_check"

// UpdateCheckMaxAge is how old a check may be before the scheduler repeats it.
const UpdateCheckMaxAge = 6 * time.Hour

// UpdateCheck is one lookup's result.
type UpdateCheck struct {
	CheckedAt int64  `json:"checked_at"`
	Available bool   `json:"available"`
	State     string `json:"state"` // unknown | current | available
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Branch    string `json:"branch"`
	Updatable bool   `json:"updatable"`
	Reason    string `json:"reason"`
	Command   string `json:"command"`
}

// NewUpdateCheckService builds the service.
func NewUpdateCheckService(agent AgentCaller, cfg *settings.Repository) *UpdateCheckService {
	return &UpdateCheckService{agent: agent, cfg: cfg, Fetch: httpsFetch}
}

func httpsFetch(ctx context.Context, u string) (int, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, ""
	}
	req.Header.Set("User-Agent", "WhaCP update check")
	req.Header.Set("Accept", "application/vnd.github.sha")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return resp.StatusCode, string(b)
}

// Cached is the last check (nil when there was none).
func (s *UpdateCheckService) Cached(ctx context.Context) *UpdateCheck {
	raw, _ := s.cfg.String(ctx, UpdateCheckKey, "")
	if raw == "" {
		return nil
	}
	var c UpdateCheck
	if json.Unmarshal([]byte(raw), &c) != nil {
		return nil
	}
	return &c
}

// Due says whether the last check is older than UpdateCheckMaxAge.
func (s *UpdateCheckService) Due(ctx context.Context) bool {
	c := s.Cached(ctx)
	return c == nil || c.CheckedAt < time.Now().Add(-UpdateCheckMaxAge).Unix()
}

var (
	githubRemoteRe = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:|ssh://git@github\.com/)([A-Za-z0-9_.-]{1,100})/([A-Za-z0-9_.-]{1,100}?)(?:\.git)?/?$`)
	sha40Re        = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// GithubRepo is "owner/repo" of a GitHub remote URL ("" for any other).
func GithubRepo(remote string) string {
	if m := githubRemoteRe.FindStringSubmatch(remote); m != nil {
		return m[1] + "/" + m[2]
	}
	return ""
}

// Check looks the newest commit of the installed branch up and remembers it.
func (s *UpdateCheckService) Check(ctx context.Context) *UpdateCheck {
	res := &UpdateCheck{CheckedAt: time.Now().Unix(), State: "unknown"}
	raw, err := s.agent.Call(ctx, "whacp.update_status", map[string]any{}, 60*time.Second)
	if err != nil {
		res.Reason = "The agent could not report the installed version: " + agentMessage(err)
		return s.save(ctx, res)
	}
	var src struct {
		Git       bool   `json:"git"`
		Commit    string `json:"commit"`
		Branch    string `json:"branch"`
		Remote    string `json:"remote"`
		Updatable bool   `json:"updatable"`
		Reason    string `json:"reason"`
		Command   string `json:"command"`
	}
	_ = json.Unmarshal(raw, &src)
	res.Current, res.Branch, res.Updatable, res.Reason, res.Command = src.Commit, src.Branch, src.Updatable, src.Reason, src.Command
	repo := GithubRepo(src.Remote)
	if !src.Git || repo == "" || src.Branch == "" || !sha40Re.MatchString(src.Commit) {
		if res.Reason == "" {
			res.Reason = "Updates can only be looked up for a checkout of a GitHub repository."
		}
		return s.save(ctx, res)
	}
	code, body := s.Fetch(ctx, "https://api.github.com/repos/"+repo+"/commits/"+url.PathEscape(src.Branch))
	sha := strings.TrimSpace(body)
	if code != 200 || !sha40Re.MatchString(sha) {
		if code == 404 {
			res.Reason = "GitHub does not show this repository without signing in (private repository); update over SSH."
		} else {
			res.Reason = "GitHub could not be reached (HTTP " + strconv.Itoa(code) + ")."
		}
		return s.save(ctx, res)
	}
	res.Latest = sha
	res.Available = sha != src.Commit
	res.State = "current"
	if res.Available {
		res.State = "available"
	}
	return s.save(ctx, res)
}

func (s *UpdateCheckService) save(ctx context.Context, c *UpdateCheck) *UpdateCheck {
	if blob, err := json.Marshal(c); err == nil {
		_ = s.cfg.Set(ctx, UpdateCheckKey, string(blob), 0)
	}
	return c
}
