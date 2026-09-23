package sites

import (
	"fmt"
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
	fmt.Fprintf(&b, "pm = ondemand\npm.max_children = %d\npm.process_idle_timeout = 10s\npm.max_requests = 500\n", s.PHP.MaxChildren)
	fmt.Fprintf(&b, "chdir = /\ncatch_workers_output = yes\nslowlog = %s\nrequest_slowlog_timeout = 10s\n", filepath.Join(l.LogDir, "php-slow.log"))
	fmt.Fprintf(&b, "php_admin_value[open_basedir] = %s/:/usr/share/php/:/dev/urandom\n", s.Root)
	fmt.Fprintf(&b, "php_admin_value[upload_tmp_dir] = %s\nphp_admin_value[session.save_path] = %s\nphp_admin_value[sys_temp_dir] = %s\n", l.TmpDir, l.TmpDir, l.TmpDir)
	fmt.Fprintf(&b, "php_admin_value[error_log] = %s\nphp_admin_flag[log_errors] = on\nphp_admin_flag[display_errors] = off\n", filepath.Join(l.LogDir, "php-error.log"))
	fmt.Fprintf(&b, "php_admin_value[memory_limit] = %dM\n", s.PHP.MemoryLimitMB)
	fmt.Fprintf(&b, "php_admin_value[upload_max_filesize] = %dM\nphp_admin_value[post_max_size] = %dM\n", s.PHP.UploadMaxMB, s.PHP.UploadMaxMB)
	fmt.Fprintf(&b, "php_admin_value[max_execution_time] = %d\n", s.PHP.MaxExecSeconds)
	if s.PHP.Harden {
		b.WriteString("php_admin_value[disable_functions] = exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec\n")
	}
	return b.String()
}

// DefaultIndex is written to a new, empty document root.
func DefaultIndex(domain string) string {
	return "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>" + domain +
		"</title></head>\n<body style=\"font-family:system-ui,sans-serif;max-width:40rem;margin:4rem auto;padding:0 1rem\">\n<h1>" + domain +
		"</h1>\n<p>This website is ready. Upload your files to <code>public_html</code>.</p>\n</body></html>\n"
}
