package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// PanelVersion is set by the binary's build (-ldflags "-X main.version=…").
var PanelVersion = "dev"

// settingField is one setting as a form control.
type settingField struct {
	Key, Label, Help, Type, Value, Error, Default, DefaultShown, Search, Own, OwnURL string
	Options                                                                          []settings.Option
	Min, Max, MaxLen                                                                 int
	Checked, Required, Changed, IsOwn, IsSelect, IsNumber, IsURL, IsBool, HasOwnLink bool
}

// settingTabs are the Tweak Settings tabs (WebHostSettingsController::TABS).
var settingTabs = []struct{ ID, Label, Icon string }{
	{"general", "General", "sliders"}, {"accounts", "Accounts and websites", "person-gear"}, {"dns", "DNS", "signpost"},
	{"ssl", "SSL/TLS", "shield-lock"}, {"monitoring", "Monitoring", "activity"}, {"security", "Security", "shield-check"},
	{"policy", "Security policies", "person-lock"}, {"hulk", "Brute force protection", "shield-exclamation"},
}

// settingOwnPage: settings kept on their own page (turning them on does more than store a value).
var settingOwnPage = map[string]string{
	"hulk_enabled": "security.hulk", "policy_verified_ips": "security.policies", "hulk_whitelist": "security.hulk",
	"hulk_synced": "-", "hulk_geo_updated": "-", "hulk_geo_tried": "-",
}

// settingsHidden are internal state keys that are not edited by hand on the Settings page.
var settingsHidden = map[string]bool{"php_profile": true, "server_setup": true, "setup_wizard_seen": true, "setup_wizard_done": true, "components_expected": true}

func flashJSON(sess *session.Session, key string) map[string]string {
	out := map[string]string{}
	if v, ok := sess.PullFlash(key); ok {
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

func (s *Server) backWithErrors(w http.ResponseWriter, r *http.Request, to string, errs map[string]string) {
	sess := sessionFrom(r.Context())
	old := map[string]string{}
	for k, v := range r.PostForm {
		if k == "_token" || strings.Contains(k, "password") || strings.Contains(k, "secret") || len(v) == 0 {
			continue
		}
		old[k] = v[0]
	}
	eb, _ := json.Marshal(errs)
	ob, _ := json.Marshal(old)
	sess.Flash("errors", string(eb))
	sess.Flash("old", string(ob))
	var first []string
	for _, v := range errs {
		first = append(first, v)
	}
	sort.Strings(first)
	sess.Flash("error", "Please correct the highlighted settings: "+first[0])
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func settingDefaultString(d *settings.Def) string {
	switch x := d.Default.(type) {
	case bool:
		if x {
			return "1"
		}
		return "0"
	case int:
		return strconv.Itoa(x)
	case string:
		return x
	}
	return ""
}

func (s *Server) plansForSelect(ctx context.Context, r *http.Request) []settings.Option {
	out := []settings.Option{{Value: "0", Label: "None (unlimited)"}}
	if plans, err := hosting.NewPlanRepository(s.db).Visible(ctx, userFrom(ctx)); err == nil {
		for _, p := range plans {
			out = append(out, settings.Option{Value: strconv.FormatInt(p.ID, 10), Label: p.Name})
		}
	}
	return out
}

func (s *Server) settingField(d *settings.Def, values, old, errs map[string]string, plans []settings.Option) settingField {
	shown := values[d.Key]
	if v, ok := old[d.Key]; ok {
		shown = v
	}
	f := settingField{Key: d.Key, Label: d.Label, Help: d.Help, Type: d.Type, Value: shown, Error: errs[d.Key], Default: settingDefaultString(d),
		Options: d.Options, Min: d.Min, Max: d.Max, MaxLen: d.MaxLength, Required: !d.Optional && d.Type != "bool" && d.Type != "url" && len(d.Options) == 0,
		IsBool: d.Type == "bool", IsNumber: d.Type == "int", IsURL: d.Type == "url"}
	if f.MaxLen == 0 {
		f.MaxLen = 255
	}
	f.Changed = values[d.Key] != f.Default
	f.Checked = shown == "1"
	if d.Key == "default_plan_id" {
		f.Options, f.IsSelect, f.IsNumber = plans, true, false
	} else if len(d.Options) > 0 {
		f.IsSelect = true
	}
	switch {
	case d.Type == "bool":
		f.DefaultShown = map[bool]string{true: "On", false: "Off"}[f.Default == "1"]
	case d.Key == "default_plan_id":
		f.DefaultShown = "None"
		for _, o := range plans {
			if o.Value == f.Default {
				f.DefaultShown = o.Label
			}
		}
	case len(d.Options) > 0:
		f.DefaultShown = f.Default
		for _, o := range d.Options {
			if o.Value == f.Default {
				f.DefaultShown = o.Label
			}
		}
	default:
		f.DefaultShown = f.Default
		if f.Default == "" {
			f.DefaultShown = "(empty)"
		}
	}
	f.Search = strings.ToLower(d.Label + " " + d.Help + " " + d.Key)
	return f
}

// mailConfig is MailConfig::get.
type mailConfig struct {
	Host, Encryption, Username, From, AlertTo string
	Port                                      int
	HasPassword                               bool
}

func (s *Server) mailConfig(ctx context.Context) mailConfig {
	str := func(k, d string) string { v, _ := s.settings.String(ctx, "mail."+k, d); return v }
	port, _ := s.settings.Int(ctx, "mail.port", 587)
	return mailConfig{Host: str("host", ""), Port: port, Encryption: str("encryption", "starttls"), Username: str("username", ""),
		HasPassword: str("password", "") != "", From: str("from", ""), AlertTo: str("alert_to", "")}
}

var mailHostRE = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

func validAddress(a string) bool {
	if len(a) > 254 || strings.ContainsAny(a, "\r\n<>") {
		return false
	}
	p, err := mail.ParseAddress(a)
	return err == nil && p.Address == a
}

func splitAddresses(list string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// saveMailConfig validates and stores the mail.* settings (MailConfig::save);
// the password is stored encrypted with the panel key, under the same keys
// the mailer reads. A blank password keeps the stored one.
func (s *Server) saveMailConfig(ctx context.Context, in map[string]string, actor int64) map[string]string {
	errs := map[string]string{}
	t := func(k string) string { return strings.TrimSpace(in[k]) }
	host := strings.ToLower(t("host"))
	port, perr := strconv.Atoi(t("port"))
	enc := t("encryption")
	if host != "" && !mailHostRE.MatchString(host) && !strings.Contains(host, ":") && !regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`).MatchString(host) {
		errs["host"] = "Enter a host name or IP address."
	}
	if perr != nil || port < 1 || port > 65535 {
		errs["port"] = "Enter a port between 1 and 65535."
	}
	switch enc {
	case "tls", "starttls":
	case "none":
		if host != "" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
			errs["encryption"] = "Unencrypted SMTP is only allowed to a relay on this server."
		}
	default:
		errs["encryption"] = "Choose an encryption mode."
	}
	ctrl := regexp.MustCompile(`[\x00-\x1F\x7F]`)
	if len([]rune(t("username"))) > 200 || ctrl.MatchString(t("username")+in["password"]) {
		errs["username"] = "The user name or password contains invalid characters."
	}
	if host != "" && !validAddress(t("from")) {
		errs["from"] = "Enter the sender address, e.g. panel@example.com."
	}
	addrs := splitAddresses(t("alert_to"))
	for _, a := range addrs {
		if !validAddress(a) {
			errs["alert_to"] = "\"" + a + "\" is not a valid email address."
		}
	}
	if len(errs) > 0 {
		return errs
	}
	set := func(k string, v any) {
		if err := s.settings.Set(ctx, "mail."+k, v, actor); err != nil {
			s.log.Error("save mail setting", "key", k, "error", err)
		}
	}
	set("host", host)
	set("port", port)
	set("encryption", enc)
	set("username", t("username"))
	set("from", t("from"))
	set("alert_to", strings.Join(addrs, ", "))
	if in["clear_password"] == "1" {
		set("password", "")
	} else if pw := in["password"]; pw != "" {
		if sealed, err := s.crypto.Encrypt([]byte(pw)); err == nil {
			set("password", sealed)
		} else {
			errs["password"] = "The password could not be stored."
			return errs
		}
	}
	return nil
}

type settingsCategory struct {
	ID, Label, Icon string
	Fields          []settingField
	Links           []settingsLink
	IsEmail, IsSys  bool
	Active          bool
}
type settingsLink struct{ URL, Text string }

var settingsCategories = []struct {
	ID, Label, Icon string
	Links           [][3]string // route, url, text
}{
	{"general", "General", "sliders", nil},
	{"server", "Server", "hdd-rack", [][3]string{{"server", "/server", "Host name, time zone, root password, restart"}, {"services", "/services", "Services"}, {"ips", "/ips", "IP addresses and IPv6 ranges"}, {"packages", "/packages", "Packages and updates"}, {"ffmpeg", "/ffmpeg", "FFmpeg"}, {"servers", "/servers", "All servers"}}},
	{"security", "Security", "shield-check", [][3]string{{"security", "/security", "Security overview"}, {"security.firewall", "/security/firewall", "Firewall"}, {"security.ssh", "/security/ssh", "SSH"}, {"security.shell", "/security/shell", "Shell access"}, {"security.malware", "/security/malware", "Malware scans"}, {"security.policies", "/security/policies", "Security policies (verified IPs, two-factor, password age)"}, {"security.password_strength", "/security/password-strength", "Password strength"}, {"security.compilers", "/security/compilers", "Compiler access"}, {"security.smtp", "/security/smtp", "SMTP restrictions"}}},
	{"websites", "Websites and PHP", "globe", [][3]string{{"websites", "/websites", "Websites"}, {"php", "/php", "PHP versions, extensions, php.ini and PHP-FPM"}}},
	{"email", "Email", "envelope", [][3]string{{"mail", "/mail", "Email hosting"}, {"mail.admin.server", "/mail/admin/server", "Mail server (Postfix, Dovecot)"}, {"mail.greylist", "/mail/admin/greylisting", "Greylisting"}, {"mail.limits", "/mail/admin/sending-limits", "Mail sending limits"}}},
	{"dns", "DNS", "signpost", [][3]string{{"dns", "/dns", "DNS zones"}}},
	{"ssl", "SSL/TLS", "shield-lock", [][3]string{{"ssl.autossl", "/ssl/autossl", "AutoSSL: run, logs, renewals"}, {"ssl.hosts", "/ssl/hosts", "SSL hosts"}, {"ssl.certificates", "/ssl/certificates", "Certificate store"}, {"ssl.keys", "/ssl/keys", "Private keys and CSRs"}, {"ssl.tls", "/ssl/tls", "TLS configuration"}}},
	{"databases", "Databases", "database", [][3]string{{"databases", "/databases", "Databases and users"}, {"databases.server", "/databases/server", "Database server (MariaDB / MySQL)"}}},
	{"backups", "Backups", "archive", [][3]string{{"backups", "/backups", "Backups, schedules and destinations"}}},
	{"monitoring", "Monitoring and alerts", "activity", [][3]string{{"monitoring", "/monitoring", "Monitoring"}, {"notifications", "/notifications", "Notifications"}}},
	{"limits", "Resource limits", "speedometer", [][3]string{{"plans", "/plans", "Hosting plans"}, {"limits", "/limits", "CPU and memory limits (cgroups)"}, {"features", "/features", "Feature lists"}}},
	{"integrations", "Integrations", "plug", [][3]string{{"plugins", "/admin/plugins", "Plugins"}, {"profile", "/profile", "API tokens (your profile)"}}},
	{"system", "System", "info-circle", [][3]string{{"audit", "/audit", "Audit log"}, {"users", "/users", "Users and roles"}}},
}

// settingsIndex GET /settings (SettingsController::index, settings.view).
func (s *Server) settingsIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess := sessionFrom(ctx)
	user := userFrom(ctx)
	values, err := s.settings.Values(ctx)
	if err != nil {
		s.log.Error("load settings", "error", err)
		values = map[string]string{}
	}
	errs, old := flashJSON(sess, "errors"), flashJSON(sess, "old")
	data := map[string]any{"CSRF": session.CSRFToken(sess), "User": user, "Title": "Settings", "Success": "", "Error": ""}
	if v, ok := sess.PullFlash("success"); ok {
		data["Success"] = v
	}
	if v, ok := sess.PullFlash("error"); ok {
		data["Error"] = v
	}
	canEdit := s.can(r, "settings.manage")
	plans := s.plansForSelect(ctx, r)
	az := s.authorizer()
	first := "general"
	var cats []settingsCategory
	for _, c := range settingsCategories {
		cat := settingsCategory{ID: c.ID, Label: c.Label, Icon: c.Icon, IsEmail: c.ID == "email", IsSys: c.ID == "system"}
		for i := range settings.Definitions {
			d := &settings.Definitions[i]
			if d.Category == c.ID && !settingsHidden[d.Key] {
				f := s.settingField(d, values, old, errs, plans)
				cat.Fields = append(cat.Fields, f)
				if f.Error != "" && first == "general" && c.ID != "general" {
					first = c.ID
				}
			}
		}
		for _, l := range c.Links {
			if ok, err := az.RouteAllowed(ctx, user, l[0]); err == nil && ok {
				cat.Links = append(cat.Links, settingsLink{URL: l[1], Text: l[2]})
			}
		}
		cats = append(cats, cat)
	}
	for i := range cats {
		cats[i].Active = cats[i].ID == first
	}
	mc := s.mailConfig(ctx)
	mv := func(k, def string) string {
		if v, ok := old[k]; ok {
			return v
		}
		return def
	}
	data["Cats"], data["CanEdit"] = cats, canEdit
	data["Mail"] = map[string]any{"Host": mv("host", mc.Host), "Port": mv("port", strconv.Itoa(mc.Port)), "Encryption": mv("encryption", mc.Encryption), "Username": mv("username", mc.Username),
		"From": mv("from", mc.From), "AlertTo": mv("alert_to", mc.AlertTo), "HasPassword": mc.HasPassword, "HostSet": mc.Host != "",
		"Errors": errs}
	data["System"] = []settingsLink{{"Panel version", PanelVersion}, {"Go version", runtime.Version()}, {"Agent socket", s.cfg.AgentSocket}, {"Database", "SQLite"},
		{"Secure session cookie", map[bool]string{true: "Yes", false: "No (development only)"}[s.cfg.Production]}}
	s.render(w, "settings_index", data)
}

func postMap(r *http.Request) map[string]string {
	m := map[string]string{}
	for k, v := range r.PostForm {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}

// settingsUpdate POST /settings (SettingsController::update).
func (s *Server) settingsUpdate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	in := postMap(r)
	to := "/settings" + hashFor(in["tab"])
	delete(in, "tab")
	changed, errs, err := s.settings.Save(r.Context(), in, user.ID)
	if err != nil {
		sess.Flash("error", "The settings could not be saved.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	if len(errs) > 0 {
		s.backWithErrors(w, r, to, errs)
		return
	}
	if len(changed) > 0 {
		uid := user.ID
		s.audit.Record(r.Context(), audit.Entry{Action: "settings.update", TargetType: "settings", TargetID: "panel", Result: audit.Success,
			Details: map[string]any{"changed": changed}, UserID: &uid, Username: user.Username})
	}
	sess.Flash("success", "Settings saved.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// settingsMail POST /settings/mail (SettingsController::mail).
func (s *Server) settingsMail(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	in := postMap(r)
	if errs := s.saveMailConfig(r.Context(), in, user.ID); errs != nil {
		s.backWithErrors(w, r, "/settings#email", errs)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{Action: "settings.mail", TargetType: "settings", TargetID: "mail", Result: audit.Success,
		Details: map[string]any{"host": strings.ToLower(strings.TrimSpace(in["host"])), "password_changed": in["password"] != "" || in["clear_password"] == "1"}, UserID: &uid, Username: user.Username})
	sess.Flash("success", "Email settings saved. Send a test message to check them.")
	http.Redirect(w, r, "/settings#email", http.StatusSeeOther)
}

// --- Basic WebHost Settings -------------------------------------------------

const nameServerFields = 4

var nsRE = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// settingsBasic GET /settings/basic (WebHostSettingsController::basic).
func (s *Server) settingsBasic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess := sessionFrom(ctx)
	user := userFrom(ctx)
	values, _ := s.settings.Values(ctx)
	errs, old := flashJSON(sess, "errors"), flashJSON(sess, "old")
	data := map[string]any{"CSRF": session.CSRFToken(sess), "User": user, "Title": "Basic WebHost Settings", "Success": "", "Error": "",
		"CanEdit": s.can(r, "settings.manage"), "Hostname": "", "Addresses": []string{}}
	if v, ok := sess.PullFlash("success"); ok {
		data["Success"] = v
	}
	if v, ok := sess.PullFlash("error"); ok {
		data["Error"] = v
	}
	if raw, err := s.agent.Call(ctx, "system.info", nil, 30*time.Second); err == nil {
		var info any
		_ = json.Unmarshal(raw, &info)
		data["Hostname"] = jstr(info, "system", "hostname")
		var addrs []string
		ifs, _ := jmap(jmap(info)["system"])["interfaces"].([]any)
		for _, ifc := range ifs {
			for _, a := range strList(jmap(ifc)["addresses"]) {
				if i := strings.Index(a, "/"); i >= 0 {
					a = a[:i]
				}
				if isPublicIPv4(a) {
					addrs = append(addrs, a)
				}
			}
		}
		data["Addresses"] = dedup(addrs)
	}
	field := func(k string) settingField {
		d, _ := settings.Lookup(k)
		return s.settingField(d, values, old, errs, nil)
	}
	fields := map[string]settingField{}
	for _, k := range []string{"panel_name", "panel_url", "dns_default_ipv4", "dns_hostmaster", "default_plan_id", "default_php_version", "account_dns_dkim", "account_dns_spf", "account_dns_dmarc", "account_spam_filter", "account_spam_box", "account_mail_routing"} {
		fields[k] = field(k)
	}
	plans := s.plansForSelect(ctx, r)
	pf := fields["default_plan_id"]
	pf.Options = plans
	fields["default_plan_id"] = pf
	var ns []map[string]string
	list := strings.FieldsFunc(values["dns_nameservers"], func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	for i := 0; i < nameServerFields; i++ {
		name := "ns" + strconv.Itoa(i+1)
		v := ""
		if i < len(list) {
			v = list[i]
		}
		if o, ok := old[name]; ok {
			v = o
		}
		ns = append(ns, map[string]string{"Name": name, "Label": "Name server " + strconv.Itoa(i+1), "Value": v, "Error": errs[name]})
	}
	versions := s.websiteService(r, user).PhpVersions(ctx)
	cur := fields["default_php_version"].Value
	seen := map[string]bool{}
	type pv struct {
		V, Label string
		Selected bool
	}
	var pvs []pv
	for _, v := range append(append([]string{}, versions...), cur) {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		label := "PHP " + v
		if !hasString(versions, v) {
			label += " (not installed)"
		}
		pvs = append(pvs, pv{v, label, v == cur})
	}
	mc := s.mailConfig(ctx)
	alertTo := mc.AlertTo
	if o, ok := old["alert_to"]; ok {
		alertTo = o
	}
	data["F"], data["NS"], data["PhpVersions"] = fields, ns, pvs
	data["Mail"] = map[string]any{"AlertTo": alertTo, "Host": mc.Host, "Error": errs["alert_to"]}
	data["Errors"] = errs
	s.render(w, "settings_basic", data)
}

// settingsBasicSave POST /settings/basic (WebHostSettingsController::saveBasic).
func (s *Server) settingsBasicSave(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	in := postMap(r)
	tab := in["tab"]
	if tab == "" {
		tab = "contact"
	}
	back := "/settings/basic"
	if regexp.MustCompile(`^[a-z]{2,20}$`).MatchString(tab) {
		back += "#" + tab
	}
	pick := func(keys ...string) map[string]string {
		m := map[string]string{}
		for _, k := range keys {
			if v, ok := in[k]; ok {
				m[k] = v
			}
		}
		return m
	}
	var toSave map[string]string
	switch tab {
	case "contact":
		mc := s.mailConfig(r.Context())
		if errs := s.saveMailConfig(r.Context(), map[string]string{"host": mc.Host, "port": strconv.Itoa(mc.Port), "encryption": mc.Encryption, "username": mc.Username, "password": "", "from": mc.From, "alert_to": in["alert_to"]}, user.ID); errs != nil {
			s.backWithErrors(w, r, back, errs)
			return
		}
	case "basic":
		toSave = pick("panel_name", "panel_url", "dns_default_ipv4", "dns_hostmaster")
	case "nameservers":
		errs := map[string]string{}
		var list []string
		for i := 1; i <= nameServerFields; i++ {
			ns := strings.ToLower(strings.TrimRight(strings.TrimSpace(in["ns"+strconv.Itoa(i)]), "."))
			if ns == "" {
				continue
			}
			if len(ns) < 4 || len(ns) > 253 || !nsRE.MatchString(ns) {
				errs["ns"+strconv.Itoa(i)] = "Enter a host name such as ns" + strconv.Itoa(i) + ".example.com."
			}
			list = append(list, ns)
		}
		if len(errs) > 0 {
			s.backWithErrors(w, r, back, errs)
			return
		}
		toSave = map[string]string{"dns_nameservers": strings.Join(dedup(list), ", ")}
	case "accounts":
		toSave = pick("default_plan_id", "default_php_version", "account_dns_dkim", "account_dns_spf", "account_dns_dmarc", "account_spam_filter", "account_spam_box", "account_mail_routing")
	default:
		sess.Flash("error", "Unknown section.")
		http.Redirect(w, r, "/settings/basic", http.StatusSeeOther)
		return
	}
	if toSave != nil {
		changed, errs, err := s.settings.Save(r.Context(), toSave, user.ID)
		if err != nil {
			sess.Flash("error", "The settings could not be saved.")
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		if len(errs) > 0 {
			s.backWithErrors(w, r, back, errs)
			return
		}
		if len(changed) > 0 {
			uid := user.ID
			s.audit.Record(r.Context(), audit.Entry{Action: "settings.update", TargetType: "settings", TargetID: "panel", Result: audit.Success,
				Details: map[string]any{"changed": changed}, UserID: &uid, Username: user.Username})
		}
	}
	sess.Flash("success", "Saved.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// --- Tweak Settings ---------------------------------------------------------

type tweakTab struct {
	ID, Label, Icon string
	Fields          []settingField
	Count, Changed  int
	Can, Active     bool
	OnlyAdmins      bool
}

func (s *Server) mayEditSettingsTab(r *http.Request, tab string) bool {
	if !s.can(r, "settings.manage") {
		return false
	}
	if tab == "policy" || tab == "hulk" {
		u := userFrom(r.Context())
		return hosting.IsAll(u) && u.RoleName == "admin" && s.can(r, "security.manage")
	}
	return true
}

// settingsTweak GET /settings/tweak (WebHostSettingsController::tweak).
func (s *Server) settingsTweak(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess := sessionFrom(ctx)
	user := userFrom(ctx)
	values, _ := s.settings.Values(ctx)
	errs, old := flashJSON(sess, "errors"), flashJSON(sess, "old")
	data := map[string]any{"CSRF": session.CSRFToken(sess), "User": user, "Title": "Tweak Settings", "Success": "", "Error": ""}
	if v, ok := sess.PullFlash("success"); ok {
		data["Success"] = v
	}
	if v, ok := sess.PullFlash("error"); ok {
		data["Error"] = v
	}
	plans := s.plansForSelect(ctx, r)
	az := s.authorizer()
	var tabs []tweakTab
	total := 0
	first := ""
	for _, t := range settingTabs {
		tb := tweakTab{ID: t.ID, Label: t.Label, Icon: t.Icon, Can: s.mayEditSettingsTab(r, t.ID), OnlyAdmins: t.ID == "policy" || t.ID == "hulk"}
		for i := range settings.Definitions {
			d := &settings.Definitions[i]
			if d.Category != t.ID {
				continue
			}
			if own, ok := settingOwnPage[d.Key]; ok && own == "-" {
				continue
			}
			f := s.settingField(d, values, old, errs, plans)
			f.Search += " " + strings.ToLower(t.Label)
			if own, ok := settingOwnPage[d.Key]; ok {
				f.IsOwn = true
				if ok2, err := az.RouteAllowed(ctx, user, own); err == nil && ok2 {
					f.HasOwnLink, f.OwnURL = true, routeURL(own)
				}
			}
			if f.Changed {
				tb.Changed++
				total++
			}
			if f.Error != "" && first == "" {
				first = t.ID
			}
			tb.Fields = append(tb.Fields, f)
		}
		tb.Count = len(tb.Fields)
		tabs = append(tabs, tb)
	}
	if first == "" {
		first = tabs[0].ID
	}
	for i := range tabs {
		tabs[i].Active = tabs[i].ID == first
	}
	data["Tabs"], data["ChangedTotal"] = tabs, total
	s.render(w, "settings_tweak", data)
}

// settingsTweakSave POST /settings/tweak: saves one tab, or resets one
// setting (reset=key) or the whole tab (reset=all).
func (s *Server) settingsTweakSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess := sessionFrom(ctx)
	user := userFrom(ctx)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	in := postMap(r)
	tab := in["tab"]
	var keys []string
	labels := map[string]string{}
	known := false
	for _, t := range settingTabs {
		known = known || t.ID == tab
	}
	if !known {
		sess.Flash("error", "Unknown tab.")
		http.Redirect(w, r, "/settings/tweak", http.StatusSeeOther)
		return
	}
	for i := range settings.Definitions {
		d := &settings.Definitions[i]
		if _, own := settingOwnPage[d.Key]; d.Category == tab && !own {
			keys = append(keys, d.Key)
			labels[d.Key] = d.Label
		}
	}
	back := "/settings/tweak#" + tab
	if !s.mayEditSettingsTab(r, tab) {
		msg := "You may not change these settings."
		if tab == "policy" || tab == "hulk" {
			msg = "You may not change these settings: only administrators manage security policies and brute force protection."
		}
		sess.Flash("error", msg)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if tab == "policy" && !s.auth.IsConfirmed(sess) {
		sess.Flash("error", "Confirm your password to continue, then submit again.")
		http.Redirect(w, r, "/confirm-password?return="+url.QueryEscape("/settings/tweak#policy"), http.StatusSeeOther)
		return
	}
	reset := in["reset"]
	input := map[string]string{}
	if reset != "" {
		var which []string
		if reset == "all" {
			which = keys
		} else if hasString(keys, reset) {
			which = []string{reset}
		}
		if len(which) == 0 {
			sess.Flash("error", "Unknown setting.")
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		for _, k := range which {
			d, _ := settings.Lookup(k)
			input[k] = settingDefaultString(d)
		}
	} else {
		for _, k := range keys {
			if v, ok := in[k]; ok {
				input[k] = v
			}
		}
	}
	changed, errs, err := s.settings.Save(ctx, input, user.ID)
	if err != nil {
		sess.Flash("error", "The settings could not be saved.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if len(errs) > 0 {
		s.backWithErrors(w, r, back, errs)
		return
	}
	note := ""
	var names []string
	for k := range changed {
		names = append(names, labels[k])
	}
	sort.Strings(names)
	if tab == "hulk" && len(changed) > 0 {
		if err := s.hulkSync(ctx, s.hulkService()); err != nil {
			note = " The brute force protection rules were not applied yet (they are retried): " + err.Error()
		}
	}
	uid := user.ID
	if len(changed) > 0 {
		action := "settings.update"
		if reset != "" {
			action = "settings.reset"
		}
		s.audit.Record(ctx, audit.Entry{Action: action, TargetType: "settings", TargetID: "panel", Result: audit.Success,
			Details: map[string]any{"tab": tab, "changed": changed}, UserID: &uid, Username: user.Username})
	}
	msg := "Nothing changed."
	if len(changed) > 0 {
		prefix := "Saved: "
		if reset != "" {
			prefix = "Reset to the default: "
		}
		msg = prefix + strings.Join(names, ", ") + "."
	}
	sess.Flash("success", msg+note)
	http.Redirect(w, r, back, http.StatusSeeOther)
}
