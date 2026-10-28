// Package apps installs applications into websites: WordPress (downloaded
// over HTTPS and verified against the published SHA-1) and Laravel (via
// Composer).
//
// Only the download runs as root, into the agent's own state directory.
// Extracting, writing configuration, Composer and PHP all run as the
// website account. WordPress is installed immediately from the command
// line so its web installer can never be claimed by someone else.
package apps

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/sites"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Sources says where applications are downloaded from.
type Sources struct {
	WordPressURL string
	Client       *http.Client
	// CatalogBase, when set, overrides every staged app's download URL with
	// CatalogBase/<app>.<ext> (and its checksum with CatalogBase/<app>.sum).
	// Used by tests to serve archives locally.
	CatalogBase string
	// GiteaDL and GiteaGitHub are the two places Gitea publishes a release
	// and its checksum (a download server and the GitHub release assets).
	GiteaDL     string
	GiteaGitHub string
}

// DefaultSources are the official download locations.
var DefaultSources = Sources{
	WordPressURL: "https://wordpress.org/latest.tar.gz", Client: &http.Client{Timeout: 15 * time.Minute},
	GiteaDL: "https://dl.gitea.com/gitea", GiteaGitHub: "https://github.com/go-gitea/gitea/releases/download",
}

const maxDownload = 512 << 20

// userAgent identifies NimVor. A bare product name keeps SourceForge (Dolibarr)
// answering with the file instead of a web page for browsers.
const userAgent = "NimVor"

// DB holds database credentials for the application.
type DB struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
	Host     string `json:"host"`
}

// AppDef describes a "staged" application: one that NimVor downloads, verifies
// and unpacks into the website, leaving its own web-based installer to finish
// setup (with the database NimVor created). WordPress and Laravel are not here
// — they are installed unattended by bespoke code.
type AppDef struct {
	Name    string   // human name
	URL     string   // archive URL (https)
	SumURL  string   // checksum sidecar URL ("" = none published)
	SumAlgo string   // "sha256" or "sha1"
	Format  string   // "targz" or "zip"
	Strip   int      // leading path components to drop
	DocRoot string   // web root relative to the install folder ("" = the folder)
	Ext     []string // required PHP extensions
	Finish  string   // path under the site URL that runs the app's installer
	NoDB    bool     // the app needs no database (flat-file, e.g. Grav)
	// GitHub and AssetRe: the download is the release asset of this
	// "owner/repo" whose name matches AssetRe, found through the latest
	// release (for projects whose files carry the version in their name).
	GitHub  string
	AssetRe string
	// Unverified, when set, says why the download could not be checked by the
	// CI download test (for example the vendor refuses the runners); the entry
	// still works where the vendor answers.
	Unverified string
	Package    string   // Composer package: installed with create-project instead of a download
	Touch      []string // files created (empty) under the install folder after unpacking, e.g. TYPO3's FIRST_INSTALL
}

// Catalog of staged applications. URLs are the vendors' official "latest
// stable" downloads; the per-app Strip/DocRoot/Ext are confirmed on a real
// host. Verified against a published checksum where the vendor provides one.
var Catalog = map[string]AppDef{
	"joomla":     {Name: "Joomla", GitHub: "joomla/joomla-cms", AssetRe: `Full_Package\.tar\.gz$`, Format: "targz", Strip: 0, Ext: []string{"mysqli", "json", "mbstring", "xml", "zip", "gd"}, Finish: "/installation/"},
	"drupal":     {Name: "Drupal", URL: "https://www.drupal.org/download-latest/tar.gz", Format: "targz", Strip: 1, Ext: []string{"pdo_mysql", "gd", "xml", "mbstring"}, Finish: "/core/install.php"},
	"phpbb":      {Name: "phpBB", URL: "https://download.phpbb.com/pub/release/3.3/latest/phpBB-latest.tar.bz2", Format: "tarbz2", Strip: 1, Ext: []string{"mysqli", "json", "gd"}, Finish: "/install/", Unverified: "download.phpbb.com answers HTTP 403 to the CI runners; the download could not be checked there"},
	"smf":        {Name: "SMF (Simple Machines Forum)", URL: "https://download.simplemachines.org/index.php?thanks;filename=smf_latest_install.tar.gz", Format: "targz", Strip: 0, Ext: []string{"mysqli", "gd"}, Finish: "/install.php", Unverified: "download.simplemachines.org answers HTTP 403 to the CI runners; the download could not be checked there"},
	"prestashop": {Name: "PrestaShop", GitHub: "PrestaShop/PrestaShop", AssetRe: `^prestashop_[0-9.]+\.zip$`, Format: "zip", Strip: 0, Ext: []string{"pdo_mysql", "gd", "intl", "zip", "curl"}, Finish: "/index.php"},
	"opencart":   {Name: "OpenCart", GitHub: "opencart/opencart", AssetRe: `^opencart-[0-9.]+\.zip$`, Format: "zip", Strip: 0, DocRoot: "upload", Ext: []string{"mysqli", "gd", "curl", "zip"}, Finish: "/install/"},
	"abantecart": {Name: "AbanteCart", URL: "https://github.com/abantecart/abantecart-src/archive/refs/heads/master.tar.gz", Format: "targz", Strip: 1, DocRoot: "public_html", Ext: []string{"mysqli", "gd", "curl", "zip"}, Finish: "/install/"},
	"nextcloud":  {Name: "Nextcloud", URL: "https://download.nextcloud.com/server/releases/latest.tar.bz2", SumURL: "https://download.nextcloud.com/server/releases/latest.tar.bz2.sha256", SumAlgo: "sha256", Format: "tarbz2", Strip: 1, Ext: []string{"pdo_mysql", "gd", "curl", "zip", "mbstring", "xml"}, Finish: "/index.php"},
	"matomo":     {Name: "Matomo", URL: "https://builds.matomo.org/matomo.zip", Format: "zip", Strip: 1, Ext: []string{"pdo_mysql", "gd", "curl", "mbstring"}, Finish: "/index.php"},
	"moodle":     {Name: "Moodle", URL: "https://download.moodle.org/download.php/direct/stable405/moodle-latest-405.tgz", Format: "targz", Strip: 1, Ext: []string{"pdo_mysql", "gd", "curl", "intl", "xml", "zip", "mbstring"}, Finish: "/install.php"},
	"mediawiki":  {Name: "MediaWiki", URL: "https://releases.wikimedia.org/mediawiki/1.43/mediawiki-1.43.1.tar.gz", Format: "targz", Strip: 1, Ext: []string{"mysqli", "mbstring", "xml", "intl"}, Finish: "/mw-config/index.php"},
	"grav":       {Name: "Grav", URL: "https://getgrav.org/download/core/grav/latest", Format: "zip", Strip: 1, NoDB: true, Ext: []string{"gd", "curl", "mbstring", "zip", "json"}, Finish: "/"},
	// Blogs & CMS
	"backdrop":    {Name: "Backdrop CMS", URL: "https://github.com/backdrop/backdrop/releases/latest/download/backdrop.zip", Format: "zip", Strip: 1, Ext: []string{"pdo_mysql", "gd", "mbstring", "xml", "json"}, Finish: "/core/install.php"},
	"processwire": {Name: "ProcessWire", URL: "https://github.com/processwire/processwire/archive/refs/heads/master.zip", Format: "zip", Strip: 1, Ext: []string{"pdo_mysql", "gd", "mbstring", "json"}, Finish: "/install.php"},
	"bludit":      {Name: "Bludit", URL: "https://www.bludit.com/releases/bludit-latest.zip", Format: "zip", Strip: 1, NoDB: true, Ext: []string{"gd", "mbstring", "json", "dom"}, Finish: "/"},
	// Business, finance
	"dolibarr":  {Name: "Dolibarr ERP & CRM", URL: "https://sourceforge.net/projects/dolibarr/files/latest/download", Format: "zip", Strip: 1, DocRoot: "htdocs", Ext: []string{"mysqli", "gd", "curl", "intl", "mbstring", "xml", "zip"}, Finish: "/install/"},
	"akaunting": {Name: "Akaunting", URL: "https://akaunting.com/download.php?version=latest", Format: "zip", Strip: 0, Ext: []string{"pdo_mysql", "mbstring", "xml", "curl", "zip", "gd", "intl", "bcmath", "fileinfo", "dom"}, Finish: "/"},
	// Composer-distributed
	"flarum":   {Name: "Flarum", Package: "flarum/flarum", DocRoot: "public", Ext: []string{"pdo_mysql", "mbstring", "gd", "curl", "dom", "fileinfo", "json", "tokenizer", "zip"}, Finish: "/"},
	"typo3":    {Name: "TYPO3", Package: "typo3/cms-base-distribution", DocRoot: "public", Touch: []string{"public/FIRST_INSTALL"}, Ext: []string{"pdo_mysql", "gd", "intl", "mbstring", "xml", "zip", "curl", "json"}, Finish: "/typo3/install.php"},
	"concrete": {Name: "Concrete CMS", Package: "concretecms/composer", DocRoot: "public", Ext: []string{"pdo_mysql", "gd", "mbstring", "xml", "curl", "zip", "fileinfo", "intl"}, Finish: "/index.php/install"},
	"bagisto":  {Name: "Bagisto", Package: "bagisto/bagisto", DocRoot: "public", Ext: []string{"pdo_mysql", "gd", "intl", "mbstring", "xml", "curl", "zip", "bcmath", "fileinfo", "openssl"}, Finish: "/install"},
	"shopware": {Name: "Shopware Community Edition", Package: "shopware/production", DocRoot: "public", Ext: []string{"pdo_mysql", "gd", "intl", "mbstring", "xml", "curl", "zip", "dom", "fileinfo", "openssl", "sodium"}, Finish: "/installer"},
	// Frameworks and starters (Composer; no database is created)
	"symfony":     {Name: "Symfony", Package: "symfony/skeleton", NoDB: true, DocRoot: "public", Ext: []string{"mbstring", "xml", "intl", "ctype", "iconv"}, Finish: "/"},
	"codeigniter": {Name: "CodeIgniter 4", Package: "codeigniter4/appstarter", NoDB: true, DocRoot: "public", Ext: []string{"mbstring", "intl", "json", "curl"}, Finish: "/"},
	"yii":         {Name: "Yii 2", Package: "yiisoft/yii2-app-basic", NoDB: true, DocRoot: "web", Ext: []string{"mbstring", "xml", "intl", "ctype"}, Finish: "/"},
	"cakephp":     {Name: "CakePHP", Package: "cakephp/app", NoDB: true, DocRoot: "webroot", Ext: []string{"mbstring", "intl", "xml", "json"}, Finish: "/"},
	"slim":        {Name: "Slim", Package: "slim/slim-skeleton", NoDB: true, DocRoot: "public", Ext: []string{"mbstring", "json"}, Finish: "/"},
	// More CMS, business, finance
	"kirby":          {Name: "Kirby", Package: "getkirby/plainkit", NoDB: true, Ext: []string{"mbstring", "gd", "curl", "ctype", "dom", "json", "libxml"}, Finish: "/panel"},
	"craft":          {Name: "Craft CMS", Package: "craftcms/craft", DocRoot: "web", Ext: []string{"pdo_mysql", "gd", "intl", "mbstring", "xml", "curl", "zip", "bcmath", "dom"}, Finish: "/admin/install"},
	"invoiceninja":   {Name: "Invoice Ninja", GitHub: "invoiceninja/invoiceninja", AssetRe: `^invoiceninja\.tar\.gz$`, Format: "targz", Strip: 0, DocRoot: "public", Ext: []string{"pdo_mysql", "gd", "mbstring", "xml", "curl", "zip", "bcmath", "intl", "fileinfo"}, Finish: "/setup"},
	"facturascripts": {Name: "FacturaScripts", URL: "https://facturascripts.com/DownloadBuild/1/stable", Format: "zip", Strip: 1, Ext: []string{"mysqli", "gd", "mbstring", "xml", "curl", "zip", "openssl"}, Finish: "/"},
	// Media, marketing
	"piwigo": {Name: "Piwigo", URL: "https://piwigo.org/download/dlcounter.php?code=latest", Format: "zip", Strip: 1, Ext: []string{"mysqli", "gd", "mbstring", "exif", "json", "zip"}, Finish: "/install.php"},
	"mautic": {Name: "Mautic", Package: "mautic/recommended-project", DocRoot: "docroot", Ext: []string{"pdo_mysql", "gd", "intl", "mbstring", "xml", "curl", "zip", "bcmath", "imap"}, Finish: "/installer"},
}

// Request describes an installation.
type Request struct {
	App        string `json:"app"` // wordpress, laravel, or a Catalog key
	Domain     string `json:"domain"`
	User       string `json:"user"`
	Root       string `json:"root"`
	Path       string `json:"path"` // relative install folder
	PHPVersion string `json:"php_version"`
	DB         DB     `json:"db"`
	URL        string `json:"url"` // https://example.com
	// WordPress
	Title         string `json:"title"`
	AdminUser     string `json:"admin_user"`
	AdminEmail    string `json:"admin_email"`
	AdminPassword string `json:"admin_password"`
	// WordPress: plugins from wordpress.org installed and activated after the
	// site is set up (keys of WPPlugins).
	Plugins []string `json:"plugins"`
	// Laravel
	Migrate bool `json:"migrate"`
	// Native applications (programs supervised as a worker behind the reverse
	// proxy): the loopback port the website's proxy points at, and the worker
	// slug the panel reserved for the service.
	Port        int    `json:"port"`
	ServiceSlug string `json:"service_slug"`
	// Runtime is the website's selected language runtime ("python:3.12.7").
	Runtime string `json:"runtime"`
	// AllowTesting lets an application in testing status be installed. The
	// panel sets it only for administrators; every other status that is not
	// enabled is refused whatever the request says.
	AllowTesting bool `json:"allow_testing"`
}

// WPPlugins are the wordpress.org plugins an install may add (WooCommerce is
// "WordPress + WooCommerce" in the catalog), with their main plugin file.
var WPPlugins = map[string]struct{ URL, Main string }{
	"woocommerce":            {"https://downloads.wordpress.org/plugin/woocommerce.latest-stable.zip", "woocommerce/woocommerce.php"},
	"tutor":                  {"https://downloads.wordpress.org/plugin/tutor.latest-stable.zip", "tutor/tutor.php"},
	"learnpress":             {"https://downloads.wordpress.org/plugin/learnpress.latest-stable.zip", "learnpress/learnpress.php"},
	"lifterlms":              {"https://downloads.wordpress.org/plugin/lifterlms.latest-stable.zip", "lifterlms/lifterlms.php"},
	"bbpress":                {"https://downloads.wordpress.org/plugin/bbpress.latest-stable.zip", "bbpress/bbpress.php"},
	"buddypress":             {"https://downloads.wordpress.org/plugin/buddypress.latest-stable.zip", "buddypress/bp-loader.php"},
	"easy-digital-downloads": {"https://downloads.wordpress.org/plugin/easy-digital-downloads.latest-stable.zip", "easy-digital-downloads/easy-digital-downloads.php"},
	"wp-statistics":          {"https://downloads.wordpress.org/plugin/wp-statistics.latest-stable.zip", "wp-statistics/wp-statistics.php"},
	"koko-analytics":         {"https://downloads.wordpress.org/plugin/koko-analytics.latest-stable.zip", "koko-analytics/koko-analytics.php"},
	"download-monitor":       {"https://downloads.wordpress.org/plugin/download-monitor.latest-stable.zip", "download-monitor/download-monitor.php"},
}

// wpActivate activates the plugin named by NIMVOR_PLUGIN from the command line.
const wpActivate = `<?php
define('WP_USE_THEMES', false);
require 'wp-load.php';
require_once ABSPATH . 'wp-admin/includes/plugin.php';
$r = activate_plugin(getenv('NIMVOR_PLUGIN'));
if (is_wp_error($r)) { fwrite(STDERR, $r->get_error_message()); exit(1); }
`

var (
	wpUser = regexp.MustCompile(`^[A-Za-z0-9._@-]{3,60}$`)
	urlRe  = regexp.MustCompile(`^https?://[A-Za-z0-9.-]+(:[0-9]+)?$`)
)

func (r *Request) validate(env *host.Env) error {
	_, staged := Catalog[r.App]
	_, native := nativeApps[r.App]
	if r.App != "wordpress" && r.App != "laravel" && !staged && !native {
		return &validate.Error{Field: "app", Message: "unknown application"}
	}
	if err := validate.Domain("domain", r.Domain); err != nil {
		return err
	}
	if err := validate.Username("user", r.User); err != nil {
		return err
	}
	if r.Root == "" {
		r.Root = filepath.Join(env.Cfg.SitesRoot, r.Domain)
	}
	if err := validate.Within("root", env.Cfg.SitesRoot, r.Root); err != nil {
		return err
	}
	if r.Path == "" {
		return &validate.Error{Field: "path", Message: "choose the folder to install into"}
	}
	if err := validate.RelPath("path", r.Path); err != nil {
		return err
	}
	switch strings.Split(filepath.Clean(r.Path), "/")[0] {
	case "logs", "tmp", "backups", ".ssh":
		return &validate.Error{Field: "path", Message: "cannot install into " + r.Path}
	}
	if native {
		// No PHP, no MySQL: a loopback port and a worker slug instead.
		if r.Port < 1024 || r.Port > 65535 {
			return &validate.Error{Field: "port", Message: "must be a port between 1024 and 65535"}
		}
		if err := validate.Slug("service_slug", r.ServiceSlug); err != nil {
			return err
		}
		if !urlRe.MatchString(r.URL) {
			return &validate.Error{Field: "url", Message: "must be the website address, e.g. https://example.com"}
		}
		if r.App == "django" {
			if !wpUser.MatchString(r.AdminUser) {
				return &validate.Error{Field: "admin_user", Message: "3–60 letters, digits or . _ @ -"}
			}
			if !regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`).MatchString(r.AdminEmail) || len(r.AdminEmail) > 100 {
				return &validate.Error{Field: "admin_email", Message: "must be an email address"}
			}
			if len(r.AdminPassword) < 12 || len(r.AdminPassword) > 128 || strings.ContainsAny(r.AdminPassword, "\x00\n\r") {
				return &validate.Error{Field: "admin_password", Message: "12–128 characters on one line"}
			}
		}
		if m := nativeApps[r.App].manifest; m.Runtime.Type == "python" && r.Runtime != "" && !regexp.MustCompile(`^python:[0-9][0-9A-Za-z.]{0,31}$`).MatchString(r.Runtime) {
			return &validate.Error{Field: "runtime", Message: "must look like python:3.12.7"}
		}
		return nil
	}
	if err := validate.PHPVersion("php_version", r.PHPVersion); err != nil {
		return err
	}
	// Flat-file staged apps (e.g. Grav) need no database; everything else does.
	if !(staged && Catalog[r.App].NoDB) {
		if err := validate.DBIdentifier("db.name", r.DB.Name); err != nil {
			return err
		}
		if err := validate.DBIdentifier("db.user", r.DB.User); err != nil {
			return err
		}
		if r.DB.Password == "" || len(r.DB.Password) > 200 || strings.ContainsAny(r.DB.Password, "\x00\n\r") {
			return &validate.Error{Field: "db.password", Message: "is required"}
		}
		if r.DB.Host == "" {
			r.DB.Host = "localhost"
		}
		if r.DB.Host != "localhost" && r.DB.Host != "127.0.0.1" {
			return &validate.Error{Field: "db.host", Message: "must be localhost"}
		}
	}
	if !urlRe.MatchString(r.URL) {
		return &validate.Error{Field: "url", Message: "must be the website address, e.g. https://example.com"}
	}
	for _, p := range r.Plugins {
		if _, ok := WPPlugins[p]; !ok || r.App != "wordpress" {
			return &validate.Error{Field: "plugins", Message: "unknown plugin"}
		}
	}
	if r.App == "wordpress" {
		if len(r.Title) == 0 || len(r.Title) > 100 || strings.ContainsAny(r.Title, "\x00\n\r") {
			return &validate.Error{Field: "title", Message: "1–100 characters"}
		}
		if !wpUser.MatchString(r.AdminUser) {
			return &validate.Error{Field: "admin_user", Message: "3–60 letters, digits or . _ @ -"}
		}
		if !regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`).MatchString(r.AdminEmail) || len(r.AdminEmail) > 100 {
			return &validate.Error{Field: "admin_email", Message: "must be an email address"}
		}
		if err := validate.Password("admin_password", r.AdminPassword, 12); err != nil {
			return err
		}
	}
	return nil
}

// emptyTarget reports whether dir is missing, empty, or holds only NimVor's
// placeholder page.
func emptyTarget(env *host.Env, r Request, dir string) (bool, error) {
	entries, err := env.Root.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() == "index.html" {
			b, _ := env.Root.ReadFile(filepath.Join(dir, "index.html"))
			if sites.PlaceholderIndex(b, r.Domain) {
				continue
			}
		}
		return false, nil
	}
	return true, nil
}

// download fetches url to a root-only temporary file and returns its path
// and SHA-1 (WordPress's checksum algorithm).
func download(ctx context.Context, env *host.Env, client *http.Client, url string, log io.Writer) (string, string, error) {
	return downloadSum(ctx, env, client, url, "sha1", log)
}

// downloadSum fetches url to a root-only temporary file and returns its path
// and the hex digest in the requested algorithm ("sha1" or "sha256").
func downloadSum(ctx context.Context, env *host.Env, client *http.Client, url, algo string, log io.Writer) (string, string, error) {
	dir := filepath.Join(env.Cfg.StateDir, "downloads")
	if _, err := env.Writer.MkdirAll(dir, 0o700, agentfs.RootOwner); err != nil {
		return "", "", err
	}
	real, err := env.Root.Path(dir)
	if err != nil {
		return "", "", err
	}
	f, err := os.CreateTemp(real, "app-*.archive")
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", userAgent)
	res, err := client.Do(req)
	if err != nil {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("download failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("download failed: HTTP %d", res.StatusCode)
	}
	var h hash.Hash
	if algo == "sha256" {
		h = sha256.New()
	} else {
		h = sha1.New()
	}
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, maxDownload+1))
	if err != nil || n > maxDownload {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("download failed or too large")
	}
	return f.Name(), hex.EncodeToString(h.Sum(nil)), nil
}

// extractArchive unpacks a .tar.gz, .tar.bz2 or .zip into dest, dropping the
// first strip path components. Entries must stay inside dest; a symlink may
// only point inside dest; special files are skipped. Each extracted file is
// owned by the website account (uid/gid).
func extractArchive(format, archive, dest string, strip, uid, gid int) error {
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}
	if format == "zip" {
		return extractZip(archive, dest, strip, uid, gid)
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	var dr io.Reader = f
	if format == "tarbz2" {
		dr = bzip2.NewReader(f)
	} else {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		dr = gz
	}
	tr := tar.NewReader(dr)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		rel, ok := stripPath(hdr.Name, strip)
		if !ok {
			continue
		}
		target, ok := safeJoin(dest, rel)
		if !ok {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			os.Chown(target, uid, gid)
		case tar.TypeReg:
			if total += hdr.Size; total > maxUnpack {
				return fmt.Errorf("the archive is too large")
			}
			if err := writeFile(target, tr, os.FileMode(hdr.Mode)&0o755, uid, gid); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := safeSymlink(dest, target, hdr.Linkname, uid, gid); err != nil {
				return err
			}
		}
	}
	return nil
}

func extractZip(archive, dest string, strip, uid, gid int) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	var total int64
	for _, zf := range zr.File {
		rel, ok := stripPath(zf.Name, strip)
		if !ok {
			continue
		}
		target, ok := safeJoin(dest, rel)
		if !ok {
			return fmt.Errorf("unsafe path in archive: %s", zf.Name)
		}
		info := zf.FileInfo()
		if info.IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			os.Chown(target, uid, gid)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			rc, err := zf.Open()
			if err != nil {
				return err
			}
			link, _ := io.ReadAll(io.LimitReader(rc, 4096))
			rc.Close()
			if err := safeSymlink(dest, target, string(link), uid, gid); err != nil {
				return err
			}
			continue
		}
		if total += int64(zf.UncompressedSize64); total > maxUnpack {
			return fmt.Errorf("the archive is too large")
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = writeFile(target, rc, info.Mode()&0o755, uid, gid)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

const maxUnpack = 2 << 30 // 2 GiB unpacked

// stripPath drops the first n path components; ok is false for the stripped
// prefix itself and for empty results.
func stripPath(name string, n int) (string, bool) {
	clean := path.Clean(strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "./"))
	if clean == "." || clean == "" {
		return "", false
	}
	parts := strings.Split(clean, "/")
	if len(parts) <= n {
		return "", false
	}
	return strings.Join(parts[n:], "/"), true
}

// safeJoin joins rel onto dest, refusing anything that escapes dest.
func safeJoin(dest, rel string) (string, bool) {
	if strings.HasPrefix(rel, "/") || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return "", false
	}
	target := filepath.Join(dest, filepath.FromSlash(rel))
	if target != dest && !strings.HasPrefix(target, dest+string(os.PathSeparator)) {
		return "", false
	}
	return target, true
}

func writeFile(target string, r io.Reader, mode os.FileMode, uid, gid int) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	w, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, io.LimitReader(r, maxUnpack)); err != nil {
		w.Close()
		return err
	}
	w.Close()
	return os.Chown(target, uid, gid)
}

// safeSymlink creates a symlink only when its target stays within dest.
func safeSymlink(dest, target, link string, uid, gid int) error {
	var resolved string
	if filepath.IsAbs(link) {
		resolved = filepath.Clean(link)
	} else {
		resolved = filepath.Join(filepath.Dir(target), link)
	}
	if resolved != dest && !strings.HasPrefix(resolved, dest+string(os.PathSeparator)) {
		return fmt.Errorf("unsafe symlink: %s -> %s", target, link)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	_ = os.Remove(target)
	if err := os.Symlink(link, target); err != nil {
		return err
	}
	return os.Lchown(target, uid, gid)
}

func fetchText(ctx context.Context, client *http.Client, url string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", userAgent)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	return strings.TrimSpace(string(b)), err
}

func salt() string {
	b := make([]byte, 48)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// phpString quotes s for a single-quoted PHP string literal.
func phpString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// WPConfig renders wp-config.php.
func WPConfig(r Request) string {
	var b strings.Builder
	b.WriteString("<?php\n// Generated by NimVor.\n")
	fmt.Fprintf(&b, "define('DB_NAME', %s);\ndefine('DB_USER', %s);\ndefine('DB_PASSWORD', %s);\ndefine('DB_HOST', %s);\n", phpString(r.DB.Name), phpString(r.DB.User), phpString(r.DB.Password), phpString(r.DB.Host))
	b.WriteString("define('DB_CHARSET', 'utf8mb4');\ndefine('DB_COLLATE', '');\n")
	for _, k := range []string{"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY", "AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT"} {
		fmt.Fprintf(&b, "define('%s', %s);\n", k, phpString(salt()))
	}
	b.WriteString("$table_prefix = 'wp_';\n")
	b.WriteString("define('DISALLOW_FILE_EDIT', true);\ndefine('WP_DEBUG', false);\n")
	// WordPress's usual marker: tools (WP-CLI "config set") add settings above it.
	b.WriteString("/* That's all, stop editing! Happy publishing. */\n")
	b.WriteString("if (!defined('ABSPATH')) {\n    define('ABSPATH', __DIR__ . '/');\n}\nrequire_once ABSPATH . 'wp-settings.php';\n")
	return b.String()
}

// wpInstall runs WordPress's installer from the command line.
const wpInstall = `<?php
define('WP_INSTALLING', true);
$u = parse_url(getenv('NIMVOR_URL'));
$_SERVER['HTTP_HOST'] = $u['host'] . (isset($u['port']) ? ':' . $u['port'] : '');
$_SERVER['REQUEST_URI'] = '/';
$_SERVER['HTTPS'] = $u['scheme'] === 'https' ? 'on' : '';
// No "new site" e-mail: the panel shows the login details, and the local
// sendmail cannot queue mail from the installer's confined unit (Postfix's
// setgid postdrop is refused under NoNewPrivileges and retries forever).
function wp_new_blog_notification() {}
require getcwd() . '/wp-load.php';
require_once ABSPATH . 'wp-admin/includes/upgrade.php';
if (is_blog_installed()) { fwrite(STDERR, "WordPress is already installed.\n"); exit(1); }
$r = wp_install(getenv('NIMVOR_TITLE'), getenv('NIMVOR_ADMIN_USER'), getenv('NIMVOR_ADMIN_EMAIL'), true, '', wp_slash(getenv('NIMVOR_ADMIN_PASSWORD')));
update_option('siteurl', getenv('NIMVOR_URL'));
update_option('home', getenv('NIMVOR_URL'));
echo "WordPress installed (user id " . $r['user_id'] . ").\n";
`

// laravelEnv rewrites DB_* and APP_URL in a Laravel .env file.
const laravelEnv = `<?php
$f = getcwd() . '/.env';
$env = file_get_contents($f);
$set = ['DB_CONNECTION' => 'mysql', 'DB_HOST' => getenv('NIMVOR_DB_HOST'), 'DB_PORT' => '3306', 'DB_DATABASE' => getenv('NIMVOR_DB_NAME'), 'DB_USERNAME' => getenv('NIMVOR_DB_USER'), 'DB_PASSWORD' => getenv('NIMVOR_DB_PASSWORD'), 'APP_URL' => getenv('NIMVOR_URL'), 'APP_ENV' => 'production', 'APP_DEBUG' => 'false'];
foreach ($set as $k => $v) {
    $line = $k . '=' . (preg_match('/[\s#"\'$]/', $v) ? '"' . addcslashes($v, '"\\$') . '"' : $v);
    $n = 0;
    $env = preg_replace('/^#?\s*' . $k . '=.*$/m', str_replace(['\\', '$'], ['\\\\', '\\$'], $line), $env, 1, $n);
    if ($n === 0) { $env .= "\n" . $line; }
}
file_put_contents($f, $env);
chmod($f, 0640);
echo "Configured .env\n";
`

// Install performs the installation (called inside a job).
func Install(ctx context.Context, env *host.Env, src Sources, r Request, cred *execx.Credential, log io.Writer) (map[string]any, error) {
	root, _ := env.Root.Path(r.Root)
	target := filepath.Join(root, r.Path)
	if n, ok := nativeApps[r.App]; ok {
		return n.install(ctx, env, src, r, cred, log)
	}
	php := "php" + r.PHPVersion
	as := func(dir string, extraEnv []string, stdin string) execx.Opts {
		o := execx.Opts{As: cred, Dir: dir, Output: log, Env: extraEnv, Timeout: 20 * time.Minute}
		if stdin != "" {
			o.Stdin = strings.NewReader(stdin)
		}
		return o
	}
	// Check PHP extensions first, so nothing is downloaded or written in vain.
	need := map[string][]string{"wordpress": {"mysqli", "json"}, "laravel": {"pdo_mysql", "mbstring", "xml", "curl", "zip"}}[r.App]
	if def, ok := Catalog[r.App]; ok {
		need = def.Ext
	}
	if missing, err := missingPHPExtensions(ctx, env, php, cred, root, need); err != nil {
		return nil, fmt.Errorf("PHP %s is missing extensions: %s. Install them under PHP and try again", r.PHPVersion, missing)
	}
	mkdir := fmt.Sprintf("umask 027 && mkdir -p %s", shQuote(target))
	if _, err := env.Runner.RunWith(ctx, "sh", as(root, nil, ""), "-c", mkdir); err != nil {
		return nil, fmt.Errorf("the folder %s could not be created", r.Path)
	}

	switch r.App {
	case "wordpress":
		sumURL := src.WordPressURL + ".sha1"
		want, err := fetchText(ctx, src.Client, sumURL)
		if err != nil || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(want) {
			return nil, fmt.Errorf("the WordPress checksum could not be downloaded")
		}
		file, got, err := download(ctx, env, src.Client, src.WordPressURL, log)
		if err != nil {
			return nil, err
		}
		defer os.Remove(file)
		if got != want {
			return nil, fmt.Errorf("the WordPress download does not match its published checksum")
		}
		fmt.Fprintln(log, "Checksum verified.")
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		// Remove NimVor's placeholder page, then extract as the site account.
		_, _ = env.Runner.RunWith(ctx, "sh", as(target, nil, ""), "-c", "rm -f index.html")
		o := as(target, nil, "")
		o.Stdin = f
		if out, err := env.Runner.RunWith(ctx, "tar", o, "-xzf", "-", "--no-same-owner", "--strip-components=1", "-C", target); err != nil {
			return nil, fmt.Errorf("extracting WordPress failed: %s", host.Output(out))
		}
		if err := env.StripSetID(ctx, cred, target); err != nil {
			return nil, err
		}
		if _, err := env.Runner.RunWith(ctx, "sh", as(target, nil, WPConfig(r)), "-c", "umask 027 && cat > wp-config.php"); err != nil {
			return nil, fmt.Errorf("wp-config.php could not be written")
		}
		fmt.Fprintln(log, "Wrote wp-config.php (file editing in the dashboard is disabled).")
		wpEnv := []string{"NIMVOR_URL=" + r.URL, "NIMVOR_TITLE=" + r.Title, "NIMVOR_ADMIN_USER=" + r.AdminUser, "NIMVOR_ADMIN_EMAIL=" + r.AdminEmail, "NIMVOR_ADMIN_PASSWORD=" + r.AdminPassword}
		if out, err := env.Runner.RunWith(ctx, php, as(target, wpEnv, wpInstall), "-d", "display_errors=stderr"); err != nil {
			return nil, fmt.Errorf("the WordPress installer failed: %s", host.Output(out))
		}
		for _, name := range r.Plugins {
			pl := WPPlugins[name]
			fmt.Fprintf(log, "Installing the %s plugin…\n", name)
			pfile, _, err := downloadSum(ctx, env, src.Client, pl.URL, "sha256", log)
			if err != nil {
				return nil, fmt.Errorf("the %s plugin could not be downloaded: %v", name, err)
			}
			defer os.Remove(pfile)
			plugins := filepath.Join(target, "wp-content", "plugins")
			if err := extractArchive("zip", pfile, plugins, 0, int(cred.UID), int(cred.GID)); err != nil {
				return nil, fmt.Errorf("unpacking the %s plugin failed: %v", name, err)
			}
			if out, err := env.Runner.RunWith(ctx, php, as(target, []string{"NIMVOR_PLUGIN=" + pl.Main}, wpActivate), "-d", "display_errors=stderr"); err != nil {
				return nil, fmt.Errorf("activating the %s plugin failed: %s", name, host.Output(out))
			}
		}
		return map[string]any{"app": "wordpress", "path": r.Path, "admin_user": r.AdminUser, "login_url": r.URL + "/wp-admin/"}, nil

	case "laravel":
		if !env.Runner.Available("composer") {
			return nil, fmt.Errorf("composer is not installed; install it under Packages")
		}
		if err := composerCreate(ctx, env, r, cred, root, target, "laravel/laravel", log); err != nil {
			return nil, err
		}
		lenv := []string{"NIMVOR_URL=" + r.URL, "NIMVOR_DB_HOST=" + r.DB.Host, "NIMVOR_DB_NAME=" + r.DB.Name, "NIMVOR_DB_USER=" + r.DB.User, "NIMVOR_DB_PASSWORD=" + r.DB.Password}
		if out, err := env.Runner.RunWith(ctx, php, as(target, lenv, laravelEnv)); err != nil {
			return nil, fmt.Errorf("configuring .env failed: %s", host.Output(out))
		}
		if r.Migrate {
			if out, err := env.Runner.RunWith(ctx, php, as(target, nil, ""), "artisan", "migrate", "--force"); err != nil {
				return nil, fmt.Errorf("php artisan migrate failed: %s", host.Output(out))
			}
		}
		return map[string]any{"app": "laravel", "path": r.Path, "document_root": filepath.Join(r.Path, "public")}, nil
	}
	if def, ok := Catalog[r.App]; ok {
		return installStaged(ctx, env, src, r, def, cred, target, log)
	}
	return nil, fmt.Errorf("unknown app")
}

// composerCreate runs `composer create-project pkg` into target (which must be
// missing or empty) with the website's PHP version, as the website account.
// pkg is a constant from the catalog, never user input.
func composerCreate(ctx context.Context, env *host.Env, r Request, cred *execx.Credential, root, target, pkg string, log io.Writer) error {
	if !env.Runner.Available("composer") {
		return fmt.Errorf("composer is not installed; install it under Packages")
	}
	parent, name := filepath.Split(target)
	run := func(dir string, env2 []string) execx.Opts {
		return execx.Opts{As: cred, Dir: dir, Output: log, Env: env2, Timeout: 30 * time.Minute}
	}
	// create-project needs a missing or empty target folder.
	_, _ = env.Runner.RunWith(ctx, "sh", run(root, nil), "-c", "rmdir "+shQuote(target)+" 2>/dev/null; true")
	cenv := []string{"COMPOSER_NO_INTERACTION=1", "COMPOSER_HOME=" + filepath.Join(root, ".composer"), "COMPOSER_CACHE_DIR=" + filepath.Join(root, "tmp", "composer-cache")}
	composer := "/usr/bin/composer"
	if env.Root.Exists("/usr/local/bin/composer") {
		composer = "/usr/local/bin/composer"
	}
	// php8.3 on Debian, Remi's /opt/remi/php83/root/usr/bin/php on RHEL.
	phpBin := "php" + r.PHPVersion
	for _, v := range env.Discovery.PHP().Versions {
		if v.Version == r.PHPVersion && v.CLIBinary != "" {
			phpBin = v.CLIBinary
		}
	}
	script := fmt.Sprintf("exec %s %s create-project --prefer-dist --no-progress %s %s", shQuote(phpBin), composer, pkg, shQuote(filepath.Clean(name)))
	if out, err := env.Runner.RunWith(ctx, "sh", run(parent, cenv), "-c", script); err != nil {
		return fmt.Errorf("composer create-project failed: %s", host.Output(out))
	}
	return nil
}

// githubAPI is the GitHub API; tests point it at a local server.
var githubAPI = "https://api.github.com"

// ResolveURL is where def's archive is downloaded from: its URL, or the asset
// of the latest GitHub release that matches AssetRe.
func ResolveURL(ctx context.Context, client *http.Client, def AppDef) (string, error) {
	if def.GitHub == "" {
		return def.URL, nil
	}
	re, err := regexp.Compile(def.AssetRe)
	if err != nil {
		return "", err
	}
	var names []string
	// The latest release first; some projects publish their files on an
	// earlier release (or not on GitHub at all), so look through a few.
	for _, path := range []string{"/releases/latest", "/releases?per_page=15"} {
		releases, err := githubReleases(ctx, client, def, path)
		if err != nil {
			return "", err
		}
		for _, rel := range releases {
			if rel.Draft || rel.Prerelease {
				continue
			}
			for _, a := range rel.Assets {
				if len(names) < 20 {
					names = append(names, a.Name)
				}
				if re.MatchString(a.Name) && strings.HasPrefix(a.URL, "https://github.com/"+def.GitHub+"/releases/download/") {
					return a.URL, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no recent %s release has a download matching %s (assets seen: %s)", def.Name, def.AssetRe, strings.Join(names, ", "))
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// githubReleases reads one release (…/releases/latest) or a list of them.
func githubReleases(ctx context.Context, client *http.Client, def AppDef, path string) ([]githubRelease, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, githubAPI+"/repos/"+def.GitHub+path, nil)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the latest %s release could not be looked up: %v", def.Name, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the latest %s release could not be looked up: HTTP %d", def.Name, res.StatusCode)
	}
	body := io.LimitReader(res.Body, 8<<20)
	if strings.HasSuffix(path, "/latest") {
		var one githubRelease
		if err := json.NewDecoder(body).Decode(&one); err != nil {
			return nil, fmt.Errorf("the latest %s release could not be read: %v", def.Name, err)
		}
		return []githubRelease{one}, nil
	}
	var many []githubRelease
	if err := json.NewDecoder(body).Decode(&many); err != nil {
		return nil, fmt.Errorf("the %s releases could not be read: %v", def.Name, err)
	}
	return many, nil
}

// missingPHPExtensions runs a read-only check as the site account and returns
// the extensions that are not loaded (an error when any is missing).
func missingPHPExtensions(ctx context.Context, env *host.Env, php string, cred *execx.Credential, root string, need []string) (string, error) {
	check := fmt.Sprintf(`$m = array_filter(%s, fn ($e) => !extension_loaded($e)); if ($m) { echo implode(" ", $m); exit(1); }`, phpArray(need))
	out, err := env.Runner.RunWith(ctx, php, execx.Opts{As: cred, Dir: root, Timeout: time.Minute}, "-r", check)
	if err != nil {
		return strings.TrimSpace(string(out)), err
	}
	return "", nil
}

// checkInstallable refuses an application whose manifest does not validate or
// whose status does not allow this installation.
func checkInstallable(r Request) error {
	m, ok := ManifestFor(r.App)
	if !ok {
		return fmt.Errorf("unknown application")
	}
	if errs := m.Validate(); len(errs) > 0 {
		return fmt.Errorf("%s has an invalid catalog entry and cannot be installed: %s", m.Name, strings.Join(errs, "; "))
	}
	if !m.Status.Installable(r.AllowTesting) {
		return fmt.Errorf("%s cannot be installed right now (its status is %s)", m.Name, m.Status)
	}
	return nil
}

// installClaims makes an installation target (website and folder) belong to
// one request at a time: a second request for the same target is refused
// before it can create a database or start a job.
var installClaims = struct {
	sync.Mutex
	held map[string]bool
}{held: map[string]bool{}}

func claimInstall(key string) bool {
	installClaims.Lock()
	defer installClaims.Unlock()
	if installClaims.held[key] {
		return false
	}
	installClaims.held[key] = true
	return true
}

func releaseInstall(key string) {
	installClaims.Lock()
	delete(installClaims.held, key)
	installClaims.Unlock()
}

// installStaged downloads def's archive (verified against its checksum when
// the vendor publishes one), unpacks it into target, and hands off to the
// application's own web installer. The database has already been created by
// the panel; its credentials are shown to the user, who finishes setup there.
func installStaged(ctx context.Context, env *host.Env, src Sources, r Request, def AppDef, cred *execx.Credential, target string, log io.Writer) (map[string]any, error) {
	if def.Package != "" {
		return installComposer(ctx, env, r, def, cred, target, log)
	}
	url := def.URL
	if src.CatalogBase != "" {
		// Tests point the whole catalog at a local server.
		url = src.CatalogBase + "/" + r.App + "." + archiveExt(def.Format)
	} else if def.GitHub != "" {
		var err error
		if url, err = ResolveURL(ctx, src.Client, def); err != nil {
			return nil, err
		}
	}
	algo := def.SumAlgo
	if algo == "" {
		algo = "sha256"
	}
	fmt.Fprintf(log, "Downloading %s…\n", def.Name)
	file, got, err := downloadSum(ctx, env, src.Client, url, algo, log)
	if err != nil {
		return nil, err
	}
	defer os.Remove(file)
	sumURL := def.SumURL
	if src.CatalogBase != "" && def.SumURL != "" {
		sumURL = src.CatalogBase + "/" + r.App + ".sum"
	}
	if sumURL != "" {
		want, err := fetchText(ctx, src.Client, sumURL)
		if err != nil {
			return nil, fmt.Errorf("the %s checksum could not be downloaded", def.Name)
		}
		want = strings.Fields(want)[0]
		if !strings.EqualFold(want, got) {
			return nil, fmt.Errorf("the %s download does not match its published checksum", def.Name)
		}
		fmt.Fprintln(log, "Checksum verified.")
	} else {
		fmt.Fprintln(log, "Downloaded over HTTPS (the vendor publishes no checksum file).")
	}
	// Remove NimVor's placeholder page, then unpack confined to the install
	// folder, owned by the website account.
	os.Remove(filepath.Join(target, "index.html"))
	if err := extractArchive(def.Format, file, target, def.Strip, int(cred.UID), int(cred.GID)); err != nil {
		return nil, fmt.Errorf("unpacking %s failed: %v", def.Name, err)
	}
	fmt.Fprintf(log, "Unpacked %s.\n", def.Name)
	docroot := r.Path
	if def.DocRoot != "" {
		docroot = filepath.Join(r.Path, def.DocRoot)
	}
	return map[string]any{
		"app": r.App, "path": r.Path, "document_root": docroot,
		"staged": true, "finish_url": strings.TrimRight(r.URL, "/") + def.Finish,
	}, nil
}

// installComposer installs a Composer-distributed application and hands off to
// its own web installer, like installStaged does for archives.
func installComposer(ctx context.Context, env *host.Env, r Request, def AppDef, cred *execx.Credential, target string, log io.Writer) (map[string]any, error) {
	root, _ := env.Root.Path(r.Root)
	fmt.Fprintf(log, "Installing %s with Composer…\n", def.Name)
	if err := composerCreate(ctx, env, r, cred, root, target, def.Package, log); err != nil {
		return nil, err
	}
	for _, f := range def.Touch {
		if _, err := env.Runner.RunWith(ctx, "sh", execx.Opts{As: cred, Dir: target, Timeout: time.Minute}, "-c", "umask 027 && : > "+shQuote(f)); err != nil {
			return nil, fmt.Errorf("could not prepare %s", def.Name)
		}
	}
	docroot := r.Path
	if def.DocRoot != "" {
		docroot = filepath.Join(r.Path, def.DocRoot)
	}
	return map[string]any{
		"app": r.App, "path": r.Path, "document_root": docroot,
		"staged": true, "finish_url": strings.TrimRight(r.URL, "/") + def.Finish,
	}, nil
}

func archiveExt(format string) string {
	switch format {
	case "zip":
		return "zip"
	case "tarbz2":
		return "tar.bz2"
	default:
		return "tar.gz"
	}
}

func phpArray(items []string) string {
	q := make([]string, len(items))
	for i, it := range items {
		q[i] = phpString(it)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Register adds apps.install.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "apps.install", Mutating: true, Description: "Install an application (WordPress, Laravel, or a staged app like Joomla, Drupal, phpBB, SMF, PrestaShop, OpenCart, AbanteCart) into a website (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in Request
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		if err := checkInstallable(in); err != nil {
			return nil, actions.Conflict(err.Error())
		}
		cred, _, err := env.Credential(in.User)
		if err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		empty, err := emptyTarget(env, in, filepath.Join(in.Root, in.Path))
		if err != nil {
			return nil, actions.Failed("The target folder could not be read.", err.Error())
		}
		if !empty {
			return nil, actions.Conflict(fmt.Sprintf("The folder %s is not empty. Choose an empty folder, or remove its files first.", in.Path))
		}
		// One installation per website and folder at a time.
		key := in.Domain + "|" + filepath.Clean(in.Path)
		if !claimInstall(key) {
			return nil, actions.Conflict(fmt.Sprintf("An installation into %s is already in progress.", in.Path))
		}
		j := env.Jobs.Start("apps.install", "site:"+in.Domain, func(ctx context.Context, log io.Writer) (any, error) {
			defer releaseInstall(key)
			// Look again now that the job holds the website's lock: an earlier
			// job for this website may have filled the folder since the request.
			if empty, err := emptyTarget(env, in, filepath.Join(in.Root, in.Path)); err != nil || !empty {
				return nil, fmt.Errorf("the folder %s is no longer empty; nothing was changed", in.Path)
			}
			return Install(ctx, env, DefaultSources, in, cred, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	registerPlan(r, env)
}
