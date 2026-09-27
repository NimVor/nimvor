package sites

import (
	"fmt"
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
	HTTP2Direct bool // nginx >= 1.25.1 uses "http2 on;"
	IPv6        bool // add [::] listeners
}

const header = "Managed by WhaCP. Do not edit: changes are overwritten when the website is saved in the panel."

// RenderNginx produces the server blocks for a site. All values were
// validated by Spec.Normalize, so none can break out of a directive.
func RenderNginx(s Spec, l Layout) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n# Website: %s\n\n", header, s.Domain)
	names := strings.Join(s.Names(), " ")

	// Port 80: either the site itself or a redirect to HTTPS.
	b.WriteString("server {\n    listen 80;\n")
	if l.IPv6 {
		b.WriteString("    listen [::]:80;\n")
	}
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
		fmt.Fprintf(&b, "    listen 443 ssl%s;\n", suffix)
		if l.IPv6 {
			fmt.Fprintf(&b, "    listen [::]:443 ssl%s;\n", suffix)
		}
		if l.HTTP2Direct {
			b.WriteString("    http2 on;\n")
		}
		fmt.Fprintf(&b, "    server_name %s;\n", names)
		fmt.Fprintf(&b, "    ssl_certificate %s;\n    ssl_certificate_key %s;\n", s.CertFile, s.KeyFile)
		b.WriteString("    ssl_protocols TLSv1.2 TLSv1.3;\n    ssl_prefer_server_ciphers off;\n    ssl_session_cache shared:WhaCPSSL:10m;\n    ssl_session_timeout 1d;\n")
		if s.HSTS {
			b.WriteString("    add_header Strict-Transport-Security \"max-age=31536000\" always;\n")
		}
		body(&b, s, l)
		b.WriteString("}\n")
	}
	return b.String()
}

func acme(b *strings.Builder, l Layout) {
	fmt.Fprintf(b, "    location ^~ /.well-known/acme-challenge/ {\n        root %s;\n        default_type \"text/plain\";\n        try_files $uri =404;\n    }\n", l.ACMERoot)
}

func body(b *strings.Builder, s Spec, l Layout) {
	fmt.Fprintf(b, "    root %s;\n", s.DocumentRoot)
	if s.Kind == "php" {
		b.WriteString("    index index.php index.html index.htm;\n")
	} else {
		b.WriteString("    index index.html index.htm;\n")
	}
	fmt.Fprintf(b, "    access_log %s;\n    error_log %s warn;\n", filepath.Join(l.LogDir, "access.log"), filepath.Join(l.LogDir, "error.log"))
	fmt.Fprintf(b, "    client_max_body_size %dm;\n", s.ClientMaxBodyMB)
	b.WriteString("    add_header X-Content-Type-Options \"nosniff\" always;\n")
	acme(b, l)
	b.WriteString("    location ~ /\\.(?!well-known) {\n        deny all;\n    }\n")
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
