---
title: Server management: services, updates and monitoring
slug: server-management-services-updates-and-monitoring
excerpt: Watch the server's health, restart services, apply updates and read the logs.
date: 2026-11-05
---
The server pages show what the machine is doing and let you act on it: services, package updates, resource use and logs. This guide tours them.

## Step 1: The dashboard: the server at a glance

![The dashboard: the server at a glance (NimVor panel)](/assets/blog/server-management-services-updates-and-monitoring/01.jpg)

The dashboard is your server at a glance: processor, memory, disk and load, the services that are running, and your websites. The big buttons at the top create websites and accounts, and the setup banner shows what is still left to do on a new server.

## Step 2: Server: system, disks, network

![Server: system, disks, network (NimVor panel)](/assets/blog/server-management-services-updates-and-monitoring/02.jpg)

The Server page has the details. System shows the host name, the operating system, the kernel and how long the server has been up. Disks lists every mounted disk with its use, and network interfaces shows the addresses. The reload and restart buttons are here too, behind a confirmation.

## Step 3: Health: everything that needs attention in one place

![Health: everything that needs attention in one place (NimVor panel)](/assets/blog/server-management-services-updates-and-monitoring/03.jpg)

Health runs every check in one place: resources, components, security, updates, certificates, backups and tasks. Each problem says what is wrong and has an Open button that takes you straight to the page that fixes it.

## Step 4: Services: start, stop, restart

![Services: start, stop, restart (NimVor panel)](/assets/blog/server-management-services-updates-and-monitoring/04.jpg)

Services lists the programs that keep a server running: the web server, PHP, the database, mail and so on. On a real server you see whether each is running and enabled at boot, read its recent log, and start, stop or restart it. Nginx is always tested before it is reloaded or restarted, and the panel and agent cannot be stopped from this page.

## Step 5: Nginx: test, reload and tune

![Nginx: test, reload and tune (NimVor panel)](/assets/blog/server-management-services-updates-and-monitoring/05.jpg)

The Nginx page controls the web server. Test config checks the configuration without touching anything, and Reload applies it. Tuning sets the largest upload, timeouts, compression and whether the server version is hidden. Every change is checked with nginx dash t first, and the old file is kept if the check fails.

## Step 6: Monitoring: live and history

![Monitoring: live and history (NimVor panel)](/assets/blog/server-management-services-updates-and-monitoring/06.jpg)

Monitoring shows the last day, week or month of processor, memory, load, disk and network, with alerts when something crosses a limit. It fills in as samples arrive, one every five minutes, so a new server shows a short history at first. Live updates every few seconds and lists the busiest processes, so you can see what is using the server right now.

## Step 7: PHP Manager: versions and extensions

![PHP Manager: versions and extensions (NimVor panel)](/assets/blog/server-management-services-updates-and-monitoring/07.jpg)

The PHP Manager chooses which PHP versions the server offers and which extensions each has. Pick versions, pick extensions, review the changes, and apply. Websites can then choose any installed version on their own.

## Step 8: Setup wizard and hostname references

![Setup wizard and hostname references (NimVor panel)](/assets/blog/server-management-services-updates-and-monitoring/08.jpg)

The setup wizard walks a new server through the profile, the PHP versions and the components. And if you change the server host name, the hostname references page scans for the old name in configuration files and tells you where it still appears.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
