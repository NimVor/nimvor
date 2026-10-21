package hosting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// AccountService creates a hosting account in one step
// (AccountService::create): the panel login (a hosting customer, or a
// reseller), its plan and hosting options, and — when a domain is given — its
// first website for that domain, run by a new system account of the same
// name. The website then gets its DNS zone, email and automatic SSL through
// the normal website creation.
//
// If the website cannot be created, the login is removed again.
type AccountService struct {
	users    *UserService
	userRepo *identity.Repository
	sites    *WebsiteService
	options  *AccountOptionStore
	db       *sql.DB
	audit    Auditor
}

// NewAccountService builds the service.
func NewAccountService(users *UserService, userRepo *identity.Repository, sites *WebsiteService, options *AccountOptionStore, db *sql.DB, audit Auditor) *AccountService {
	return &AccountService{users: users, userRepo: userRepo, sites: sites, options: options, db: db, audit: audit}
}

// Account is the result of AccountService.Create
// (AccountService::create's return value).
type Account struct {
	ID        int64
	Username  string
	WebsiteID sql.NullInt64
	Password  sql.NullString // set only when generated
}

// accountUsernameRe mirrors AccountService::create's username pattern (stricter
// than panel logins: also the website's system account name).
var accountUsernameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,30}$`)

// reservedSystemUsers are taken server accounts (AccountService::create).
var reservedSystemUsers = map[string]bool{
	"root": true, "www-data": true, "nobody": true, "mysql": true,
	"whacp": true, "admin": true, "postfix": true, "dovecot": true,
	"bind": true, "named": true, "redis": true, "memcache": true,
}

// Create makes a hosting account (AccountService::create). in carries the
// account form (username, email, display_name, password,
// password_confirmation, generate, reseller, plan_id, domain, www,
// php_version and the option checkboxes).
func (s *AccountService) Create(ctx context.Context, in map[string]string, actor *identity.User) (*Account, error) {
	username := strings.ToLower(strings.TrimSpace(in["username"]))
	domain := strings.ToLower(strings.TrimRight(strings.TrimSpace(in["domain"]), "."))
	errs := &ValidationError{}
	// Also the name of the website's system account.
	if !accountUsernameRe.MatchString(username) || reservedSystemUsers[username] {
		errs.Add("username", "Use 3–31 lowercase letters, digits, - or _, starting with a letter (also the website's system account).")
	}
	reseller := formBool(in["reseller"])
	if reseller && !IsAll(actor) {
		errs.Add("reseller", "Only administrators create resellers.")
	}
	var generated string
	if formBool(in["generate"]) {
		var err error
		generated, err = GeneratedPassword()
		if err != nil {
			return nil, err
		}
		in["password"] = generated
		in["password_confirmation"] = generated
	}
	if domain != "" {
		// Checked before anything is created (format, not taken).
		_, err := s.sites.Validate(ctx, map[string]string{
			"domain": domain, "kind": "static", "system_user": username,
		}, nil, IsAll(actor))
		if err != nil {
			var ve *ValidationError
			if !errors.As(err, &ve) {
				return nil, err
			}
			for _, k := range []string{"domain", "aliases"} {
				if m, ok := ve.Fields[k]; ok {
					errs.Add(k, m)
				}
			}
		}
	}
	if errs.Fields != nil {
		return nil, errs
	}
	name := "customer"
	if reseller {
		name = "reseller"
	}
	var roleID int64
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM roles WHERE name = ?", name).Scan(&roleID); err != nil {
		return nil, fmt.Errorf("account: role %q: %w", name, err)
	}
	id, err := s.users.Create(ctx, map[string]string{
		"username":              username,
		"email":                 in["email"],
		"display_name":          in["display_name"],
		"password":              in["password"],
		"password_confirmation": in["password_confirmation"],
		"role_id":               strconv.FormatInt(roleID, 10),
		"is_active":             "1",
		"plan_id":               in["plan_id"],
	}, actor)
	if err != nil {
		return nil, err
	}
	if err := s.options.Set(ctx, id, s.OptionsFrom(in)); err != nil {
		return nil, err
	}
	var websiteID sql.NullInt64
	if domain != "" {
		// As the new account: its plan and domain rules apply.
		account, err := s.userRepo.Find(ctx, id)
		if err != nil {
			return nil, err
		}
		phpVersion := in["php_version"]
		if phpVersion == "" {
			if vs := s.sites.PhpVersions(ctx); len(vs) > 0 {
				phpVersion = vs[0]
			}
		}
		aliases := ""
		if formBoolOr(in["www"], true) {
			aliases = "www." + domain
		}
		wid, err := s.sites.Create(ctx, map[string]string{
			"domain":          domain,
			"kind":            "php",
			"php_version":     phpVersion,
			"system_user":     username,
			"new_system_user": "1",
			"aliases":         aliases,
		}, account, nil)
		if err != nil {
			_ = s.userRepo.Delete(ctx, id)
			s.recordAudit(ctx, "account.create", "user", username, "failure", map[string]any{
				"domain": domain, "error": err.Error(),
			})
			return nil, err
		}
		websiteID = sql.NullInt64{Int64: wid, Valid: true}
	}
	password := sql.NullString{}
	if generated != "" {
		password = sql.NullString{String: generated, Valid: true}
	}
	s.recordAudit(ctx, "account.create", "user", strconv.FormatInt(id, 10), "success", map[string]any{
		"username": username, "domain": domain, "reseller": reseller,
	})
	return &Account{ID: id, Username: username, WebsiteID: websiteID, Password: password}, nil
}

// OptionsFrom picks the account hosting options out of the form
// (AccountService::optionsFrom): the checkboxes default off, mail routing
// defaults to "auto", and DMARC requires DKIM and SPF.
func (s *AccountService) OptionsFrom(in map[string]string) map[string]any {
	out := map[string]any{
		"dns_dkim":         formBool(in["dns_dkim"]),
		"dns_spf":          formBool(in["dns_spf"]),
		"dns_dmarc":        formBool(in["dns_dmarc"]),
		"dns_registrar_ns": formBool(in["dns_registrar_ns"]),
		"dns_overwrite":    formBool(in["dns_overwrite"]),
		"spam_filter":      formBool(in["spam_filter"]),
		"spam_box":         formBool(in["spam_box"]),
	}
	if mr, ok := in["mail_routing"]; ok && mailRoutings[mr] {
		out["mail_routing"] = mr
	} else {
		out["mail_routing"] = "auto"
	}
	// DMARC needs DKIM and SPF.
	out["dns_dmarc"] = out["dns_dmarc"] && out["dns_dkim"] && out["dns_spf"]
	return out
}

// recordAudit is the nil-safe audit hook (same contract as WebsiteService's).
func (s *AccountService) recordAudit(ctx context.Context, action, targetType, target, result string, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, targetType, target, result, details)
	}
}

// passwordChars is the AccountService password alphabet (unambiguous set).
const passwordChars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_.!@#%"

// GeneratedPassword makes a strong password: 20 characters from the
// unambiguous set (AccountService::password).
func GeneratedPassword() (string, error) {
	buf := make([]byte, 20)
	for i := range buf {
		n, err := rand.Int(rand.Reader, int64(len(passwordChars)))
		if err != nil {
			return "", err
		}
		buf[i] = passwordChars[n.Int64()]
	}
	return string(buf), nil
}

// formBoolOr is formBool with a default for a missing key (the PHP
// filter_var($input['www'] ?? true) form).
func formBoolOr(v string, def bool) bool {
	if v == "" {
		return def
	}
	return formBool(v)
}
