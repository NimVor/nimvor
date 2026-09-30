package sites

import (
	"fmt"
	"github.com/mchkhawarshahzad/whacp/agent/internal/tlsprofile"
	"html"
	"path/filepath"
	"strings"
)

// Layout holds the paths a site's configuration refers to.
type Layout struct {
	NginxFile   string
	NginxLink   string
	PoolFile    string
	Socket      string
	LogDir      string
	TmpDir      string
	ACMERoot    string
	WebUser     string
	HTTP2Direct bool   // nginx >= 1.25.1 uses "http2 on;"
	IPv6        bool   // add [::] listeners
	TLSInclude  string // the server-wide TLS snippet's include line
	Slice       string // the account's slice when it has resource limits
	// ModSecInclude is the website's ModSecurity include (a glob that
	// matches nothing while ModSecurity is off for it).
	ModSecInclude string
	// ProtectInclude is the website's protection include (password,
	// hotlink, WordPress rules; a glob that matches nothing without any).
	ProtectInclude string
	// Sendmail is PHP's sendmail_path: WhaCP's sendmail, which submits
	// over SMTP to this machine, where the account's sending limit
	// applies (empty: PHP's default).
	Sendmail string
}

const header = "Managed by WhaCP. Do not edit: changes are overwritten when the website is saved in the panel."

// RenderNginx produces the server blocks for a site. All values were
// validated by Spec.Normalize, so none can break out of a directive.
func RenderNginx(s Spec, l Layout) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n# Website: %s\n\n", header, s.Domain)
	names := strings.Join(s.Names(), " ")

	// Port 80: either the site itself or a redirect to HTTPS.
	b.WriteString("server {\n")
	listen(&b, s, l, "80")
	fmt.Fprintf(&b, "    server_name %s;\n", names)
	if s.HTTPS && s.ForceHTTPS {
		acme(&b, l)
		b.WriteString("    location / {\n        return 301 https://$host$request_uri;\n    }\n}\n")
	} else {
		body(&b, s, l)
		b.WriteString("}\n")
	}

	if s.HTTPS {
		b.WriteString("\nserver {\n")
		suffix := " http2"
		if l.HTTP2Direct {
			suffix = ""
		}
		listen(&b, s, l, "443 ssl"+suffix)
		if l.HTTP2Direct {
			b.WriteString("    http2 on;\n")
		}
		fmt.Fprintf(&b, "    server_name %s;\n", names)
		fmt.Fprintf(&b, "    ssl_certificate %s;\n    ssl_certificate_key %s;\n", s.CertFile, s.KeyFile)
		if l.TLSInclude != "" {
			b.WriteString(l.TLSInclude)
		} else {
			b.WriteString(tlsprofile.LegacyBlock)
		}
		if s.HSTS {
			b.WriteString("    add_header Strict-Transport-Security \"max-age=31536000\" always;\n")
		}
		body(&b, s, l)
		b.WriteString("}\n")
	}
	return b.String()
}

// listen writes the listen lines: the website's own addresses when set,
// otherwise every address (IPv6 only where the server has it).
func listen(b *strings.Builder, s Spec, l Layout, port string) {
	if s.IPv4 != "" {
		fmt.Fprintf(b, "    listen %s:%s;\n", s.IPv4, port)
	} else {
		fmt.Fprintf(b, "    listen %s;\n", port)
	}
	switch {
	case s.IPv6 != "":
		fmt.Fprintf(b, "    listen [%s]:%s;\n", s.IPv6, port)
	case l.IPv6:
		fmt.Fprintf(b, "    listen [::]:%s;\n", port)
	}
}

// SecurityHeaders are the optional headers a website can turn on: safe for
// most applications (no script policy, which breaks many).
const SecurityHeaders = "    add_header Referrer-Policy \"strict-origin-when-cross-origin\" always;\n" +
	"    add_header X-Frame-Options \"SAMEORIGIN\" always;\n" +
	"    add_header Permissions-Policy \"camera=(), microphone=(), geolocation=(), payment=(), usb=()\" always;\n"

func acme(b *strings.Builder, l Layout) {
	fmt.Fprintf(b, "    location ^~ /.well-known/acme-challenge/ {\n        root %s;\n        default_type \"text/plain\";\n        try_files $uri =404;\n    }\n", l.ACMERoot)
}

func body(b *strings.Builder, s Spec, l Layout) {
	fmt.Fprintf(b, "    root %s;\n", s.DocumentRoot)
	// A symbolic link is followed only when it and its target have the
	// same owner: a website cannot link to another website's files (nginx
	// may read those through its group).
	b.WriteString("    disable_symlinks if_not_owner from=$document_root;\n")
	if s.Kind == "php" {
		b.WriteString("    index index.php index.html index.htm;\n")
	} else {
		b.WriteString("    index index.html index.htm;\n")
	}
	fmt.Fprintf(b, "    access_log %s;\n    error_log %s warn;\n", filepath.Join(l.LogDir, "access.log"), filepath.Join(l.LogDir, "error.log"))
	fmt.Fprintf(b, "    client_max_body_size %dm;\n", s.ClientMaxBodyMB)
	if s.Autoindex && s.Kind != "proxy" {
		b.WriteString("    autoindex on;\n")
	}
	b.WriteString("    add_header X-Content-Type-Options \"nosniff\" always;\n")
	if s.SecurityHeaders {
		b.WriteString(SecurityHeaders)
		if s.HTTPS {
			b.WriteString("    add_header Content-Security-Policy \"upgrade-insecure-requests\" always;\n")
		}
	}
	b.WriteString(l.ModSecInclude)
	b.WriteString(l.ProtectInclude)
	acme(b, l)
	b.WriteString("    location ~ /\\.(?!well-known) {\n        deny all;\n    }\n")
	if s.BandwidthExceeded {
		b.WriteString(bandwidthExceeded)
		return
	}
	for _, r := range s.Redirects {
		fmt.Fprintf(b, "    location = %s {\n        return %d %s;\n    }\n", r.From, r.Code, r.To)
	}
	switch s.Kind {
	case "php":
		b.WriteString("    location / {\n        try_files $uri $uri/ /index.php?$query_string;\n    }\n")
		b.WriteString("    location ~ \\.php$ {\n        try_files $uri =404;\n        fastcgi_split_path_info ^(.+\\.php)(/.+)$;\n        include fastcgi_params;\n")
		b.WriteString("        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;\n        fastcgi_param PATH_INFO $fastcgi_path_info;\n")
		fmt.Fprintf(b, "        fastcgi_pass unix:%s;\n        fastcgi_read_timeout %ds;\n    }\n", l.Socket, s.PHP.MaxExecSeconds+10)
	case "static":
		b.WriteString("    location / {\n        try_files $uri $uri/ =404;\n    }\n")
	case "proxy":
		fmt.Fprintf(b, "    location / {\n        proxy_pass %s;\n", s.ProxyPass)
		b.WriteString("        proxy_http_version 1.1;\n        proxy_set_header Host $host;\n        proxy_set_header X-Real-IP $remote_addr;\n")
		b.WriteString("        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n        proxy_set_header X-Forwarded-Proto $scheme;\n")
		b.WriteString("        proxy_set_header Upgrade $http_upgrade;\n        proxy_set_header Connection $connection_upgrade_whacp;\n        proxy_read_timeout 300s;\n    }\n")
	}
}

// bandwidthExceeded answers every request of a website whose account has
// used its monthly data transfer (the text has no quote or "$").
const bandwidthExceeded = "    location / {\n        default_type text/html;\n        add_header Cache-Control \"no-store\" always;\n" +
	"        return 509 \"<!doctype html><html lang='en'><head><meta charset='utf-8'><title>Bandwidth limit exceeded</title></head>" +
	"<body style='font-family:sans-serif;max-width:40em;margin:4em auto;padding:0 1em'><h1>Bandwidth limit exceeded</h1>" +
	"<p>This website has used its monthly data transfer. It is available again at the start of next month, or earlier when its hosting plan allows more.</p></body></html>\";\n    }\n"

// RenderProxyMap is included once (whacp-00-proxy-map.conf) so proxied
// sites can upgrade WebSocket connections.
func RenderProxyMap() string {
	return "# " + header + "\nmap $http_upgrade $connection_upgrade_whacp {\n    default upgrade;\n    ''      close;\n}\n"
}

// RenderPool produces the PHP-FPM pool for a site. The pool runs as the
// site user, is confined with open_basedir and keeps temp files inside the
// site.
func RenderPool(s Spec, l Layout) string {
	var b strings.Builder
	fmt.Fprintf(&b, "; %s\n; Website: %s\n\n[whacp-%s]\n", header, s.Domain, s.Domain)
	fmt.Fprintf(&b, "user = %s\ngroup = %s\n", s.User, s.User)
	fmt.Fprintf(&b, "listen = %s\nlisten.owner = %s\nlisten.group = %s\nlisten.mode = 0660\n", l.Socket, l.WebUser, l.WebUser)
	p := s.PHP
	fmt.Fprintf(&b, "pm = %s\npm.max_children = %d\n", p.PM, p.MaxChildren)
	switch p.PM {
	case "dynamic":
		fmt.Fprintf(&b, "pm.start_servers = %d\npm.min_spare_servers = %d\npm.max_spare_servers = %d\n", p.StartServers, p.MinSpare, p.MaxSpare)
	case "ondemand":
		fmt.Fprintf(&b, "pm.process_idle_timeout = %ds\n", p.IdleTimeoutSec)
	}
	fmt.Fprintf(&b, "pm.max_requests = %d\n", p.MaxRequests)
	if p.RequestTimeoutSec > 0 {
		fmt.Fprintf(&b, "request_terminate_timeout = %ds\n", p.RequestTimeoutSec)
	}
	fmt.Fprintf(&b, "chdir = /\ncatch_workers_output = yes\nslowlog = %s\nrequest_slowlog_timeout = %ds\n", filepath.Join(l.LogDir, "php-slow.log"), p.SlowlogSec)
	// The website's confinement, then WhaCP's defaults; the website's own
	// directives (validated in Normalize) replace defaults they may change.
	defaults := []iniLine{
		{"open_basedir", s.Root + "/:/usr/share/php/:/dev/urandom", true},
		{"upload_tmp_dir", l.TmpDir, true},
		{"session.save_path", l.TmpDir, true},
		{"sys_temp_dir", l.TmpDir, true},
		{"error_log", filepath.Join(l.LogDir, "php-error.log"), true},
		{"log_errors", "On", true},
		{"display_errors", "Off", true},
		{"memory_limit", fmt.Sprintf("%dM", s.PHP.MemoryLimitMB), true},
		{"upload_max_filesize", fmt.Sprintf("%dM", s.PHP.UploadMaxMB), true},
		{"post_max_size", fmt.Sprintf("%dM", s.PHP.UploadMaxMB), true},
		{"max_execution_time", fmt.Sprint(s.PHP.MaxExecSeconds), true},
	}
	// Safer session cookies. They are plain (not admin) values, so the
	// website's own directives or ini_set() can still change them.
	defaults = append(defaults,
		iniLine{"session.cookie_httponly", "On", false},
		iniLine{"session.use_strict_mode", "On", false},
		iniLine{"session.use_only_cookies", "On", false},
	)
	if s.HTTPS && s.ForceHTTPS {
		// Only when every request is HTTPS: otherwise visitors on plain
		// HTTP would lose their session.
		defaults = append(defaults, iniLine{"session.cookie_secure", "On", false})
	}
	if l.Sendmail != "" {
		defaults = append(defaults, iniLine{"sendmail_path", l.Sendmail, true})
	}
	if s.PHP.Harden {
		defaults = append(defaults, iniLine{"disable_functions", "exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec", true})
	}
	b.WriteString(poolINI(defaults, s.PHP.INI))
	return b.String()
}

// DefaultIndex is written to a new, empty document root: a "coming soon"
// page (Bootstrap from jsDelivr, pinned with its integrity hash; without
// it the page still reads well). The domain is HTML-escaped.
func DefaultIndex(domain string) string {
	d := html.EscapeString(domain)
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + d + ` — coming soon</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css" integrity="sha384-QWTKZyjpPEjISv5WaRU9OFeRpok6YctnYmDr5pNlyT2bRjXh0JMhjY6hW+ALEwIH" crossorigin="anonymous">
<style>
body{min-height:100vh;margin:0;font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;background:linear-gradient(135deg,#0f766e 0%,#1e3a8a 100%);color:#fff}
.soon-card{max-width:40rem;background:rgba(255,255,255,.08);border:1px solid rgba(255,255,255,.18);border-radius:1.25rem;backdrop-filter:blur(6px)}
.soon-dot{display:inline-block;width:.6rem;height:.6rem;border-radius:50%;background:#5eead4;box-shadow:0 0 0 .35rem rgba(94,234,212,.25)}
</style>
</head>
<body class="d-flex align-items-center justify-content-center p-3">
<main class="soon-card text-center p-4 p-md-5">
<p class="text-uppercase small mb-3" style="letter-spacing:.2em"><span class="soon-dot me-2"></span>Coming soon</p>
<h1 class="display-5 fw-bold mb-3 text-break">` + d + `</h1>
<p class="lead mb-4" style="opacity:.85">We are working on something new. Please check back soon.</p>
<hr class="my-4" style="border-color:rgba(255,255,255,.3)">
<p class="small mb-0" style="opacity:.7">Website owner: upload your site to <code class="text-white">public_html</code> to replace this page.</p>
</main>
</body>
</html>
`
}

// PlaceholderIndex reports whether b is a placeholder page WhaCP wrote
// for the domain (the current one or the one of earlier versions).
func PlaceholderIndex(b []byte, domain string) bool {
	legacy := "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>" + domain +
		"</title></head>\n<body style=\"font-family:system-ui,sans-serif;max-width:40rem;margin:4rem auto;padding:0 1rem\">\n<h1>" + domain +
		"</h1>\n<p>This website is ready. Upload your files to <code>public_html</code>.</p>\n</body></html>\n"
	return string(b) == DefaultIndex(domain) || string(b) == legacy
}
