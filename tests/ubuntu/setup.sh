#!/usr/bin/env bash
#
# Runs inside an Ubuntu test VM (22.04, 26.04): installs what the
# real-host tests use, the way an administrator would, and starts the
# services. Writes the PHP version websites are tested with to
# /etc/whacp-ci-php.
#   22.04 ships PHP 8.1: PHP 8.3 comes from the ondrej/php PPA (as the
#         installation guide says for the panel).
#   Newer releases: the distribution's own PHP.

set -Eeuo pipefail
export DEBIAN_FRONTEND=noninteractive
. /etc/os-release

# Background upgrades would hold the package lock.
systemctl stop unattended-upgrades apt-daily.timer apt-daily-upgrade.timer 2>/dev/null || true
while fuser /var/lib/dpkg/lock-frontend /var/lib/apt/lists/lock >/dev/null 2>&1; do sleep 2; done

apt-get update -q
if [[ $VERSION_ID == 22.04 ]]; then
    apt-get install -y -q --no-install-recommends software-properties-common
    add-apt-repository -y ppa:ondrej/php
    apt-get update -q
    php=8.3
else
    apt-get install -y -q --no-install-recommends php-cli
    php=$(php -r 'echo PHP_MAJOR_VERSION . "." . PHP_MINOR_VERSION;')
fi
echo "$php" > /etc/whacp-ci-php

echo "postfix postfix/main_mailer_type select Internet Site" | debconf-set-selections
echo "postfix postfix/mailname string ci.whacp.test" | debconf-set-selections
echo "roundcube-core roundcube/dbconfig-install boolean false" | debconf-set-selections
apt-get install -y -q --no-install-recommends \
    nginx mariadb-server mariadb-client redis-server memcached pure-ftpd bind9 bind9-utils \
    postfix dovecot-imapd dovecot-lmtpd dovecot-sieve opendkim opendkim-tools rspamd \
    ufw fail2ban openssh-server git curl rsync openssl ca-certificates unzip \
    roundcube-core roundcube-sqlite3 roundcube-plugins \
    "php${php}-cli" "php${php}-fpm" "php${php}-mysql" "php${php}-sqlite3" "php${php}-mbstring" \
    "php${php}-xml" "php${php}-curl" "php${php}-zip" "php${php}-intl"
# The panel's CLI is PHP >= 8.3 (on 22.04 the PPA's version).
update-alternatives --set php "/usr/bin/php${php}" 2>/dev/null || true
command -v composer >/dev/null || curl -fsSL https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer

systemctl enable --now mariadb redis-server memcached nginx "php${php}-fpm" ssh named
ufw allow 22/tcp
ufw --force enable
systemctl restart fail2ban
nginx -t
mysql -e 'select version()'
echo "PHP for websites: $php ($(php -r 'echo PHP_VERSION;') for the CLI)"
