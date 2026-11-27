# Service logos

PNG renderings (256 × 256, transparent) of the marks of the software NimVor manages, used by `View::logo()` on the Software
tiles, chips, application-type cards and window titles, so a card says "Nginx" with Nginx's own mark.

| File | Software | Source |
|---|---|---|
| nginx.png, php.png, mariadb.png, mongodb.png, postgresql.png, redis.png, nodejs.png, python.png | Nginx, PHP, MariaDB, MongoDB, PostgreSQL, Redis, Node.js, Python | [devicon](https://github.com/devicons/devicon) 2.16.0 (MIT licence), `*-original.svg`, rendered to PNG |

The marks and names belong to their projects and owners. They are shown only to identify the software NimVor installs and
manages (nominative use) and say nothing about endorsement. Software without a logo here (Postfix, Dovecot, PowerDNS, FTP, the
firewall and so on) keeps an ordinary icon; add a logo only when its licence allows redistribution, and list it above.

To add one: put `NAME.png` here and map the panel's id for it in `View::LOGOS` (`app/View/View.php`).
