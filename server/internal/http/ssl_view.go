package http

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// sslCertBadge is the certificate state as a badge and one line (ssl/_cert.php).
type sslCertBadge struct {
	Class, Label, Line string
	Error              bool
}

var (
	sslOrgRE = regexp.MustCompile(`O=([^,]+)`)
	sslCNRE  = regexp.MustCompile(`CN=([^,]+)`)
)

func sslCert(c any, warnDays int) sslCertBadge {
	days := int(jnum(c, "days_remaining"))
	var b sslCertBadge
	switch {
	case jstr(c, "error") != "":
		b.Class, b.Label, b.Error = "is-error", "Unreadable", true
		b.Line = jstr(c, "error")
		return b
	case jbool(c, "expired"):
		b.Class, b.Label = "is-error", "Expired"
	case days <= warnDays:
		b.Class, b.Label = "is-warn", strconv.Itoa(days)+" days left"
	default:
		b.Class, b.Label = "is-ok", strconv.Itoa(days)+" days left"
	}
	issuer := jstr(c, "issuer")
	if m := sslOrgRE.FindStringSubmatch(issuer); m != nil {
		issuer = m[1]
	} else if m := sslCNRE.FindStringSubmatch(issuer); m != nil {
		issuer = m[1]
	}
	if jbool(c, "self_signed") {
		issuer = "self-signed"
	}
	until := jstr(c, "not_after")
	if len(until) > 10 {
		until = until[:10]
	}
	b.Line = issuer + " · until " + until
	return b
}

var sslTabs = []struct{ Key, Label, Href string }{
	{"hosts", "SSL hosts", "/ssl/hosts"}, {"autossl", "AutoSSL", "/ssl/autossl"}, {"certificates", "Certificates", "/ssl/certificates"},
	{"keys", "Keys and CSRs", "/ssl/keys"}, {"tls", "TLS configuration", "/ssl/tls"}, {"services", "Services SSL", "/ssl/services"},
}

// sslBase returns the data every SSL Center page shares, or false once it has
// answered: the section is for server administrators only (SslCenterController::denied).
func (s *Server) sslBase(w http.ResponseWriter, r *http.Request, tab, title string) (map[string]any, bool) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.renderError(w, http.StatusForbidden, "Access denied", "The SSL/TLS section is for server administrators. Each website has its own SSL page.")
		return nil, false
	}
	sess := sessionFrom(r.Context())
	warn, _ := s.settings.Int(r.Context(), "ssl_warning_days", 14)
	data := map[string]any{
		"CSRF": session.CSRFToken(sess), "User": user, "Title": title, "Tab": tab, "Tabs": sslTabs,
		"WarnDays": warn, "Success": "", "Error": "", "AgentError": "",
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	data["CanManage"], _ = s.authorizer().Can(r.Context(), user, "ssl.manage")
	return data, true
}

// sslSitesByName maps each host name (domain and aliases) to its website id.
func (s *Server) sslSitesByName(ctx context.Context, r *http.Request) map[string]int64 {
	out := map[string]int64{}
	repo := hosting.NewWebsiteRepository(s.db)
	sites, err := repo.All(ctx, s.currentServerID(r), userFrom(ctx))
	if err != nil {
		return out
	}
	ids := make([]int64, len(sites))
	for i, w := range sites {
		ids[i] = w.ID
	}
	aliases, _ := repo.AliasesFor(ctx, ids)
	for _, w := range sites {
		out[strings.ToLower(w.Domain)] = w.ID
		for _, a := range aliases[w.ID] {
			out[strings.ToLower(a)] = w.ID
		}
	}
	return out
}

type sslHostRow struct {
	Name, File, State, Redirect, Served string
	HTTPS, Enabled, Covered, HasCert    bool
	Cert                                sslCertBadge
	SiteID                              int64
	Whacp                               bool
}

type sslCertRow struct {
	Name, Source, SourceLabel, Names, Subject, Issuer, Valid, Fingerprint, File, Meta, UsedBy string
	Cert                                                                                      sslCertBadge
	Deletable, KeyReadable                                                                    bool
}

func sslHostState(https, covered bool, c any, warn int) string {
	if !https {
		return "none"
	}
	if !covered || jbool(c, "expired") || jstr(c, "error") != "" || int(jnum(c, "days_remaining")) <= warn {
		return "warn"
	}
	return "ok"
}

// sslAdminOnly answers 403 unless the user is a server administrator: the
// SSL Center is server-wide (SslCenterController::denied).
func (s *Server) sslAdminOnly(w http.ResponseWriter, r *http.Request) bool {
	if hosting.IsAll(userFrom(r.Context())) {
		return true
	}
	s.renderError(w, http.StatusForbidden, "Access denied", "The SSL/TLS section is for server administrators. Each website has its own SSL page.")
	return false
}
