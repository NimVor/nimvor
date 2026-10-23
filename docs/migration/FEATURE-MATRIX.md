# WhaCP Feature Matrix (plan-gated features)

_From app/Services/FeatureService::FEATURES._

| Key | Label | Group | Gated by | Ported |
|---|---|---|---|---|
| php | PHP & website settings | Websites | sites.edit,sites.update | ☐ |
| mysql | MySQL / MariaDB databases | Databases | databases.view,databases.manage | ☐ |
| mongodb | MongoDB databases | Databases | sites.mongo | ☐ |
| email | Email | Email | mail.manage | ☐ |
| dns | DNS zone editor | Domains | dns.manage | ☐ |
| file_manager | File manager | Files | files.manage | ☐ |
| ssl | SSL / HTTPS | Websites | ssl.view,ssl.manage | ☑ |
| subdomains | Subdomains | Domains | sites.subdomains | ☐ |
| addon_domains | Addon domains | Domains | sites.addons | ☐ |
| cloudflare | Cloudflare | Websites | sites.cloudflare | ☑ |
| ftp | FTP / SFTP accounts | Files | ftp.manage | ☑ |
| cron | Cron jobs | Automation | cron.view,cron.manage | ☑ |
| backups | Backups | Backups | backups.view,backups.manage | ☑ |
| auto_backups | Show scheduled (automatic) backups to the customer | Backups | (toggle) | ☑ |
| apps | Application installer | Websites | apps.install | ☑ |
| git | Git deployment | Automation | deploy.manage | ☑ |
| workers | Workers (background processes) | Automation | workers.manage | ☐ |
| redis | Redis | Caching | sites.redis | ☑ |
| memcached | Memcached | Caching | sites.memcached | ☑ |
| nodejs | Node.js runtime & settings | Runtimes | sites.nodejs | ☐ |
| python | Python runtime & settings | Runtimes | sites.python | ☐ |
| logs | Logs | Monitoring | sites.logs | ☐ |
| stats | Visitor statistics | Monitoring | sites.stats | ☑ |
| site_health | Site health | Monitoring | sites.health | ☑ |
| malware | Malware scan | Security | sites.malware | ☑ |
| firewall | Web firewall (ModSecurity) | Security | sites.firewall | ☑ |
| site_security | Site security overview | Security | sites.security | ☑ |
| ssh | SSH shell & web terminal | Files | sites.terminal | ☐ |
| wordpress | WordPress Toolkit | Websites | sites.wordpress,wordpress | ☐ |
| type_php | Create PHP sites | Site types | (toggle) | ☐ |
| type_static | Create static sites | Site types | (toggle) | ☐ |
| type_nodejs | Create Node.js sites | Site types | (toggle) | ☐ |
| type_python | Create Python sites | Site types | (toggle) | ☐ |
| type_proxy | Create reverse-proxy sites | Site types | (toggle) | ☐ |
