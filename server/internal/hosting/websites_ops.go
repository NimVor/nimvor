package hosting

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"time"
)

// This file ports the advanced operations of app/Services/WebsiteService.php:
// restartPhp, reapply, permissions (check/repair) and setRuntime. The
// directory-listing op (SetDirectoryListing) lives with the other patch ops in
// websites_write.go, and the app-Worker provisioning (provisionApp) is a
// controller concern, ported in the HTTP layer.

// RestartPhp reloads the website's PHP-FPM unit (WebsiteService::restartPhp).
// The unit is named per distribution by discovery when the agent reports it
// (php8.3-fpm on Debian, php83-php-fpm on RHEL); otherwise the default
// php<version>-fpm.service name is used.
func (s *WebsiteService) RestartPhp(ctx context.Context, site *Website) error {
	if site.PhpVersion == "" {
		return &UserError{What: "This website does not use PHP."}
	}
	unit := "php" + site.PhpVersion + "-fpm.service"
	raw, err := s.agent.Call(ctx, "discovery.php")
	if err != nil {
		return err
	}
	var d struct {
		Versions []struct {
			Version    string `json:"version"`
			FPMService string `json:"fpm_service"`
		} `json:"versions"`
	}
	if json.Unmarshal(raw, &d) == nil {
		for _, v := range d.Versions {
			if v.Version == site.PhpVersion && v.FPMService != "" {
				unit = v.FPMService
			}
		}
	}
	if _, err := s.agent.Call(ctx, "services.control", map[string]any{"unit": unit, "op": "reload"}); err != nil {
		return err
	}
	s.recordAudit(ctx, "website.restart_php", "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
		"domain": site.Domain,
	})
	return nil
}

// Reapply writes the stored state to the server again (WebsiteService::reapply):
// it repairs drift after a manual edit of the generated configuration.
func (s *WebsiteService) Reapply(ctx context.Context, site *Website) error {
	if err := s.apply(ctx, site, false); err != nil {
		return err
	}
	s.recordAudit(ctx, "website.reapply", "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
		"domain": site.Domain,
	})
	return nil
}

// Permissions checks (dry run) or repairs the ownership and permissions of
// the website's files (WebsiteService::permissions). The agent walk can take
// a long time on large trees, so the call gets its own 30-minute budget.
// Returns the agent's result (checked/fixed/skipped_count/…).
func (s *WebsiteService) Permissions(ctx context.Context, site *Website, repair bool) (map[string]any, error) {
	raw, err := s.agent.Call(ctx, "sites.permissions", map[string]any{
		"domain": site.Domain, "user": site.SystemUser, "document_root": site.DocumentRoot, "dry_run": !repair,
	}, 1800*time.Second)
	if err != nil {
		return nil, err
	}
	res := map[string]any{}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if repair {
		s.recordAudit(ctx, "website.permissions_repair", "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
			"domain": site.Domain, "fixed": jsonInt(res["fixed"]),
		})
	}
	return res, nil
}

// jsonInt reads a numeric JSON value as an int (PHP (int) $res['fixed']).
func jsonInt(v any) int {
	f, _ := v.(float64)
	return int(f)
}

// SetRuntime changes an app website's runtime (WebsiteService::setRuntime).
// The caller updates the app worker to match; this only stores the choice and
// audits it.
func (s *WebsiteService) SetRuntime(ctx context.Context, site *Website, runtime string) error {
	typ := AppType(site)
	if typ == "" {
		return &UserError{What: "This is not a Node.js or Python website."}
	}
	if !regexp.MustCompile(`^` + appRuntimePrefix[typ] + `:[0-9][0-9A-Za-z.]{0,31}$`).MatchString(runtime) {
		return &ValidationError{Fields: map[string]string{"app_runtime": "Choose a " + appKinds[typ] + " version."}}
	}
	if err := s.sites.UpdateFields(ctx, site.ID, map[string]any{"app_runtime": runtime}); err != nil {
		return err
	}
	s.recordAudit(ctx, "website.runtime", "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
		"runtime": runtime,
	})
	return nil
}
