# WhaCP Route Matrix

_Generated from `panel/routes/web.php` at baseline. One row per registered route._

Total routes: 551

| Method | Path | Controller::method | Name | Permission | Auth | CSRF | Ported |
|---|---|---|---|---|---|---|---|
| GET | `/` | DashboardController::index | dashboard | dashboard.view | yes | yes | ☑ |
| GET | `/accounts/bandwidth` | AccountAdminController::bandwidth | accounts.bandwidth | users.manage | yes | yes | ☐ |
| GET | `/accounts/new` | AccountsController::create | accounts.create | users.manage | yes | yes | ☐ |
| POST | `/accounts/terminate/confirm` | AccountAdminController::confirmTerminate | accounts.terminate.confirm | users.manage | yes | yes | ☐ |
| POST | `/accounts/terminate` | AccountAdminController::terminate | accounts.terminate | users.manage | yes | yes | ☐ |
| POST | `/accounts` | AccountsController::store | accounts.store | users.manage | yes | yes | ☐ |
| POST | `/admin/plugins/{name}/toggle` | PluginsController::toggle | plugins.toggle | plugins.manage | yes | yes | ☐ |
| GET | `/admin/plugins` | PluginsController::index | plugins | plugins.manage | yes | yes | ☐ |
| GET | `/api/v1/databases` | ApiController::listDatabases | api.databases | databases.view | yes | yes | ☐ |
| POST | `/api/v1/databases` | ApiController::createDatabase | api.databases.create | databases.manage | yes | yes | ☐ |
| GET | `/api/v1/jobs/{id}` | ApiController::job | api.jobs |  | yes | yes | ☐ |
| GET | `/api/v1/me` | ApiController::me | api.me |  | yes | yes | ☐ |
| POST | `/api/v1/notifications/read` | ApiController::readNotifications | api.notifications.read | notifications.view | yes | yes | ☐ |
| GET | `/api/v1/notifications` | ApiController::listNotifications | api.notifications | notifications.view | yes | yes | ☐ |
| GET | `/api/v1/openapi.yaml` | ApiController::openapi | api.openapi |  | no | yes | ☐ |
| GET | `/api/v1/server` | ApiController::serverInfo | api.server | server.view | yes | yes | ☐ |
| GET | `/api/v1/websites/{id}/backups` | ApiController::listBackups | api.backups | backups.view | yes | yes | ☐ |
| POST | `/api/v1/websites/{id}/backups` | ApiController::createBackup | api.backups.create | backups.manage | yes | yes | ☐ |
| POST | `/api/v1/websites/{id}/deploy` | ApiController::deployWebsite | api.deploy | deploy.manage | yes | yes | ☐ |
| POST | `/api/v1/websites/{id}/disable` | ApiController::disableWebsite | api.websites.disable | websites.manage | yes | yes | ☐ |
| POST | `/api/v1/websites/{id}/enable` | ApiController::enableWebsite | api.websites.enable | websites.manage | yes | yes | ☐ |
| POST | `/api/v1/websites/{id}/ssl` | ApiController::issueSsl | api.websites.ssl | ssl.manage | yes | yes | ☐ |
| DELETE | `/api/v1/websites/{id}` | ApiController::deleteWebsite | api.websites.delete | websites.manage | yes | yes | ☐ |
| GET | `/api/v1/websites/{id}` | ApiController::showWebsite | api.websites.show | websites.view | yes | yes | ☐ |
| GET | `/api/v1/websites` | ApiController::listWebsites | api.websites | websites.view | yes | yes | ☐ |
| POST | `/api/v1/websites` | ApiController::createWebsite | api.websites.create | websites.manage | yes | yes | ☐ |
| POST | `/apps-manager/app` | AppManagerController::app | apps.manager.app | settings.manage | yes | yes | ☐ |
| POST | `/apps-manager/category` | AppManagerController::category | apps.manager.category | settings.manage | yes | yes | ☐ |
| POST | `/apps-manager/installer` | AppManagerController::installer | apps.manager.installer | settings.manage | yes | yes | ☐ |
| GET | `/apps-manager` | AppManagerController::index | apps.manager | settings.view | yes | yes | ☐ |
| GET | `/audit` | AuditLogController::index | audit | audit.view | yes | yes | ☑ |
| POST | `/backups/accounts/delete` | AccountBackupsController::delete | backups.accounts.delete | backups.manage | yes | yes | ☐ |
| GET | `/backups/accounts/download` | AccountBackupsController::download | backups.accounts.download | backups.manage | yes | yes | ☐ |
| GET | `/backups/accounts/restore` | AccountBackupsController::preview | backups.accounts.preview | backups.manage | yes | yes | ☐ |
| POST | `/backups/accounts/restore` | AccountBackupsController::start | backups.accounts.restore | users.manage | yes | yes | ☐ |
| POST | `/backups/accounts/restores/{id}/recreate` | AccountBackupsController::recreate | backups.accounts.recreate | users.manage | yes | yes | ☐ |
| GET | `/backups/accounts/restores/{id}` | AccountBackupsController::show | backups.accounts.restores | backups.manage | yes | yes | ☐ |
| POST | `/backups/accounts/upload` | AccountBackupsController::upload | backups.accounts.upload | backups.manage | yes | yes | ☐ |
| POST | `/backups/accounts` | AccountBackupsController::create | backups.accounts.create | backups.manage | yes | yes | ☐ |
| POST | `/backups/key/download` | BackupsController::downloadKey | backups.key.download | backups.manage | yes | yes | ☐ |
| POST | `/backups/key/import` | BackupsController::importKey | backups.key.import | backups.manage | yes | yes | ☐ |
| POST | `/backups/schedules/{schedule}/delete` | BackupsController::deleteServerSchedule | backups.schedules.delete | backups.manage | yes | yes | ☐ |
| POST | `/backups/schedules` | BackupsController::storeServerSchedule | backups.schedules.store | backups.manage | yes | yes | ☐ |
| POST | `/backups/server/{backup}/delete` | BackupsController::deleteServer | backups.server.delete | backups.manage | yes | yes | ☐ |
| GET | `/backups/server/{backup}/download` | BackupsController::downloadServer | backups.server.download | backups.manage | yes | yes | ☐ |
| POST | `/backups/server/{backup}/restore` | BackupsController::restoreServer | backups.server.restore | server.manage | yes | yes | ☐ |
| POST | `/backups/server` | BackupsController::createServer | backups.server | backups.manage | yes | yes | ☐ |
| GET | `/backups/storages/confirm` | BackupsController::confirmStorage | backups.storages.confirm | backups.manage | yes | yes | ☐ |
| POST | `/backups/storages/confirm` | BackupsController::trustStorage | backups.storages.trust | backups.manage | yes | yes | ☐ |
| GET | `/backups/storages/google/callback` | BackupsController::googleCallback | backups.storages.google | backups.manage | yes | yes | ☐ |
| POST | `/backups/storages/{storage}/active` | BackupsController::activateStorage | backups.storages.active | backups.manage | yes | yes | ☐ |
| POST | `/backups/storages/{storage}/delete` | BackupsController::deleteStorage | backups.storages.delete | backups.manage | yes | yes | ☐ |
| POST | `/backups/storages/{storage}/scan` | BackupsController::scanStorage | backups.storages.scan | backups.manage | yes | yes | ☐ |
| POST | `/backups/storages/{storage}/test` | BackupsController::testStorage | backups.storages.test | backups.manage | yes | yes | ☐ |
| POST | `/backups/storages/{storage}` | BackupsController::updateStorage | backups.storages.update | backups.manage | yes | yes | ☐ |
| POST | `/backups/storages` | BackupsController::storeStorage | backups.storages.store | backups.manage | yes | yes | ☐ |
| GET | `/backups` | BackupsController::index | backups | backups.view | yes | yes | ☐ |
| POST | `/cloudflare/always-https` | CloudflareController::alwaysHttps | cloudflare.always_https | server.manage | yes | yes | ☐ |
| POST | `/cloudflare/connect` | CloudflareController::connect | cloudflare.connect | server.manage | yes | yes | ☐ |
| POST | `/cloudflare/disconnect` | CloudflareController::disconnect | cloudflare.disconnect | server.manage | yes | yes | ☐ |
| POST | `/cloudflare/proxy` | CloudflareController::proxy | cloudflare.proxy | server.manage | yes | yes | ☐ |
| POST | `/cloudflare/purge` | CloudflareController::purge | cloudflare.purge | server.manage | yes | yes | ☐ |
| POST | `/cloudflare/ssl` | CloudflareController::ssl | cloudflare.ssl | server.manage | yes | yes | ☐ |
| GET | `/cloudflare` | CloudflareController::index | cloudflare | server.view | yes | yes | ☐ |
| POST | `/components/profile` | ComponentsController::profile | components.profile | server.manage | yes | yes | ☐ |
| POST | `/components/{id}/{step}` | ComponentsController::fix | components.fix | server.view | yes | yes | ☐ |
| GET | `/components` | ComponentsController::index | components | server.view | yes | yes | ☐ |
| GET | `/confirm-password` | AuthController::confirmForm | auth.confirm |  | yes | yes | ☐ |
| POST | `/confirm-password` | AuthController::confirm | auth.confirm.submit |  | yes | yes | ☐ |
| GET | `/cron` | DiscoveryController::cron | cron | cron.view | yes | yes | ☐ |
| GET | `/dashboard/live` | DashboardController::live | dashboard.live | dashboard.view | yes | yes | ☐ |
| POST | `/database-users/{id}/assign` | DatabasesController::assign | dbusers.assign | databases.manage | yes | yes | ☑ |
| POST | `/database-users/{id}/delete` | DatabasesController::deleteUser | dbusers.delete | databases.manage | yes | yes | ☑ |
| POST | `/database-users/{id}/hosts` | DatabasesController::addHost | dbusers.host | databases.manage | yes | yes | ☑ |
| POST | `/database-users/{id}/password` | DatabasesController::password | dbusers.password | databases.manage | yes | yes | ☑ |
| POST | `/database-users/{id}/reveal` | DatabasesController::reveal | dbusers.reveal | databases.manage | yes | yes | ☑ |
| POST | `/database-users` | DatabasesController::createUser | dbusers.store | databases.manage | yes | yes | ☑ |
| POST | `/databases/browse/api` | DbBrowserController::serverApi | databases.browser.server_api | databases.manage | yes | yes | ☑ |
| GET | `/databases/browse` | DbBrowserController::serverBrowse | databases.browser.server | databases.manage | yes | yes | ☑ |
| GET | `/databases/server/connections` | DbServerController::connections | databases.server.connections | server.view | yes | yes | ☑ |
| POST | `/databases/server/flush` | DbServerController::flush | databases.server.flush | server.manage | yes | yes | ☑ |
| POST | `/databases/server/kill` | DbServerController::kill | databases.server.kill | server.manage | yes | yes | ☑ |
| GET | `/databases/server/logs` | DbServerController::logs | databases.server.logs | logs.view | yes | yes | ☑ |
| POST | `/databases/server/restart` | DbServerController::restart | databases.server.restart | services.manage | yes | yes | ☑ |
| GET | `/databases/server/settings` | DbServerController::settingsPage | databases.server.settings_page | server.view | yes | yes | ☑ |
| POST | `/databases/server/settings` | DbServerController::saveSettings | databases.server.settings | server.manage | yes | yes | ☑ |
| GET | `/databases/server` | DbServerController::index | databases.server | server.view | yes | yes | ☑ |
| POST | `/databases/{id}/browse/api` | DbBrowserController::api | databases.browser.api | databases.manage | yes | yes | ☑ |
| GET | `/databases/{id}/browse` | DbBrowserController::browse | databases.browser | databases.manage | yes | yes | ☑ |
| POST | `/databases/{id}/drop` | DatabasesController::drop | databases.drop | databases.manage | yes | yes | ☑ |
| POST | `/databases/{id}/dump` | DatabasesController::dump | databases.dump | databases.manage | yes | yes | ☑ |
| POST | `/databases/{id}/grant` | DatabasesController::grant | databases.grant | databases.manage | yes | yes | ☑ |
| POST | `/databases/{id}/import` | DatabasesController::import | databases.import | databases.manage | yes | yes | ☑ |
| GET | `/databases` | DatabasesController::index | databases | databases.view | yes | yes | ☑ |
| POST | `/databases` | DatabasesController::store | databases.store | databases.manage | yes | yes | ☑ |
| POST | `/discovery/run` | DiscoveryController::run | discovery.run | discovery.run | yes | yes | ☐ |
| GET | `/discovery` | DiscoveryController::index | discovery | websites.view | yes | yes | ☐ |
| POST | `/dns/install` | DnsController::install | dns.install | packages.manage | yes | yes | ☐ |
| POST | `/dns/setup` | DnsController::setup | dns.setup | server.manage | yes | yes | ☐ |
| POST | `/dns/zones/{id}/bulk` | DnsController::bulk | dns.bulk | dns.manage | yes | yes | ☐ |
| POST | `/dns/zones/{id}/delete` | DnsController::delete | dns.delete | dns.manage | yes | yes | ☐ |
| GET | `/dns/zones/{id}/export` | DnsController::export | dns.export | dns.manage | yes | yes | ☐ |
| POST | `/dns/zones/{id}/import` | DnsController::import | dns.import | dns.manage | yes | yes | ☐ |
| POST | `/dns/zones/{id}/records/{record}/delete` | DnsController::deleteRecord | dns.records.delete | dns.manage | yes | yes | ☐ |
| POST | `/dns/zones/{id}/records/{record}` | DnsController::updateRecord | dns.records.update | dns.manage | yes | yes | ☐ |
| POST | `/dns/zones/{id}/records` | DnsController::addRecord | dns.records.store | dns.manage | yes | yes | ☐ |
| POST | `/dns/zones/{id}/settings` | DnsController::settings | dns.settings | dns.manage | yes | yes | ☐ |
| POST | `/dns/zones/{id}/sync` | DnsController::sync | dns.sync | dns.manage | yes | yes | ☐ |
| GET | `/dns/zones/{id}` | DnsController::show | dns.zone | dns.manage | yes | yes | ☐ |
| POST | `/dns/zones` | DnsController::store | dns.store | dns.manage | yes | yes | ☐ |
| GET | `/dns` | DnsController::index | dns | dns.manage | yes | yes | ☑ |
| GET | `/domains` | DomainsController::index | domains | websites.view | yes | yes | ☐ |
| POST | `/features/{id}/delete` | FeaturesController::destroy | features.delete | plans.manage | yes | yes | ☐ |
| GET | `/features` | FeaturesController::index | features | plans.manage | yes | yes | ☐ |
| POST | `/features` | FeaturesController::store | features.store | plans.manage | yes | yes | ☐ |
| POST | `/ffmpeg/apply` | FfmpegController::apply | ffmpeg.apply | server.manage | yes | yes | ☐ |
| POST | `/ffmpeg/install` | FfmpegController::install | ffmpeg.install | packages.manage | yes | yes | ☐ |
| POST | `/ffmpeg/uninstall` | FfmpegController::uninstall | ffmpeg.uninstall | packages.manage | yes | yes | ☐ |
| GET | `/ffmpeg` | FfmpegController::index | ffmpeg | server.view | yes | yes | ☐ |
| POST | `/ftp/configure` | FtpController::configure | ftp.configure | server.manage | yes | yes | ☐ |
| POST | `/ftp/disable` | FtpController::disable | ftp.disable | server.manage | yes | yes | ☐ |
| POST | `/ftp/install` | FtpController::install | ftp.install | packages.manage | yes | yes | ☐ |
| GET | `/ftp` | FtpController::index | ftp | server.manage | yes | yes | ☐ |
| GET | `/healthz` | HealthController::show | health |  | no | yes | ☐ |
| POST | `/hooks/deploy/{token}` | DeployController::webhook | hooks.deploy |  | no | no | ☐ |
| GET | `/ips/migrate` | IpController::migrate | ips.migrate | server.view | yes | yes | ☐ |
| POST | `/ips/migrate` | IpController::runMigration | ips.migrate.run | server.manage | yes | yes | ☐ |
| POST | `/ips/ranges/{id}/allocate` | IpController::allocate | ips.ranges.allocate | server.manage | yes | yes | ☐ |
| POST | `/ips/ranges/{id}/delete` | IpController::deleteRange | ips.ranges.delete | server.manage | yes | yes | ☐ |
| POST | `/ips/ranges/{id}` | IpController::updateRange | ips.ranges.update | server.manage | yes | yes | ☐ |
| GET | `/ips/ranges` | IpController::ranges | ips.ranges | server.view | yes | yes | ☐ |
| POST | `/ips/ranges` | IpController::storeRange | ips.ranges.store | server.manage | yes | yes | ☐ |
| POST | `/ips/rebuild` | IpController::rebuild | ips.rebuild | server.manage | yes | yes | ☐ |
| POST | `/ips/{id}/delete` | IpController::delete | ips.delete | server.manage | yes | yes | ☐ |
| POST | `/ips/{id}` | IpController::update | ips.update | server.manage | yes | yes | ☐ |
| GET | `/ips` | IpController::index | ips | server.view | yes | yes | ☐ |
| POST | `/ips` | IpController::store | ips.store | server.manage | yes | yes | ☐ |
| GET | `/jobs/{id}` | JobsController::show | jobs.show |  | yes | yes | ☑ |
| POST | `/limits/apply` | LimitsController::apply | limits.apply | server.manage | yes | yes | ☐ |
| POST | `/limits/quota/enable` | LimitsController::enableQuota | limits.quota.enable | server.manage | yes | yes | ☐ |
| GET | `/limits` | LimitsController::index | limits | server.view | yes | yes | ☐ |
| POST | `/login/two-factor/key/options` | AuthController::securityKeyOptions | login.key.options |  | no | yes | ☐ |
| POST | `/login/two-factor/key` | AuthController::securityKey | login.key |  | no | yes | ☐ |
| GET | `/login/two-factor` | AuthController::twoFactor | login.two_factor |  | no | yes | ◑ |
| POST | `/login/two-factor` | AuthController::verifyTwoFactor | login.two_factor.submit |  | no | yes | ◑ |
| GET | `/login/verify-ip` | AuthController::verifyIpForm | login.verify_ip |  | no | yes | ☐ |
| POST | `/login/verify-ip` | AuthController::verifyIp | login.verify_ip.submit |  | no | yes | ☐ |
| GET | `/login` | AuthController::show | login |  | no | yes | ☑ |
| POST | `/login` | AuthController::login | login.submit |  | no | yes | ☑ |
| POST | `/logout` | AuthController::logout | logout |  | yes | yes | ☑ |
| GET | `/logs/download` | LogsController::download | logs.download | logs.view | yes | yes | ☑ |
| GET | `/logs/tail` | LogsController::tail | logs.tail | logs.view | yes | yes | ☑ |
| GET | `/logs` | LogsController::index | logs | logs.view | yes | yes | ☑ |
| POST | `/mail/admin/greylisting/providers/update` | MailGreylistController::updateProviders | mail.greylist.providers.update | server.manage | yes | yes | ☐ |
| POST | `/mail/admin/greylisting/providers` | MailGreylistController::saveProviders | mail.greylist.providers | server.manage | yes | yes | ☐ |
| GET | `/mail/admin/greylisting/report` | MailGreylistController::report | mail.greylist.report | server.view | yes | yes | ☐ |
| POST | `/mail/admin/greylisting/report` | MailGreylistController::cleanup | mail.greylist.cleanup | server.manage | yes | yes | ☐ |
| POST | `/mail/admin/greylisting/trusted/{n}/delete` | MailGreylistController::deleteTrusted | mail.greylist.trusted.delete | server.manage | yes | yes | ☐ |
| POST | `/mail/admin/greylisting/trusted` | MailGreylistController::saveTrusted | mail.greylist.trusted | server.manage | yes | yes | ☐ |
| GET | `/mail/admin/greylisting` | MailGreylistController::index | mail.greylist | server.view | yes | yes | ☐ |
| POST | `/mail/admin/greylisting` | MailGreylistController::save | mail.greylist.save | server.manage | yes | yes | ☐ |
| GET | `/mail/admin/queue/{id}` | MailAdminController::message | mail.admin.message | server.view | yes | yes | ☐ |
| GET | `/mail/admin/queue` | MailAdminController::queue | mail.admin.queue | server.view | yes | yes | ☐ |
| POST | `/mail/admin/queue` | MailAdminController::queueAction | mail.admin.queue.action | server.manage | yes | yes | ☐ |
| GET | `/mail/admin/reports` | MailAdminController::reports | mail.admin.reports | server.view | yes | yes | ☐ |
| POST | `/mail/admin/sending-limits/reset` | MailRateController::reset | mail.limits.reset | server.manage | yes | yes | ☐ |
| GET | `/mail/admin/sending-limits` | MailRateController::index | mail.limits | server.view | yes | yes | ☐ |
| POST | `/mail/admin/sending-limits` | MailRateController::save | mail.limits.save | server.manage | yes | yes | ☐ |
| GET | `/mail/admin/server` | MailAdminController::server | mail.admin.server | server.view | yes | yes | ☐ |
| POST | `/mail/admin/server` | MailAdminController::saveSettings | mail.admin.settings | server.manage | yes | yes | ☐ |
| POST | `/mail/admin/tools/repair` | MailAdminController::repair | mail.admin.repair | server.manage | yes | yes | ☐ |
| GET | `/mail/admin/tools` | MailAdminController::tools | mail.admin.tools | server.view | yes | yes | ☐ |
| POST | `/mail/domains/{id}/aliases/{alias}/delete` | MailController::deleteAlias | mail.aliases.delete | mail.manage | yes | yes | ☐ |
| POST | `/mail/domains/{id}/aliases` | MailController::saveAlias | mail.aliases.store | mail.manage | yes | yes | ☐ |
| POST | `/mail/domains/{id}/delete` | MailController::delete | mail.delete | mail.manage | yes | yes | ☐ |
| GET | `/mail/domains/{id}/dns-check` | MailController::dnsCheck | mail.dns.check | mail.manage | yes | yes | ☐ |
| POST | `/mail/domains/{id}/dns` | MailController::publishDns | mail.dns | mail.manage | yes | yes | ☐ |
| POST | `/mail/domains/{id}/mailboxes/{mailbox}/delete` | MailController::deleteMailbox | mail.mailboxes.delete | mail.manage | yes | yes | ☐ |
| POST | `/mail/domains/{id}/mailboxes/{mailbox}/filters/{n}/delete` | MailFiltersController::delete | mail.filters.delete | mail.manage | yes | yes | ☐ |
| POST | `/mail/domains/{id}/mailboxes/{mailbox}/filters/{n}/toggle` | MailFiltersController::toggle | mail.filters.toggle | mail.manage | yes | yes | ☐ |
| GET | `/mail/domains/{id}/mailboxes/{mailbox}/filters` | MailFiltersController::show | mail.filters | mail.manage | yes | yes | ☐ |
| POST | `/mail/domains/{id}/mailboxes/{mailbox}/filters` | MailFiltersController::save | mail.filters.save | mail.manage | yes | yes | ☐ |
| POST | `/mail/domains/{id}/mailboxes/{mailbox}/responder` | MailFiltersController::responder | mail.responder | mail.manage | yes | yes | ☐ |
| POST | `/mail/domains/{id}/mailboxes/{mailbox}` | MailController::updateMailbox | mail.mailboxes.update | mail.manage | yes | yes | ☐ |
| POST | `/mail/domains/{id}/mailboxes` | MailController::saveMailbox | mail.mailboxes.store | mail.manage | yes | yes | ☐ |
| POST | `/mail/domains/{id}/routing` | MailFiltersController::routing | mail.routing | mail.manage | yes | yes | ☐ |
| GET | `/mail/domains/{id}` | MailController::show | mail.domain | mail.manage | yes | yes | ☐ |
| POST | `/mail/domains` | MailController::store | mail.store | mail.manage | yes | yes | ☐ |
| POST | `/mail/install` | MailController::install | mail.install | packages.manage | yes | yes | ☐ |
| POST | `/mail/setup` | MailController::setup | mail.setup | server.manage | yes | yes | ☐ |
| POST | `/mail/spam/install` | MailController::installSpam | mail.spam.install | packages.manage | yes | yes | ☐ |
| POST | `/mail/spam` | MailController::spam | mail.spam | server.manage | yes | yes | ☐ |
| POST | `/mail/webmail/install` | MailController::installWebmail | mail.webmail.install | packages.manage | yes | yes | ☐ |
| POST | `/mail/webmail` | MailController::webmail | mail.webmail | server.manage | yes | yes | ☐ |
| GET | `/mail` | MailController::index | mail | mail.manage | yes | yes | ☑ |
| POST | `/memcached/configure` | MemcachedController::configure | memcached.configure | redis.manage | yes | yes |☑ |
| POST | `/memcached/flush` | MemcachedController::flush | memcached.flush | redis.manage | yes | yes |☑ |
| POST | `/memcached/install` | MemcachedController::install | memcached.install | packages.manage | yes | yes |☑ |
| POST | `/memcached/restart` | MemcachedController::restart | memcached.restart | services.manage | yes | yes |☑ |
| GET | `/memcached` | MemcachedController::index | memcached | redis.manage | yes | yes |☑ |
| POST | `/mongo/admin-password` | MongoController::adminPassword | mongo.admin_password | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/auth` | MongoController::auth | mongo.auth | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/backup` | MongoController::backup | mongo.backup | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/backups/delete` | MongoController::deleteBackup | mongo.backup_delete | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/browse` | MongoController::browse | mongo.browse | mongo.view | yes | yes | ☐ |
| GET | `/mongo/browser` | MongoController::browser | mongo.browser | mongo.view | yes | yes | ☐ |
| POST | `/mongo/config-file` | MongoController::configFile | mongo.config_file | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/config` | MongoController::config | mongo.config | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/databases/bulk-drop` | MongoController::bulkDropDatabases | mongo.database_bulk_drop | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/databases/drop` | MongoController::dropDatabase | mongo.database_drop | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/databases` | MongoController::createDatabase | mongo.database_create | mongo.manage | yes | yes | ☐ |
| GET | `/mongo/export` | MongoController::export | mongo.export | mongo.view | yes | yes | ☐ |
| POST | `/mongo/install` | MongoController::install | mongo.install | packages.manage | yes | yes | ☐ |
| POST | `/mongo/note` | MongoController::note | mongo.note | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/remotes/databases` | MongoController::remoteDatabases | mongo.remote_databases | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/remotes/delete` | MongoController::remoteDelete | mongo.remote_delete | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/remotes/pull` | MongoController::remotePull | mongo.remote_pull | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/remotes/test` | MongoController::remoteTest | mongo.remote_test | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/remotes` | MongoController::remoteAdd | mongo.remote_add | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/repository` | MongoController::enableRepository | mongo.repository_enable | packages.manage | yes | yes | ☐ |
| POST | `/mongo/restore` | MongoController::restore | mongo.restore | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/service` | MongoController::service | mongo.service | services.manage | yes | yes | ☐ |
| POST | `/mongo/users/drop` | MongoController::dropUser | mongo.user_drop | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/users/password` | MongoController::setPassword | mongo.user_password | mongo.manage | yes | yes | ☐ |
| POST | `/mongo/users` | MongoController::createUser | mongo.user_create | mongo.manage | yes | yes | ☐ |
| GET | `/mongo` | MongoController::index | mongo | mongo.view | yes | yes | ☐ |
| GET | `/monitoring/live.json` | MonitoringController::liveData | monitoring.live.data | monitoring.view | yes | yes | ☐ |
| GET | `/monitoring/live` | MonitoringController::live | monitoring.live | monitoring.view | yes | yes | ☐ |
| POST | `/monitoring/processes/kill-user` | MonitoringController::killUser | monitoring.processes.kill_user | server.manage | yes | yes | ☐ |
| POST | `/monitoring/processes/signal` | MonitoringController::signal | monitoring.processes.signal | server.manage | yes | yes | ☐ |
| GET | `/monitoring/processes/trace` | MonitoringController::trace | monitoring.processes.trace | server.manage | yes | yes | ☐ |
| GET | `/monitoring/processes` | MonitoringController::processes | monitoring.processes | monitoring.view | yes | yes | ☐ |
| GET | `/monitoring` | MonitoringController::index | monitoring | monitoring.view | yes | yes | ☐ |
| POST | `/nginx/reload` | NginxController::reload | nginx.reload | services.manage | yes | yes | ☐ |
| POST | `/nginx/test` | NginxController::test | nginx.test | server.view | yes | yes | ☐ |
| POST | `/nginx/tuning` | NginxController::tuning | nginx.tuning | server.manage | yes | yes | ☐ |
| GET | `/nginx` | NginxController::index | nginx | server.view | yes | yes | ☐ |
| POST | `/notifications/email` | NotificationsController::email | notifications.email | notifications.view | yes | yes | ☐ |
| POST | `/notifications/read` | NotificationsController::read | notifications.read | notifications.view | yes | yes | ☑ |
| GET | `/notifications/summary` | NotificationsController::summary | notifications.summary | notifications.view | yes | yes | ☐ |
| GET | `/notifications` | NotificationsController::index | notifications | notifications.view | yes | yes | ☑ |
| POST | `/packages/install` | SystemController::install | packages.install | packages.manage | yes | yes | ☐ |
| POST | `/packages/refresh` | SystemController::refresh | packages.refresh | packages.manage | yes | yes | ☐ |
| POST | `/packages/repair` | SystemController::repair | packages.repair | packages.manage | yes | yes | ☐ |
| POST | `/packages/upgrade` | SystemController::upgrade | packages.upgrade | packages.manage | yes | yes | ☐ |
| GET | `/packages` | SystemController::packages | packages | packages.manage | yes | yes | ☐ |
| GET | `/password/forgot` | PasswordResetController::forgot | password.forgot |  | no | yes | ☑ |
| POST | `/password/forgot` | PasswordResetController::send | password.send |  | no | yes | ☑ |
| GET | `/password/reset/{token}` | PasswordResetController::show | password.reset.form |  | no | yes | ☑ |
| POST | `/password/reset/{token}` | PasswordResetController::reset | password.reset |  | no | yes | ☑ |
| POST | `/php/install` | PhpController::install | php.install | php.manage | yes | yes | ☐ |
| POST | `/php/manager/apply` | PhpManagerController::apply | php.manager.apply | packages.manage | yes | yes | ☐ |
| GET | `/php/manager/export` | PhpManagerController::export | php.manager.export | packages.manage | yes | yes | ☐ |
| POST | `/php/manager/import` | PhpManagerController::import | php.manager.import | packages.manage | yes | yes | ☐ |
| POST | `/php/manager/review` | PhpManagerController::review | php.manager.review | packages.manage | yes | yes | ☐ |
| GET | `/php/manager` | PhpManagerController::index | php.manager | packages.manage | yes | yes | ☐ |
| POST | `/php/repository` | PhpController::enableRepository | php.repository | packages.manage | yes | yes | ☐ |
| POST | `/php/{version}/extensions/{op}` | PhpController::extension | php.extension | php.manage | yes | yes | ☐ |
| POST | `/php/{version}/fpm` | PhpController::fpm | php.fpm | php.manage | yes | yes | ☐ |
| POST | `/php/{version}/ini` | PhpController::ini | php.ini | php.manage | yes | yes | ☐ |
| GET | `/php/{version}/modules` | PhpController::modules | php.modules | php.view | yes | yes | ☐ |
| POST | `/php/{version}/toggle` | PhpController::toggle | php.toggle | php.manage | yes | yes | ☐ |
| GET | `/php/{version}` | PhpController::show | php.show | php.manage | yes | yes | ☐ |
| GET | `/php` | DiscoveryController::php | php | php.view | yes | yes | ☐ |
| POST | `/plans/{id}/delete` | PlansController::destroy | plans.delete | plans.manage | yes | yes | ☐ |
| GET | `/plans` | PlansController::index | plans | plans.manage | yes | yes | ☑ |
| POST | `/plans` | PlansController::store | plans.store | plans.manage | yes | yes | ☐ |
| POST | `/profile/password` | ProfileController::password | profile.password |  | yes | yes | ☐ |
| POST | `/profile/security-keys/options` | ProfileController::keyOptions | profile.2fa.key.options |  | yes | yes | ☐ |
| POST | `/profile/security-keys/{id}/delete` | ProfileController::keyDelete | profile.2fa.key.delete |  | yes | yes | ☐ |
| POST | `/profile/security-keys` | ProfileController::keyAdd | profile.2fa.key.add |  | yes | yes | ☐ |
| POST | `/profile/sessions/revoke` | ProfileController::revokeSessions | profile.sessions.revoke |  | yes | yes | ☐ |
| POST | `/profile/tokens/{id}/revoke` | ApiTokensController::revoke | tokens.revoke | api.tokens | yes | yes | ☐ |
| POST | `/profile/tokens` | ApiTokensController::store | tokens.store | api.tokens | yes | yes | ☐ |
| POST | `/profile/two-factor/disable` | ProfileController::twoFactorDisable | profile.2fa.disable |  | yes | yes | ☑ |
| POST | `/profile/two-factor/enable` | ProfileController::twoFactorEnable | profile.2fa.enable |  | yes | yes | ☑ |
| POST | `/profile/two-factor/recovery` | ProfileController::twoFactorRecovery | profile.2fa.recovery |  | yes | yes | ☑ |
| POST | `/profile/two-factor/setup` | ProfileController::twoFactorSetup | profile.2fa.setup |  | yes | yes | ☑ |
| GET | `/profile` | ProfileController::show | profile |  | yes | yes | ◑ |
| POST | `/redis/browser/api` | RedisBrowserController::api | redis.browser.api | redis.manage | yes | yes |☑ |
| GET | `/redis/browser` | RedisBrowserController::browse | redis.browser | redis.manage | yes | yes |☑ |
| POST | `/redis/configure` | RedisController::configure | redis.configure | redis.manage | yes | yes |☑ |
| POST | `/redis/flush` | RedisController::flush | redis.flush | redis.manage | yes | yes |☑ |
| POST | `/redis/password` | RedisController::password | redis.password | redis.manage | yes | yes |☑ |
| POST | `/redis/users/delete` | RedisController::deleteUser | redis.users.delete | redis.manage | yes | yes |☑ |
| POST | `/redis/users` | RedisController::createUser | redis.users.store | redis.manage | yes | yes |☑ |
| GET | `/redis` | RedisController::index | redis | redis.manage | yes | yes |☑ |
| GET | `/roles/new` | RolesController::create | roles.create | roles.manage | yes | yes | ☐ |
| POST | `/roles/{id}/delete` | RolesController::destroy | roles.delete | roles.manage | yes | yes | ☐ |
| GET | `/roles/{id}/edit` | RolesController::edit | roles.edit | users.view | yes | yes | ☐ |
| POST | `/roles/{id}/reset` | RolesController::reset | roles.reset | roles.manage | yes | yes | ☐ |
| POST | `/roles/{id}/two-factor` | RolesController::twoFactorPolicy | roles.2fa | roles.manage | yes | yes | ☐ |
| POST | `/roles/{id}` | RolesController::update | roles.update | roles.manage | yes | yes | ☐ |
| GET | `/roles` | RolesController::index | roles | users.view | yes | yes | ☑ |
| POST | `/roles` | RolesController::store | roles.store | roles.manage | yes | yes | ☐ |
| POST | `/runtimes/install` | RuntimeController::install | runtimes.install | server.manage | yes | yes | ☐ |
| POST | `/runtimes/remove` | RuntimeController::remove | runtimes.remove | server.manage | yes | yes | ☐ |
| GET | `/runtimes` | RuntimeController::index | runtimes | server.view | yes | yes | ☐ |
| POST | `/security/advisories/run` | SecurityChecksController::runAdvisories | security.advisories.run | security.view | yes | yes | ☑ |
| GET | `/security/advisories` | SecurityChecksController::advisories | security.advisories | security.view | yes | yes | ☑ |
| GET | `/security/compilers` | HardeningController::compilers | security.compilers | security.view | yes | yes | ☑ |
| POST | `/security/compilers` | HardeningController::saveCompilers | security.compilers.save | security.manage | yes | yes | ☑ |
| POST | `/security/confirm` | SecurityController::confirm | security.confirm | security.manage | yes | yes | ☑ |
| GET | `/security/egress` | HardeningController::egress | security.egress | security.view | yes | yes | ☑ |
| POST | `/security/egress` | HardeningController::saveEgress | security.egress.save | security.manage | yes | yes | ☑ |
| POST | `/security/fail2ban/unban` | SecurityController::unban | security.fail2ban.unban | security.manage | yes | yes | ☑ |
| GET | `/security/fail2ban` | SecurityController::fail2ban | security.fail2ban | security.view | yes | yes | ☑ |
| POST | `/security/fail2ban` | SecurityController::configureFail2ban | security.fail2ban.configure | security.manage | yes | yes | ☑ |
| POST | `/security/firewall/protection` | SecurityController::protection | security.firewall.protection | security.manage | yes | yes | ☑ |
| POST | `/security/firewall/rules/delete` | SecurityController::deleteRule | security.firewall.delete | security.manage | yes | yes | ☑ |
| POST | `/security/firewall/rules` | SecurityController::addRule | security.firewall.add | security.manage | yes | yes | ☑ |
| POST | `/security/firewall/toggle` | SecurityController::toggleFirewall | security.firewall.toggle | security.manage | yes | yes | ☑ |
| GET | `/security/firewall` | SecurityController::firewall | security.firewall | security.view | yes | yes | ☑ |
| POST | `/security/fix` | SecurityController::fix | security.fix | server.manage | yes | yes | ☑ |
| GET | `/security/forkbomb` | HardeningController::forkbomb | security.forkbomb | security.view | yes | yes | ☑ |
| POST | `/security/forkbomb` | HardeningController::saveForkbomb | security.forkbomb.save | security.manage | yes | yes | ☑ |
| POST | `/security/hulk/clear` | HulkController::clear | security.hulk.clear | security.manage | yes | yes | ☑ |
| POST | `/security/hulk/countries` | HulkController::countries | security.hulk.countries | security.manage | yes | yes | ☑ |
| POST | `/security/hulk/geo` | HulkController::geoUpload | security.hulk.geo | security.manage | yes | yes | ☑ |
| POST | `/security/hulk/unblock` | HulkController::unblock | security.hulk.unblock | security.manage | yes | yes | ☑ |
| GET | `/security/hulk` | HulkController::index | security.hulk | security.view | yes | yes | ☑ |
| POST | `/security/hulk` | HulkController::save | security.hulk.save | security.manage | yes | yes | ☑ |
| POST | `/security/integrity/baseline` | SecurityChecksController::setBaseline | security.integrity.baseline | security.manage | yes | yes | ☑ |
| POST | `/security/integrity/run` | SecurityChecksController::runIntegrity | security.integrity.run | security.view | yes | yes | ☑ |
| GET | `/security/integrity` | SecurityChecksController::integrity | security.integrity | security.view | yes | yes | ☑ |
| POST | `/security/malware/settings` | AvController::saveSettings | security.malware.settings | security.manage | yes | yes | ☑ |
| POST | `/security/malware/update` | AvController::updateSignatures | security.malware.update | security.manage | yes | yes | ☑ |
| GET | `/security/malware` | AvController::index | security.malware | security.view | yes | yes | ☑ |
| POST | `/security/modsec/crs` | ModSecurityController::installCrs | security.modsec.crs | security.manage | yes | yes | ☑ |
| POST | `/security/modsec/custom` | ModSecurityController::saveCustom | security.modsec.custom | security.manage | yes | yes | ☑ |
| POST | `/security/modsec/install` | ModSecurityController::install | security.modsec.install | security.manage | yes | yes | ☑ |
| POST | `/security/modsec/rollback/{id}` | ModSecurityController::rollback | security.modsec.rollback | security.manage | yes | yes | ☑ |
| POST | `/security/modsec/rule` | ModSecurityController::saveRule | security.modsec.rule | security.manage | yes | yes | ☑ |
| POST | `/security/modsec/sites/{id}` | ModSecurityController::saveSite | security.modsec.site | security.manage | yes | yes | ☑ |
| POST | `/security/modsec/update` | ModSecurityController::update | security.modsec.update | security.manage | yes | yes | ☑ |
| GET | `/security/modsec` | ModSecurityController::index | security.modsec | security.view | yes | yes | ☑ |
| POST | `/security/modsec` | ModSecurityController::configure | security.modsec.configure | security.manage | yes | yes | ☑ |
| POST | `/security/mysql-anonymous` | SecurityController::dropAnonymousUsers | security.mysql_anonymous | server.manage | yes | yes | ☑ |
| GET | `/security/password-strength` | SecurityPolicyController::strength | security.password_strength | security.view | yes | yes | ☑ |
| POST | `/security/password-strength` | SecurityPolicyController::saveStrength | security.password_strength.save | security.manage | yes | yes | ☑ |
| POST | `/security/policies/ips/forget` | SecurityPolicyController::forgetIp | security.policies.forget_ip | security.view | yes | yes | ☑ |
| GET | `/security/policies` | SecurityPolicyController::policies | security.policies | security.view | yes | yes | ☑ |
| POST | `/security/policies` | SecurityPolicyController::savePolicies | security.policies.save | security.manage | yes | yes | ☑ |
| GET | `/security/shell` | ShellController::index | security.shell | security.view | yes | yes | ☑ |
| POST | `/security/shell` | ShellController::update | security.shell.update | security.manage | yes | yes | ☑ |
| GET | `/security/smtp` | HardeningController::smtp | security.smtp | security.view | yes | yes | ☑ |
| POST | `/security/smtp` | HardeningController::saveSmtp | security.smtp.save | security.manage | yes | yes | ☑ |
| POST | `/security/ssh/keys/generate` | SecurityController::generateSshKey | security.ssh.key_generate | security.manage | yes | yes | ☑ |
| POST | `/security/ssh/keys/remove` | SecurityController::removeSshKey | security.ssh.key_remove | security.manage | yes | yes | ☑ |
| POST | `/security/ssh/keys` | SecurityController::addSshKey | security.ssh.key_add | security.manage | yes | yes | ☑ |
| GET | `/security/ssh` | SecurityController::ssh | security.ssh | security.view | yes | yes | ☑ |
| POST | `/security/ssh` | SecurityController::configureSsh | security.ssh.configure | security.manage | yes | yes | ☑ |
| POST | `/security/tls/run` | SecurityChecksController::runTls | security.tls.run | security.view | yes | yes | ☑ |
| GET | `/security/tls` | SecurityChecksController::tls | security.tls | security.view | yes | yes | ☑ |
| GET | `/security/traceroute` | HardeningController::traceroute | security.traceroute | security.view | yes | yes | ☑ |
| POST | `/security/traceroute` | HardeningController::saveTraceroute | security.traceroute.save | security.manage | yes | yes | ☑ |
| POST | `/security/undo` | SecurityController::undo | security.undo | security.manage | yes | yes | ☑ |
| GET | `/security` | SecurityController::index | security | security.view | yes | yes | ☑ |
| POST | `/server/graphical` | SystemController::graphical | server.graphical | server.manage | yes | yes | ☐ |
| GET | `/server/health` | ServerHealthController::index | server.health | server.view | yes | yes | ☐ |
| POST | `/server/hostname-references/mail` | SystemController::hostnameRemail | server.hostname_references.mail | server.manage | yes | yes | ☐ |
| POST | `/server/hostname-references/webmail` | SystemController::hostnameWebmail | server.hostname_references.webmail | server.manage | yes | yes | ☐ |
| GET | `/server/hostname-references` | SystemController::hostnameReferences | server.hostname_references | server.manage | yes | yes | ☐ |
| POST | `/server/hostname` | SystemController::hostname | server.hostname | server.manage | yes | yes | ☐ |
| POST | `/server/reboot/cancel` | SystemController::cancelReboot | server.reboot_cancel | server.manage | yes | yes | ☐ |
| POST | `/server/reboot` | SystemController::reboot | server.reboot | server.manage | yes | yes | ☐ |
| POST | `/server/root-password` | SystemController::rootPassword | server.root_password | server.manage | yes | yes | ☐ |
| POST | `/server/timezone` | SystemController::timezone | server.timezone | server.manage | yes | yes | ☐ |
| GET | `/server` | ServerController::index | server | server.view | yes | yes | ☐ |
| POST | `/servers/switch` | ServersController::switch | servers.switch |  | yes | yes | ☐ |
| POST | `/servers/{id}/check` | ServersController::check | servers.check | servers.manage | yes | yes | ☐ |
| POST | `/servers/{id}/delete` | ServersController::destroy | servers.delete | servers.manage | yes | yes | ☐ |
| GET | `/servers` | ServersController::index | servers | servers.manage | yes | yes | ☑ |
| POST | `/servers` | ServersController::store | servers.store | servers.manage | yes | yes | ☐ |
| POST | `/services/control` | ServicesController::control | services.control | services.manage | yes | yes | ☐ |
| POST | `/services/restart-all` | TopbarController::restartAll | services.restart_all | services.manage | yes | yes | ☐ |
| POST | `/services/whacp/{key}/restart` | ServicesController::restartWhacp | services.whacp.restart | services.manage | yes | yes | ☐ |
| GET | `/services` | ServerController::services | services | services.view | yes | yes | ☐ |
| GET | `/settings/basic` | WebHostSettingsController::basic | settings.basic | settings.view | yes | yes | ☐ |
| POST | `/settings/basic` | WebHostSettingsController::saveBasic | settings.basic.save | settings.manage | yes | yes | ☐ |
| POST | `/settings/mail/test` | SettingsController::testMail | settings.mail.test | settings.manage | yes | yes | ☐ |
| POST | `/settings/mail` | SettingsController::mail | settings.mail | settings.manage | yes | yes | ☐ |
| GET | `/settings/tweak` | WebHostSettingsController::tweak | settings.tweak | settings.view | yes | yes | ☐ |
| POST | `/settings/tweak` | WebHostSettingsController::saveTweak | settings.tweak.save | settings.manage | yes | yes | ☐ |
| GET | `/settings` | SettingsController::index | settings | settings.view | yes | yes | ☐ |
| POST | `/settings` | SettingsController::update | settings.update | settings.manage | yes | yes | ☐ |
| POST | `/setup/dismiss` | SetupController::dismiss | setup.dismiss | server.manage | yes | yes | ☐ |
| POST | `/setup/step` | SetupController::step | setup.step | server.manage | yes | yes | ☐ |
| GET | `/setup` | SetupController::index | setup | server.manage | yes | yes | ☐ |
| GET | `/sites/{id}/addons` | SitesController::addons | sites.addons | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/addons` | SitesController::storeAddon | sites.addons.store | websites.manage | yes | yes | ☑ |
| GET | `/sites/{id}/advanced` | SitesController::advanced | sites.advanced | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/apps/{app}` | AppsController::install | sites.apps.install | apps.install | yes | yes | ☐ |
| GET | `/sites/{id}/apps` | AppsController::show | sites.apps | apps.install | yes | yes | ☐ |
| POST | `/sites/{id}/backup-schedules/{schedule}/delete` | BackupsController::deleteSchedule | sites.backups.schedule_delete | backups.manage | yes | yes | ☐ |
| POST | `/sites/{id}/backup-schedules` | BackupsController::storeSchedule | sites.backups.schedule | backups.manage | yes | yes | ☐ |
| POST | `/sites/{id}/backups/{backup}/delete` | BackupsController::delete | sites.backups.delete | backups.manage | yes | yes | ☐ |
| GET | `/sites/{id}/backups/{backup}/download` | BackupsController::download | sites.backups.download | backups.manage | yes | yes | ☐ |
| POST | `/sites/{id}/backups/{backup}/restore` | BackupsController::restore | sites.backups.restore | backups.manage | yes | yes | ☐ |
| GET | `/sites/{id}/backups` | BackupsController::show | sites.backups | backups.view | yes | yes | ☐ |
| POST | `/sites/{id}/backups` | BackupsController::create | sites.backups.create | backups.manage | yes | yes | ☐ |
| POST | `/sites/{id}/cloudflare/always-https` | SiteCloudflareController::alwaysHttps | sites.cloudflare.always_https | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/cloudflare/connect` | SiteCloudflareController::connect | sites.cloudflare.connect | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/cloudflare/disconnect` | SiteCloudflareController::disconnect | sites.cloudflare.disconnect | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/cloudflare/proxy` | SiteCloudflareController::proxy | sites.cloudflare.proxy | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/cloudflare/purge` | SiteCloudflareController::purge | sites.cloudflare.purge | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/cloudflare/ssl` | SiteCloudflareController::ssl | sites.cloudflare.ssl | websites.manage | yes | yes | ☐ |
| GET | `/sites/{id}/cloudflare` | SiteCloudflareController::index | sites.cloudflare | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/convert` | AddonConversionController::convert | sites.convert | users.manage | yes | yes | ☑ |
| POST | `/sites/{id}/cron/{job}/delete` | CronController::delete | sites.cron.delete | cron.manage | yes | yes | ☐ |
| GET | `/sites/{id}/cron/{job}/log` | CronController::log | sites.cron.log | cron.view | yes | yes | ☐ |
| POST | `/sites/{id}/cron/{job}/run` | CronController::run | sites.cron.run | cron.manage | yes | yes | ☐ |
| POST | `/sites/{id}/cron/{job}/toggle` | CronController::toggle | sites.cron.toggle | cron.manage | yes | yes | ☐ |
| GET | `/sites/{id}/cron` | CronController::show | sites.cron | cron.view | yes | yes | ☑ |
| POST | `/sites/{id}/cron` | CronController::store | sites.cron.store | cron.manage | yes | yes | ☐ |
| GET | `/sites/{id}/databases` | DatabasesController::site | sites.databases | databases.view | yes | yes | ☑ |
| POST | `/sites/{id}/delete` | SitesController::destroy | sites.delete | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/directory-listing` | SitesController::directoryListing | sites.listing | websites.manage | yes | yes | ☑ |
| GET | `/sites/{id}/disk-usage` | DiskUsageController::show | sites.disk | files.manage | yes | yes | ☑ |
| POST | `/sites/{id}/disk-usage` | DiskUsageController::scan | sites.disk.scan | files.manage | yes | yes | ☑ |
| GET | `/sites/{id}/dns` | DnsController::site | sites.dns | dns.manage | yes | yes | ☐ |
| POST | `/sites/{id}/files/api` | FilesController::api | sites.files.api | files.manage | yes | yes | ☑ |
| GET | `/sites/{id}/files/download` | FilesController::download | sites.files.download | files.manage | yes | yes | ☑ |
| GET | `/sites/{id}/files/edit` | FilesController::edit | sites.files.edit | files.manage | yes | yes | ☑ |
| POST | `/sites/{id}/files/upload` | FilesController::upload | sites.files.upload | files.manage | yes | yes | ☑ |
| GET | `/sites/{id}/files` | FilesController::show | sites.files | files.manage | yes | yes | ☑ |
| GET | `/sites/{id}/firewall` | ModSecurityController::site | sites.firewall | websites.view | yes | yes | ☑ |
| POST | `/sites/{id}/firewall` | ModSecurityController::saveSiteMode | sites.firewall.update | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/ftp/accounts/delete` | FtpController::deleteAccount | sites.ftp.delete | ftp.manage | yes | yes | ☐ |
| POST | `/sites/{id}/ftp/accounts` | FtpController::saveAccount | sites.ftp.save | ftp.manage | yes | yes | ☐ |
| GET | `/sites/{id}/ftp` | FtpController::show | sites.ftp | ftp.manage | yes | yes | ☐ |
| POST | `/sites/{id}/git/deploy` | DeployController::run | sites.git.deploy | deploy.manage | yes | yes | ☐ |
| POST | `/sites/{id}/git/remove` | DeployController::remove | sites.git.remove | deploy.manage | yes | yes | ☐ |
| POST | `/sites/{id}/git/rotate` | DeployController::rotate | sites.git.rotate | deploy.manage | yes | yes | ☐ |
| GET | `/sites/{id}/git` | DeployController::show | sites.git | deploy.manage | yes | yes | ☐ |
| POST | `/sites/{id}/git` | DeployController::configure | sites.git.configure | deploy.manage | yes | yes | ☐ |
| GET | `/sites/{id}/health` | SitesController::health | sites.health | websites.view | yes | yes | ☐ |
| POST | `/sites/{id}/import/undo` | SitesController::undoImport | sites.import.undo | import.run | yes | yes | ☐ |
| POST | `/sites/{id}/ip` | IpController::assignSite | sites.ip | server.manage | yes | yes | ☐ |
| GET | `/sites/{id}/logs` | LogsController::site | sites.logs | logs.view | yes | yes | ☑ |
| GET | `/sites/{id}/mail` | MailController::site | sites.mail | mail.manage | yes | yes | ☐ |
| POST | `/sites/{id}/malware/release` | AvController::release | sites.malware.release | security.manage | yes | yes |☑ |
| POST | `/sites/{id}/malware/scan` | AvController::scan | sites.malware.scan | websites.manage | yes | yes |☑ |
| GET | `/sites/{id}/malware` | AvController::show | sites.malware | websites.view | yes | yes |☑ |
| GET | `/sites/{id}/memcached` | SitesController::memcached | sites.memcached | websites.view | yes | yes |☑ |
| POST | `/sites/{id}/mongo/backup` | SiteMongoController::backup | sites.mongo.backup | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/mongo/backups/delete` | SiteMongoController::deleteBackup | sites.mongo.backup_delete | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/mongo/browse` | SiteMongoController::browse | sites.mongo.browse | websites.manage | yes | yes | ☐ |
| GET | `/sites/{id}/mongo/browser` | SiteMongoController::browser | sites.mongo.browser | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/mongo/databases/drop` | SiteMongoController::dropDatabase | sites.mongo.database_drop | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/mongo/databases` | SiteMongoController::createDatabase | sites.mongo.database_create | websites.manage | yes | yes | ☐ |
| GET | `/sites/{id}/mongo/export` | SiteMongoController::export | sites.mongo.export | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/mongo/restore` | SiteMongoController::restore | sites.mongo.restore | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/mongo/users/drop` | SiteMongoController::dropUser | sites.mongo.user_drop | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/mongo/users/password` | SiteMongoController::setPassword | sites.mongo.user_password | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/mongo/users` | SiteMongoController::createUser | sites.mongo.user_create | websites.manage | yes | yes | ☐ |
| GET | `/sites/{id}/mongo` | SiteMongoController::index | sites.mongo | websites.manage | yes | yes | ☐ |
| GET | `/sites/{id}/nodejs` | SiteRuntimeController::index | sites.nodejs | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/nodejs` | SiteRuntimeController::update | sites.nodejs.update | websites.manage | yes | yes | ☑ |
| GET | `/sites/{id}/permissions` | SitesController::checkPermissions | sites.permissions | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/permissions` | SitesController::repairPermissions | sites.permissions.repair | websites.manage | yes | yes | ☑ |
| GET | `/sites/{id}/python` | SiteRuntimeController::index | sites.python | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/python` | SiteRuntimeController::update | sites.python.update | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/reapply` | SitesController::reapply | sites.reapply | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/redis/api` | RedisBrowserController::siteApi | sites.redis.api | websites.manage | yes | yes |☑ |
| GET | `/sites/{id}/redis` | RedisBrowserController::siteBrowse | sites.redis | websites.manage | yes | yes |☑ |
| POST | `/sites/{id}/restart-php` | SitesController::restartPhp | sites.restart_php | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/security-headers` | SslController::securityHeaders | sites.security_headers | ssl.manage | yes | yes | ☐ |
| GET | `/sites/{id}/security` | SitesController::security | sites.security | websites.view | yes | yes | ☐ |
| GET | `/sites/{id}/settings` | SitesController::edit | sites.edit | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/settings` | SitesController::update | sites.update | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/sftp` | FtpController::sftp | sites.sftp | ftp.manage | yes | yes | ☐ |
| POST | `/sites/{id}/ssl/custom` | SslController::custom | sites.ssl.custom | ssl.manage | yes | yes | ☐ |
| POST | `/sites/{id}/ssl/disable` | SslController::disable | sites.ssl.disable | ssl.manage | yes | yes | ☐ |
| POST | `/sites/{id}/ssl/issue` | SslController::issue | sites.ssl.issue | ssl.manage | yes | yes | ☐ |
| POST | `/sites/{id}/ssl/options` | SslController::options | sites.ssl.options | ssl.manage | yes | yes | ☐ |
| POST | `/sites/{id}/ssl/renew` | SslController::renew | sites.ssl.renew | ssl.manage | yes | yes | ☐ |
| GET | `/sites/{id}/ssl` | SslController::show | sites.ssl | ssl.view | yes | yes | ☐ |
| POST | `/sites/{id}/stats/enable` | StatsController::enable | sites.stats.enable | websites.manage | yes | yes |☑ |
| POST | `/sites/{id}/stats/update` | StatsController::update | sites.stats.update | websites.manage | yes | yes |☑ |
| GET | `/sites/{id}/stats` | StatsController::show | sites.stats | logs.view | yes | yes |☑ |
| GET | `/sites/{id}/subdomains` | SitesController::subdomains | sites.subdomains | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/subdomains` | SitesController::storeSubdomain | sites.subdomains.store | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/terminal/open` | TerminalController::openSite | sites.terminal.open | files.manage | yes | yes | ☑ |
| GET | `/sites/{id}/terminal` | TerminalController::site | sites.terminal | files.manage | yes | yes | ☑ |
| POST | `/sites/{id}/toggle` | SitesController::toggle | sites.toggle | websites.manage | yes | yes | ☑ |
| POST | `/sites/{id}/wordpress/scan` | WordPressController::scanSite | sites.wordpress.scan | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/wordpress/{wp}/check` | WordPressController::check | sites.wordpress.check | websites.view | yes | yes | ☐ |
| POST | `/sites/{id}/wordpress/{wp}/login` | WordPressController::login | sites.wordpress.login | websites.manage | yes | yes | ☐ |
| GET | `/sites/{id}/wordpress/{wp}/logs` | WordPressController::logs | sites.wordpress.logs | websites.view | yes | yes | ☐ |
| POST | `/sites/{id}/wordpress/{wp}/manage` | WordPressController::manage | sites.wordpress.manage | websites.manage | yes | yes | ☐ |
| POST | `/sites/{id}/wordpress/{wp}/tools` | WordPressController::tool | sites.wordpress.tool | websites.manage | yes | yes | ☐ |
| GET | `/sites/{id}/wordpress/{wp}` | WordPressController::show | sites.wordpress.show | websites.view | yes | yes | ☐ |
| GET | `/sites/{id}/wordpress` | WordPressController::siteList | sites.wordpress | websites.view | yes | yes | ☐ |
| POST | `/sites/{id}/workers/{worker}/delete` | WorkersController::delete | sites.workers.delete | workers.manage | yes | yes | ☑ |
| POST | `/sites/{id}/workers/{worker}/restart` | WorkersController::restart | sites.workers.restart | workers.manage | yes | yes | ☑ |
| POST | `/sites/{id}/workers/{worker}/toggle` | WorkersController::toggle | sites.workers.toggle | workers.manage | yes | yes | ☑ |
| GET | `/sites/{id}/workers` | WorkersController::show | sites.workers | workers.manage | yes | yes | ☑ |
| POST | `/sites/{id}/workers` | WorkersController::store | sites.workers.store | workers.manage | yes | yes | ☑ |
| GET | `/sites/{id}` | SitesController::show | sites.show | websites.view | yes | yes | ☐ |
| POST | `/ssl/autossl/run` | SslCenterController::autosslRun | ssl.autossl.run | ssl.manage | yes | yes | ☐ |
| GET | `/ssl/autossl` | SslCenterController::autossl | ssl.autossl | ssl.view | yes | yes | ☐ |
| POST | `/ssl/certificates/delete` | SslCenterController::deleteCertificate | ssl.certificates.delete | ssl.manage | yes | yes | ☐ |
| GET | `/ssl/certificates` | SslCenterController::certificates | ssl.certificates | ssl.view | yes | yes | ☐ |
| GET | `/ssl/hosts` | SslCenterController::hosts | ssl.hosts | ssl.view | yes | yes | ☐ |
| GET | `/ssl/keys/csr` | SslCenterController::csrDownload | ssl.keys.csr | ssl.view | yes | yes | ☐ |
| POST | `/ssl/keys/delete` | SslCenterController::deleteKey | ssl.keys.delete | ssl.manage | yes | yes | ☐ |
| POST | `/ssl/keys/install` | SslCenterController::installKey | ssl.keys.install | ssl.manage | yes | yes | ☐ |
| GET | `/ssl/keys` | SslCenterController::keys | ssl.keys | ssl.view | yes | yes | ☐ |
| POST | `/ssl/keys` | SslCenterController::createCsr | ssl.keys.create | ssl.manage | yes | yes | ☐ |
| POST | `/ssl/renewal` | SslCenterController::enableRenewal | ssl.renewal.enable | ssl.manage | yes | yes | ☐ |
| POST | `/ssl/services/account` | SslCenterController::accountRegister | ssl.services.account | server.manage | yes | yes | ☐ |
| POST | `/ssl/services/cert` | SslCenterController::serviceCert | ssl.services.cert | server.manage | yes | yes | ☐ |
| POST | `/ssl/services/hostname` | SslCenterController::hostnameIssue | ssl.services.hostname | server.manage | yes | yes | ☐ |
| POST | `/ssl/services/mail-tls` | SslCenterController::mailTls | ssl.services.mail_tls | server.manage | yes | yes | ☐ |
| GET | `/ssl/services` | SslCenterController::services | ssl.services | ssl.view | yes | yes | ☐ |
| GET | `/ssl/tls` | SslCenterController::tls | ssl.tls | ssl.view | yes | yes | ☐ |
| POST | `/ssl/tls` | SslCenterController::saveTls | ssl.tls.save | server.manage | yes | yes | ☐ |
| GET | `/ssl` | DiscoveryController::ssl | ssl | ssl.view | yes | yes | ☐ |
| GET | `/system-users` | ServerController::systemUsers | system_users | system_users.view | yes | yes | ☐ |
| POST | `/terminal/root/open` | TerminalController::openRoot | terminal.root | security.manage | yes | yes | ☑ |
| POST | `/terminal/sessions/{id}/close` | TerminalController::close | terminal.close | security.manage | yes | yes | ☑ |
| POST | `/terminal/unlock` | TerminalController::unlock | sites.terminal.unlock | files.manage | yes | yes | ☑ |
| GET | `/terminal` | TerminalController::index | terminal | security.manage | yes | yes | ☑ |
| GET | `/topbar/status` | TopbarController::status | topbar.status | monitoring.view | yes | yes | ☐ |
| GET | `/users/new` | UsersController::create | users.create | users.manage | yes | yes | ☐ |
| POST | `/users/{id}/bandwidth/reset` | AccountAdminController::resetBandwidth | users.bandwidth.reset | users.manage | yes | yes | ☐ |
| POST | `/users/{id}/delete` | UsersController::destroy | users.delete | users.manage | yes | yes | ☐ |
| GET | `/users/{id}/edit` | UsersController::edit | users.edit | users.manage | yes | yes | ☐ |
| POST | `/users/{id}/hosting-options` | AccountsController::saveOptions | users.options | users.manage | yes | yes | ☐ |
| POST | `/users/{id}/limits` | AccountAdminController::saveLimits | users.limits | users.manage | yes | yes | ☐ |
| GET | `/users/{id}/plan` | AccountAdminController::plan | users.plan | users.manage | yes | yes | ☐ |
| POST | `/users/{id}/plan` | AccountAdminController::changePlan | users.plan.change | users.manage | yes | yes | ☐ |
| POST | `/users/{id}/suspend` | AccountsController::suspend | users.suspend | users.manage | yes | yes | ☐ |
| POST | `/users/{id}/two-factor/reset` | UsersController::resetTwoFactor | users.2fa.reset | users.manage | yes | yes | ☐ |
| POST | `/users/{id}/unsuspend` | AccountsController::unsuspend | users.unsuspend | users.manage | yes | yes | ☐ |
| POST | `/users/{id}` | UsersController::update | users.update | users.manage | yes | yes | ☐ |
| GET | `/users` | UsersController::index | users | users.view | yes | yes | ☑ |
| POST | `/users` | UsersController::store | users.store | users.manage | yes | yes | ☐ |
| GET | `/websites/new` | SitesController::create | sites.create | websites.manage | yes | yes | ☑ |
| POST | `/websites/new` | SitesController::store | sites.store | websites.manage | yes | yes | ☑ |
| GET | `/websites/{id}/import` | ImportController::preview | websites.import | import.run | yes | yes | ☐ |
| POST | `/websites/{id}/import` | ImportController::run | websites.import.run | import.run | yes | yes | ☐ |
| GET | `/websites/{id}` | WebsitesController::show | websites.show | websites.view | yes | yes | ☑ |
| GET | `/websites` | WebsitesController::index | websites | websites.view | yes | yes | ☑ |
| POST | `/whacp/update/check` | TopbarController::updateCheck | whacp.update.check | server.manage | yes | yes | ☐ |
| GET | `/whacp/update` | TopbarController::update | whacp.update | server.manage | yes | yes | ☐ |
| POST | `/whacp/update` | TopbarController::updateStart | whacp.update.start | server.manage | yes | yes | ☐ |
| POST | `/wordpress/scan` | WordPressController::scanAll | wordpress.scan | websites.manage | yes | yes | ☐ |
| GET | `/wordpress` | WordPressController::index | wordpress | websites.view | yes | yes | ☐ |
| GET | `/workers` | WorkersController::index | workers | workers.manage | yes | yes | ☑ |
