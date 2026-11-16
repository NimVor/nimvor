---
title: Install and tune ModSecurity
slug: install-and-tune-modsecurity
excerpt: Add the web application firewall, choose a rule set, read its events and exclude a rule that breaks a site.
date: 2026-11-09
---
ModSecurity is a web application firewall that blocks common attacks before they reach your website. This guide installs it, picks a rule set, shows how to read its events and how to switch off a rule that misfires.

## Step 1: What ModSecurity does

![What ModSecurity does (NimVor panel)](/assets/blog/install-and-tune-modsecurity/01.jpg)

ModSecurity is a web application firewall. It looks at every request before your website sees it and blocks the ones that look like attacks: SQL injection, cross-site scripting, attempts to read files that should not be readable, and well-known scanners. The rules come from the OWASP Core Rule Set, which is maintained by the security community.

## Step 2: Installation: the Nginx connector

![Installation: the Nginx connector (NimVor panel)](/assets/blog/install-and-tune-modsecurity/02.jpg)

Nginx needs a small module, the connector, to talk to ModSecurity. The Installation tab shows its state: missing, installed but not usable, or working. If your distribution has the connector as a package, NimVor uses it. If not, it builds the module from source for your exact Nginx version, using pinned, checksum-verified releases.

## Step 3: Install in one transaction

![Install in one transaction (NimVor panel)](/assets/blog/install-and-tune-modsecurity/03.jpg)

Install opens a window that explains what will happen: a temporary unprivileged account builds the module, everything is backed up first, and each step is checked: the build, the libraries, the module load, nginx dash t, and the reload. If anything fails, the whole installation is rolled back and your web server keeps running exactly as before.

The Nginx program itself is never replaced.

## Step 4: The OWASP Core Rule Set

Next, the rules. The Vendors tab installs the OWASP Core Rule Set, from the distribution package or the verified release. It is turned on in detection only: nothing is blocked yet, so you can see what it would block before it blocks anything.

## Step 5: Server-wide settings

Server-wide settings turns ModSecurity on or off for the whole server and sets the paranoia level and the anomaly thresholds. A higher paranoia level catches more, and also makes more mistakes. The Detection only switch keeps logging without blocking, which is the safe way to start.

## Step 6: Per website: off, detect, or block

![Per website: off, detect, or block (NimVor panel)](/assets/blog/install-and-tune-modsecurity/06.jpg)

Each website has its own mode. Off does nothing. Detect only logs. On blocks. You can also turn off a single rule for one website, for example when a plugin triggers a false alarm. A new website starts in detection, and you switch to blocking when its events look clean.

## Step 7: Custom rules

![Custom rules (NimVor panel)](/assets/blog/install-and-tune-modsecurity/07.jpg)

Custom rules are your own ModSecurity rules, for example to block a path or a bad address. They use rule numbers from 10000 to 99999, and only rule directives are allowed: no programs, files or includes. Every change is checked with nginx dash t, and the previous rules are kept if the check fails.

## Step 8: Recent events

![Recent events (NimVor panel)](/assets/blog/install-and-tune-modsecurity/08.jpg)

Recent events lists what ModSecurity caught: the time, the website, the address, the request, and the rules that matched. Use it to decide whether a rule is right, or to turn one off for one site.

## Step 9: Updates and rollback

![Updates and rollback (NimVor panel)](/assets/blog/install-and-tune-modsecurity/09.jpg)

Rules and the connector can be updated from the Installation tab. If the new rules break the configuration, the Core Rule Set is switched off and the rest is restored.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
