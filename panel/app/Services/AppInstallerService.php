<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Repositories\DatabaseRepository;
use NimVor\Security\Crypto;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * One-click applications. The panel creates the database and its user,
 * then the agent installs the application as the website account.
 * WordPress is installed from the command line with a generated admin
 * password (shown once), so its web installer is never left open.
 */
final class AppInstallerService
{
    public const APPS = [
        'wordpress' => ['title' => 'WordPress', 'description' => 'Blog and website builder. Installed into the document root with a new database and an administrator account.'],
        'laravel' => ['title' => 'Laravel', 'description' => 'PHP framework skeleton via Composer, with the database configured in .env. The website is served from <folder>/public.'],
        'joomla' => ['title' => 'Joomla', 'description' => 'Content management system. Staged into the document root with a new database; finish in its web installer.', 'staged' => true],
        'drupal' => ['title' => 'Drupal', 'description' => 'Content management framework. Staged into the document root with a new database; finish in its web installer.', 'staged' => true],
        'phpbb' => ['title' => 'phpBB', 'description' => 'Forum software. Staged into the document root with a new database; finish in its web installer.', 'staged' => true],
        'smf' => ['title' => 'SMF', 'description' => 'Simple Machines Forum. Staged into the document root with a new database; finish in its web installer.', 'staged' => true],
        'prestashop' => ['title' => 'PrestaShop', 'description' => 'E-commerce platform. Staged into the document root with a new database; finish in its web installer.', 'staged' => true],
        'opencart' => ['title' => 'OpenCart', 'description' => 'E-commerce platform. Staged with a new database; the website is served from <folder>/upload.', 'staged' => true],
        'abantecart' => ['title' => 'AbanteCart', 'description' => 'E-commerce platform. Staged with a new database; the website is served from <folder>/public_html.', 'staged' => true],
        'nextcloud' => ['title' => 'Nextcloud', 'description' => 'Self-hosted files, calendar and collaboration. Staged; finish in its web installer.', 'staged' => true],
        'moodle' => ['title' => 'Moodle', 'description' => 'Learning management system. Staged; finish in its web installer.', 'staged' => true],
        'matomo' => ['title' => 'Matomo', 'description' => 'Privacy-friendly web analytics. Staged; finish in its web installer.', 'staged' => true],
        'mediawiki' => ['title' => 'MediaWiki', 'description' => 'The wiki engine behind Wikipedia. Staged; finish in its web installer.', 'staged' => true],
        'woocommerce' => ['title' => 'WooCommerce', 'description' => 'WordPress with the WooCommerce online store plugin installed and activated, with a new database and an administrator account.'],
        'gitea' => ['title' => 'Gitea', 'description' => 'Self-hosted Git service. Runs as a service behind this reverse-proxy website; finish setup in its own installer.', 'staged' => true],
        'django' => ['title' => 'Django', 'description' => 'Python web framework with the admin site, in a virtual environment behind this Python website.'],
        'fastapi' => ['title' => 'FastAPI', 'description' => 'Python API framework starter in a virtual environment behind this Python website.', 'staged' => true],
        'grav' => ['title' => 'Grav', 'description' => 'Flat-file CMS — no database. Staged; ready to use after install.', 'staged' => true],
        'backdrop' => ['title' => 'Backdrop CMS', 'description' => 'Lightweight CMS for small business and nonprofit sites. Staged with a new database; finish in its web installer.', 'staged' => true],
        'processwire' => ['title' => 'ProcessWire', 'description' => 'Flexible, API-first CMS. Staged with a new database; finish in its web installer.', 'staged' => true],
        'bludit' => ['title' => 'Bludit', 'description' => 'Flat-file blogging CMS — no database. Staged; ready to use after install.', 'staged' => true],
        'dolibarr' => ['title' => 'Dolibarr ERP & CRM', 'description' => 'ERP and CRM for small businesses. Staged with a new database; the website is served from <folder>/htdocs.', 'staged' => true],
        'akaunting' => ['title' => 'Akaunting', 'description' => 'Free online accounting and invoicing. Staged with a new database; finish in its web installer.', 'staged' => true],
        'tutor' => ['title' => 'Tutor LMS', 'description' => 'WordPress with the Tutor LMS plugin installed and activated, with a new database and an administrator account.'],
        'learnpress' => ['title' => 'LearnPress', 'description' => 'WordPress with the LearnPress plugin installed and activated, with a new database and an administrator account.'],
        'lifterlms' => ['title' => 'LifterLMS', 'description' => 'WordPress with the LifterLMS plugin installed and activated, with a new database and an administrator account.'],
        'bbpress' => ['title' => 'bbPress', 'description' => 'WordPress with the bbPress plugin installed and activated, with a new database and an administrator account.'],
        'buddypress' => ['title' => 'BuddyPress', 'description' => 'WordPress with the BuddyPress plugin installed and activated, with a new database and an administrator account.'],
        'easy-digital-downloads' => ['title' => 'Easy Digital Downloads', 'description' => 'WordPress with the Easy Digital Downloads plugin installed and activated, with a new database and an administrator account.'],
        'wp-statistics' => ['title' => 'WP Statistics', 'description' => 'WordPress with the WP Statistics plugin installed and activated, with a new database and an administrator account.'],
        'koko-analytics' => ['title' => 'Koko Analytics', 'description' => 'WordPress with the Koko Analytics plugin installed and activated, with a new database and an administrator account.'],
        'download-monitor' => ['title' => 'Download Monitor', 'description' => 'WordPress with the Download Monitor plugin installed and activated, with a new database and an administrator account.'],
        'piwigo' => ['title' => 'Piwigo', 'description' => 'Photo gallery. Staged with a new database; finish in its web installer.', 'staged' => true],
        'mautic' => ['title' => 'Mautic', 'description' => 'Marketing automation, installed with Composer. The website is served from <folder>/docroot.', 'staged' => true],
        'symfony' => ['title' => 'Symfony', 'description' => 'PHP framework skeleton via Composer; no database is created. Served from <folder>/public.', 'staged' => true],
        'codeigniter' => ['title' => 'CodeIgniter 4', 'description' => 'Lightweight PHP framework starter via Composer; no database is created. Served from <folder>/public.', 'staged' => true],
        'yii' => ['title' => 'Yii 2', 'description' => 'PHP framework starter via Composer; no database is created. Served from <folder>/web.', 'staged' => true],
        'cakephp' => ['title' => 'CakePHP', 'description' => 'PHP framework starter via Composer; no database is created. Served from <folder>/webroot.', 'staged' => true],
        'slim' => ['title' => 'Slim', 'description' => 'Micro-framework skeleton via Composer; no database is created. Served from <folder>/public.', 'staged' => true],
        'kirby' => ['title' => 'Kirby', 'description' => 'Flat-file CMS via Composer — no database. Create your account at /panel.', 'staged' => true],
        'craft' => ['title' => 'Craft CMS', 'description' => 'Flexible content platform via Composer, with a new database. Served from <folder>/web.', 'staged' => true],
        'invoiceninja' => ['title' => 'Invoice Ninja', 'description' => 'Invoicing and payments. Staged with a new database; served from <folder>/public; finish in its setup page.', 'staged' => true],
        'facturascripts' => ['title' => 'FacturaScripts', 'description' => 'Accounting and ERP. Staged with a new database; finish in its web installer.', 'staged' => true],
        'flarum' => ['title' => 'Flarum', 'description' => 'Modern, fast forum software, installed with Composer. The website is served from <folder>/public.', 'staged' => true],
        'typo3' => ['title' => 'TYPO3', 'description' => 'Enterprise content management, installed with Composer. The website is served from <folder>/public.', 'staged' => true],
        'concrete' => ['title' => 'Concrete CMS', 'description' => 'In-context editing CMS, installed with Composer. The website is served from <folder>/public.', 'staged' => true],
        'bagisto' => ['title' => 'Bagisto', 'description' => 'Laravel e-commerce platform, installed with Composer. The website is served from <folder>/public.', 'staged' => true],
        'shopware' => ['title' => 'Shopware Community Edition', 'description' => 'E-commerce platform, installed with Composer. The website is served from <folder>/public.', 'staged' => true],
    ];

    /** Apps that are WordPress plus plugins: the slug => the plugins installed and activated. */
    public const WORDPRESS_BASED = [
        'woocommerce' => ['woocommerce'],
        'tutor' => ['tutor'],
        'learnpress' => ['learnpress'],
        'lifterlms' => ['lifterlms'],
        'bbpress' => ['bbpress'],
        'buddypress' => ['buddypress'],
        'easy-digital-downloads' => ['easy-digital-downloads'],
        'wp-statistics' => ['wp-statistics'],
        'koko-analytics' => ['koko-analytics'],
        'download-monitor' => ['download-monitor'],
    ];

    /**
     * Natively deployed applications: a program supervised as a worker service
     * behind the website's reverse proxy. runtime is what the website must be
     * (binary: a reverse-proxy website; python: a Python website).
     */
    public const NATIVE = [
        'gitea' => ['runtime' => 'binary', 'finish' => '/', 'name' => 'gitea'],
        'django' => ['runtime' => 'python', 'finish' => '/admin/', 'name' => 'django', 'admin' => true],
        'fastapi' => ['runtime' => 'python', 'finish' => '/docs', 'name' => 'fastapi'],
    ];

    /**
     * Whether an application can be installed on a website (or subdomain) of
     * this kind: PHP applications need a PHP website, a program needs a plain
     * reverse-proxy website, a Python application a Python website.
     *
     * @param array<string, mixed> $site needs kind, php_version and app_runtime
     */
    public static function compatible(array $site, string $app): bool
    {
        $kind = (string) ($site['kind'] ?? '');
        $runtime = (string) ($site['app_runtime'] ?? '');
        return match (self::NATIVE[$app]['runtime'] ?? 'php') {
            'binary' => $kind === 'proxy' && $runtime === '',
            'python' => $kind === 'proxy' && str_starts_with($runtime, 'python:'),
            default => $kind === 'php' && (string) ($site['php_version'] ?? '') !== '',
        };
    }

    /** What an application needs the website to be, in words. */
    public static function needs(string $app): string
    {
        return match (self::NATIVE[$app]['runtime'] ?? 'php') {
            'binary' => 'a reverse-proxy website',
            'python' => 'a Python website',
            default => 'a PHP website',
        };
    }

    /** Staged apps: where their web installer lives and the web root subfolder. */
    public const STAGED = [
        'joomla' => ['finish' => '/installation/', 'docroot' => ''],
        'drupal' => ['finish' => '/core/install.php', 'docroot' => ''],
        'phpbb' => ['finish' => '/install/', 'docroot' => ''],
        'smf' => ['finish' => '/install.php', 'docroot' => ''],
        'prestashop' => ['finish' => '/index.php', 'docroot' => ''],
        'opencart' => ['finish' => '/install/', 'docroot' => 'upload'],
        'abantecart' => ['finish' => '/install/', 'docroot' => 'public_html'],
        'nextcloud' => ['finish' => '/index.php', 'docroot' => ''],
        'moodle' => ['finish' => '/install.php', 'docroot' => ''],
        'matomo' => ['finish' => '/index.php', 'docroot' => ''],
        'mediawiki' => ['finish' => '/mw-config/index.php', 'docroot' => ''],
        'grav' => ['finish' => '/', 'docroot' => ''],
        'backdrop' => ['finish' => '/core/install.php', 'docroot' => ''],
        'processwire' => ['finish' => '/install.php', 'docroot' => ''],
        'bludit' => ['finish' => '/', 'docroot' => ''],
        'dolibarr' => ['finish' => '/install/', 'docroot' => 'htdocs'],
        'akaunting' => ['finish' => '/', 'docroot' => ''],
        'piwigo' => ['finish' => '/install.php', 'docroot' => ''],
        'mautic' => ['finish' => '/installer', 'docroot' => 'docroot'],
        'symfony' => ['finish' => '/', 'docroot' => 'public'],
        'codeigniter' => ['finish' => '/', 'docroot' => 'public'],
        'yii' => ['finish' => '/', 'docroot' => 'web'],
        'cakephp' => ['finish' => '/', 'docroot' => 'webroot'],
        'slim' => ['finish' => '/', 'docroot' => 'public'],
        'kirby' => ['finish' => '/panel', 'docroot' => ''],
        'craft' => ['finish' => '/admin/install', 'docroot' => 'web'],
        'invoiceninja' => ['finish' => '/setup', 'docroot' => 'public'],
        'facturascripts' => ['finish' => '/', 'docroot' => ''],
        'flarum' => ['finish' => '/', 'docroot' => 'public'],
        'typo3' => ['finish' => '/typo3/install.php', 'docroot' => 'public'],
        'concrete' => ['finish' => '/index.php/install', 'docroot' => 'public'],
        'bagisto' => ['finish' => '/install', 'docroot' => 'public'],
        'shopware' => ['finish' => '/installer', 'docroot' => 'public'],
    ];

    /** @param array<string, mixed> $site */
    public static function finishUrl(array $site, string $app): string
    {
        $scheme = $site['https'] ? 'https://' : 'http://';
        if (isset(self::NATIVE[$app])) {
            return $scheme . $site['domain'] . self::NATIVE[$app]['finish'];
        }
        if (!isset(self::STAGED[$app])) {
            return '';
        }
        return $scheme . $site['domain'] . self::STAGED[$app]['finish'];
    }

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly DatabaseService $databases,
        private readonly DatabaseRepository $dbRepo,
        private readonly WebsiteService $websites,
        private readonly \NimVor\Repositories\WebsiteRepository $sites,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly AppCatalogService $catalog,
        private readonly WorkerService $workers,
    ) {
    }

    /** @param array<string, mixed> $site */
    public static function relativeDocroot(array $site): string
    {
        $root = rtrim((string) $site['root'], '/') . '/';
        $doc = (string) $site['document_root'];
        return str_starts_with($doc, $root) ? substr($doc, strlen($root)) : 'public_html';
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $input
     * @param array<string, mixed> $actor
     * @return array{job: string, admin_user?: string, admin_password?: string}
     */
    public function install(array $site, string $app, array $input, array $actor): array
    {
        if (!$this->catalog->installableBy($app, $actor)) {
            throw new UserFacingException('This application is not available for installation.');
        }
        $catalogRow = $this->catalog->find($app);
        $appName = (string) ($catalogRow['name'] ?? (self::APPS[$app]['title'] ?? $app));
        $needsDb = (int) ($catalogRow['needs_db'] ?? 1) === 1;
        if (!self::compatible($site, $app)) {
            throw new UserFacingException($appName . ' needs ' . self::needs($app) . '.', '', ((self::NATIVE[$app]['runtime'] ?? 'php') === 'php') ? 'Change the website type to PHP under Settings first.' : 'Choose or create a website of that type first.');
        }
        $errors = [];
        $wpPlugins = self::WORDPRESS_BASED[$app] ?? null;
        $params = [
            'app' => $wpPlugins !== null ? 'wordpress' : $app, 'domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'],
            'php_version' => $site['php_version'], 'url' => ($site['https'] ? 'https://' : 'http://') . $site['domain'],
        ];
        $strategy = $wpPlugins !== null ? 'wordpress-plugin' : ($app === 'wordpress' ? 'wordpress' : ($app === 'laravel' ? 'laravel' : (isset(self::STAGED[$app]) ? 'archive' : '')));
        $params['allow_testing'] = \NimVor\Hosting\Scope::isAll($actor);
        $adminPassword = null;
        $native = self::NATIVE[$app] ?? null;
        if ($native !== null) {
            // A program supervised as a service behind this website's reverse
            // proxy: the port is the one the website already points at.
            $strategy = $native['runtime'] === 'binary' ? 'binary-service' : 'python-venv';
            $params['path'] = trim((string) ($input['path'] ?? 'app'), " /");
            if (!preg_match('#^[A-Za-z0-9._-]{1,60}$#', $params['path']) || in_array($params['path'], ['logs', 'tmp', 'backups', '.ssh', '.', '..'], true)) {
                $errors['path'] = 'Choose a new folder name, e.g. app.';
            }
            if (!preg_match('/:(\d+)/', (string) $site['proxy_pass'], $pm)) {
                throw new UserFacingException($appName . ' needs this website to forward to a local port.', '', 'Set the website\'s application port under Runtime first.');
            }
            $params['port'] = (int) $pm[1];
            $params['runtime'] = (string) $site['app_runtime'];
            $params['service_slug'] = $this->workers->reserveSlug($site, $native['name']);
            if ((int) $this->db->scalar("SELECT COUNT(*) FROM app_installs WHERE website_id = ? AND strategy IN ('binary-service', 'python-venv') AND status IN ('running', 'succeeded')", [(int) $site['id']]) > 0) {
                throw new UserFacingException('This website already runs an application behind its reverse proxy.', '', 'Use another reverse-proxy or Python website for ' . $appName . '.');
            }
            if (!empty($native['admin'])) {
                $params['admin_user'] = trim((string) ($input['admin_user'] ?? 'admin'));
                $params['admin_email'] = trim((string) ($input['admin_email'] ?? ''));
                if (!preg_match('/^[A-Za-z0-9._@-]{3,60}$/', $params['admin_user'])) {
                    $errors['admin_user'] = 'Use 3–60 letters, digits or . _ @ -';
                }
                if (!filter_var($params['admin_email'], FILTER_VALIDATE_EMAIL)) {
                    $errors['admin_email'] = 'Enter the administrator\'s email address.';
                }
                $adminPassword = Crypto::password(20);
                $params['admin_password'] = $adminPassword;
            }
        } elseif ($app === 'wordpress' || $wpPlugins !== null) {
            if ($wpPlugins !== null) {
                $params['plugins'] = $wpPlugins;
            }
            $params['path'] = self::relativeDocroot($site);
            $params['title'] = trim((string) ($input['title'] ?? '')) ?: $site['domain'];
            $params['admin_user'] = trim((string) ($input['admin_user'] ?? 'admin'));
            $params['admin_email'] = trim((string) ($input['admin_email'] ?? ''));
            if (!preg_match('/^[A-Za-z0-9._@-]{3,60}$/', $params['admin_user'])) {
                $errors['admin_user'] = 'Use 3–60 letters, digits or . _ @ -';
            }
            if (!filter_var($params['admin_email'], FILTER_VALIDATE_EMAIL)) {
                $errors['admin_email'] = 'Enter the administrator\'s email address.';
            }
            if (mb_strlen($params['title']) > 100) {
                $errors['title'] = 'Keep the title under 100 characters.';
            }
            $adminPassword = Crypto::password(20);
            $params['admin_password'] = $adminPassword;
        } elseif (isset(self::STAGED[$app])) {
            // Staged apps install into the document root; their own web
            // installer finishes setup with the database created below.
            $params['path'] = self::relativeDocroot($site);
        } else {
            $params['path'] = trim((string) ($input['path'] ?? 'app'), " /");
            if (!preg_match('#^[A-Za-z0-9._-]{1,60}$#', $params['path']) || in_array($params['path'], ['logs', 'tmp', 'backups', '.ssh', '.', '..'], true)) {
                $errors['path'] = 'Choose a new folder name, e.g. app.';
            }
            $params['migrate'] = filter_var($input['migrate'] ?? false, FILTER_VALIDATE_BOOL);
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }

        // Claim the folder first: at most one installation per website folder
        // runs at a time (a unique index backs this up), so a repeated request
        // creates neither a second database nor a second job.
        try {
            $this->db->run(
                "INSERT INTO app_installs (website_id, app, path, status, strategy, user_id, created_at) VALUES (?, ?, ?, 'running', ?, ?, ?)",
                [(int) $site['id'], $params['app'], $params['path'], $strategy, (int) $actor['id'], gmdate('c')]
            );
        } catch (\PDOException $e) {
            throw new UserFacingException('An installation into this website is already in progress.', '', 'Wait for it to finish, then check the result below.');
        }
        $claimId = (int) $this->db->scalar('SELECT MAX(id) FROM app_installs WHERE website_id = ? AND path = ?', [(int) $site['id'], $params['path']]);
        $release = function (string $why) use ($claimId): void {
            $this->db->run("UPDATE app_installs SET status = 'failed', error = ?, finished_at = ? WHERE id = ? AND status = 'running'", [mb_substr($why, 0, 500), gmdate('c'), $claimId]);
        };

        // A database and user for the application (password kept encrypted
        // so it can be looked up later under Databases). Flat-file apps
        // (e.g. Grav) need none.
        $created = null;
        $database = null;
        try {
            if ($needsDb) {
                $base = substr(preg_replace('/[^a-z0-9]/', '', strtolower(explode('.', (string) $site['domain'])[0])) ?: 'site', 0, 10) . ($app === 'wordpress' || $wpPlugins !== null ? '_wp' : '_app');
                for ($i = 0; $i < 10 && $created === null; $i++) {
                    $name = $i === 0 ? $base : $base . $i;
                    try {
                        $created = $this->databases->create(['name' => $name, 'create_user' => true, 'username' => $name, 'store_password' => true, 'website_id' => (int) $site['id']], $actor);
                    } catch (ValidationException $e) {
                        if (!isset($e->errors()['name']) && !isset($e->errors()['username'])) {
                            throw $e;
                        }
                    }
                }
                if ($created === null) {
                    throw new UserFacingException('No free database name was found for the application.');
                }
                $database = $this->dbRepo->database($this->server->id(), (int) $created['database_id'], null);
                $params['db'] = ['name' => $database['name'], 'user' => $created['user'], 'password' => $created['password'], 'host' => 'localhost'];
            }
            $res = (array) $this->agent->call('apps.install', $params);
        } catch (\Throwable $e) {
            // Nothing was installed: remove only what this request created
            // and free the folder for another attempt.
            if ($database !== null) {
                try {
                    $this->databases->drop((array) $database, (string) $database['name']);
                    foreach ($this->dbRepo->users($this->server->id(), $actor) as $u) {
                        if ($u['username'] === $created['user']) {
                            $this->databases->dropUser($u);
                        }
                    }
                } catch (\Throwable) {
                }
            }
            $release($e->getMessage());
            throw $e;
        }
        $job = (string) $res['job_id'];
        $this->db->run(
            'UPDATE app_installs SET database_id = ?, admin_user = ?, job_id = ? WHERE id = ?',
            [$database !== null ? (int) $database['id'] : null, $params['admin_user'] ?? '', $job, $claimId]
        );
        $this->audit->record('app.install', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'app' => $app, 'path' => $params['path'], 'database' => $database !== null ? $database['name'] : '', 'strategy' => $strategy]);
        $out = ['job' => $job];
        if ($adminPassword !== null) {
            $out += ['admin_user' => $params['admin_user'], 'admin_password' => $adminPassword];
        }
        return $out;
    }

    /**
     * The dry run: what installing $app into $site would require and change,
     * worked out by the agent without changing anything.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $actor
     * @return array<string, mixed>
     */
    public function plan(array $site, string $app, array $actor): array
    {
        if (!$this->catalog->installableBy($app, $actor)) {
            throw new UserFacingException('This application is not available for installation.');
        }
        $row = $this->catalog->find($app);
        if (!self::compatible($site, $app)) {
            throw new UserFacingException((string) ($row['name'] ?? $app) . ' needs ' . self::needs($app) . '.', '', ((self::NATIVE[$app]['runtime'] ?? 'php') === 'php') ? 'Change the website type to PHP under Settings first.' : 'Choose or create a website of that type first.');
        }
        $wp = self::WORDPRESS_BASED[$app] ?? null;
        $params = [
            'app' => $wp !== null ? 'wordpress' : $app, 'domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'],
            'php_version' => $site['php_version'], 'url' => ($site['https'] ? 'https://' : 'http://') . $site['domain'],
            'path' => self::relativeDocroot($site), 'allow_testing' => \NimVor\Hosting\Scope::isAll($actor),
        ];
        if ($wp !== null) {
            $params['plugins'] = $wp;
        }
        $native = self::NATIVE[$app] ?? null;
        if ($native !== null) {
            $params['path'] = 'app';
            $params['port'] = preg_match('/:(\d+)/', (string) $site['proxy_pass'], $pm) ? (int) $pm[1] : 0;
            $params['runtime'] = (string) $site['app_runtime'];
            $params['service_slug'] = $this->workers->reserveSlug($site, $native['name']);
            if (!empty($native['admin'])) {
                $params += ['admin_user' => 'admin', 'admin_email' => 'admin@example.com', 'admin_password' => 'plan-only-not-used-0000'];
            }
        }
        if ($app === 'wordpress' || $wp !== null) {
            $params += ['title' => $site['domain'], 'admin_user' => 'admin', 'admin_email' => 'admin@example.com', 'admin_password' => 'plan-only-not-used-0000'];
        } elseif ($app === 'laravel') {
            $params['path'] = 'app';
        }
        if ((int) ($row['needs_db'] ?? 1) === 1) {
            // Only names for the plan; nothing is created.
            $params['db'] = ['name' => 'plan_preview', 'user' => 'plan_preview', 'password' => 'plan-only-not-used-0000', 'host' => 'localhost'];
        }
        return (array) $this->agent->call('apps.plan', $params);
    }

    /**
     * Update running installations; point Laravel sites at public/ once done.
     *
     * @param array<string, mixed> $site
     */
    public function refresh(array $site): void
    {
        foreach ($this->db->fetchAll("SELECT * FROM app_installs WHERE website_id = ? AND status = 'running'", [(int) $site['id']]) as $i) {
            try {
                $job = (array) $this->agent->call('jobs.get', ['id' => $i['job_id']]);
            } catch (AgentException $e) {
                if ($e->errorCode() === 'not_found') {
                    $this->db->run("UPDATE app_installs SET status = 'unknown', finished_at = ? WHERE id = ?", [gmdate('c'), (int) $i['id']]);
                }
                continue;
            }
            $status = (string) ($job['status'] ?? '');
            if ($status !== 'succeeded' && $status !== 'failed') {
                continue;
            }
            $version = mb_substr((string) (((array) ($job['result'] ?? []))['version'] ?? ''), 0, 40);
            $error = mb_substr((string) ($job['error'] ?? ''), 0, 500);
            $this->db->run('UPDATE app_installs SET status = ?, error = ?, version = ?, finished_at = ? WHERE id = ?', [$status, $error, $version, gmdate('c'), (int) $i['id']]);
            if ($status === 'succeeded' && is_array(((array) ($job['result'] ?? []))['service'] ?? null)) {
                // A native application's service was created by the agent: list it with the website's workers.
                $this->workers->adopt($site, (array) $job['result']['service']);
            }
            $this->audit->record('app.install_result', 'website', (string) $site['id'], $status === 'succeeded' ? AuditLogger::SUCCESS : AuditLogger::FAILURE, ['domain' => $site['domain'], 'app' => $i['app'], 'path' => $i['path'], 'version' => $version, 'error' => $status === 'failed' ? mb_substr($error, 0, 300) : '']);
            // Point the website at the app's web root when it is a subfolder
            // (Laravel's public/, OpenCart's upload/, AbanteCart's public_html/).
            $sub = $i['app'] === 'laravel' ? 'public' : (string) (self::STAGED[$i['app']]['docroot'] ?? '');
            if ($status === 'succeeded' && $sub !== '') {
                $fresh = $this->sites->find((int) $site['server_id'], (int) $site['id'], null);
                if ($fresh !== null) {
                    $this->websites->patch($fresh, ['document_root' => rtrim((string) $fresh['root'], '/') . '/' . $i['path'] . '/' . $sub], 'app.document_root');
                }
            }
        }
    }

    /**
     * @param array<string, mixed> $site
     * @return list<array<string, mixed>>
     */
    public function history(array $site): array
    {
        return $this->db->fetchAll('SELECT a.*, d.name AS database_name FROM app_installs a LEFT JOIN databases d ON d.id = a.database_id WHERE a.website_id = ? ORDER BY a.id DESC', [(int) $site['id']]);
    }
}
