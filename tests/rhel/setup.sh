#!/usr/bin/env bash
#
# Runs inside the AlmaLinux test VM: installs what the real-host tests use,
# the way an administrator would (base repositories, EPEL, Remi for PHP,
# Rspamd's repository), and starts the services. RHEL 10 ships Valkey
# instead of Redis.

set -Eeuo pipefail
major=$(. /etc/os-release; echo "${VERSION_ID%%.*}")
dnf install -y -q epel-release dnf-plugins-core
dnf config-manager --set-enabled crb
dnf install -y -q "https://rpms.remirepo.net/enterprise/remi-release-${major}.rpm"
curl -fsSL -o /etc/yum.repos.d/rspamd.repo "https://rspamd.com/rpm-stable/centos-${major}/rspamd.repo"
rpm --import https://rspamd.com/rpm/gpg.key
redis=redis
[[ $major -ge 10 ]] && redis=valkey
if [[ $major == 9 ]]; then
    # The panel's own PHP (8.3); websites use Remi's parallel versions.
    dnf module reset -y -q php
    dnf module enable -y -q php:remi-8.3
fi
dnf install -y -q --setopt=install_weak_deps=False \
    nginx mariadb-server mariadb "$redis" memcached pure-ftpd bind bind-utils \
    postfix dovecot dovecot-pigeonhole opendkim opendkim-tools rspamd \
    firewalld fail2ban fail2ban-systemd openssh-server git tar rsync curl openssl \
    policycoreutils-python-utils dnf-utils roundcubemail \
    php-cli php-pdo php-mbstring php-sodium \
    php83-php-fpm php83-php-cli php83-php-mysqlnd php83-php-pdo php83-php-mbstring php83-php-xml \
    php83-php-intl php83-php-zip php83-php-gd php83-php-curl php83-php-opcache
command -v composer >/dev/null || dnf install -y -q composer || true
systemctl enable --now mariadb "$redis" memcached nginx php83-php-fpm named firewalld fail2ban
firewall-cmd --permanent --add-service=ssh >/dev/null
firewall-cmd --reload >/dev/null
nginx -t
mysql -e 'select version()'
# Log denials that the policy normally hides (dontaudit), so CI shows them.
semodule -DB
getenforce
