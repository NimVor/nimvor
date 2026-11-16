---
title: Install and update server components
slug: install-and-update-server-components
excerpt: See which server software is installed, add what you need and keep it updated.
date: 2026-11-06
---
The Components page lists the software NimVor manages on the server, what is installed and what can be added. This guide shows how to install and update it safely.

## Step 1: Components: the software your server runs

![Components: the software your server runs (NimVor panel)](/assets/blog/install-and-update-server-components/01.jpg)

Components shows the software your server can run, grouped by job: PHP, the web server, the database, email, DNS, FTP, caches such as Redis and Memcached, MongoDB, the firewall, Fail2ban, the malware scanner and Let's Encrypt. Each card lists what it needs, with a mark for every step that is done and a clear note for every step that is not.

## Step 2: Tell NimVor what this server is for

![Tell NimVor what this server is for (NimVor panel)](/assets/blog/install-and-update-server-components/02.jpg)

The button What this server should have lets you tick the components this server is meant to run. Ticked ones are checked and shown first; the rest are tucked away under Not used on this server. Nothing is installed or removed by this window; it only decides what the health checks expect. Let's say this server needs Redis and Memcached for website caches.

## Step 3: Install what is missing

![Install what is missing (NimVor panel)](/assets/blog/install-and-update-server-components/03.jpg)

Redis and Memcached now show first, each with a clear message: not installed. The Install button on a card adds the missing packages to the installation queue.

## Step 4: The installation queue

![The installation queue (NimVor panel)](/assets/blog/install-and-update-server-components/04.jpg)

A window follows the queue, one item at a time, so two installs can never fight over the package manager lock. Each item shows waiting, installing, installed or failed, with the log of what was done. You can close the window and come back: the queue keeps running on the server.

## Step 5: Packages: installs and updates in one place

![Packages: installs and updates in one place (NimVor panel)](/assets/blog/install-and-update-server-components/05.jpg)

The Packages page is the single place for software. The numbers at the top show updates waiting, security updates, how old the package index is, components installed, missing or not working, and what is in the queue.

## Step 6: Updates

![Updates (NimVor panel)](/assets/blog/install-and-update-server-components/06.jpg)

The Updates tab lists the packages that can be upgraded and marks the security ones. Check for updates refreshes the list from your distribution as a background task with a live log, so closing the browser does not interrupt it. Choose what to upgrade and the work runs the same way.

## Step 7: When something fails

![When something fails (NimVor panel)](/assets/blog/install-and-update-server-components/07.jpg)

If an install fails, the previous state is restored and the log says why: a held package, no disk space, a repository that is down. Nothing is left half installed. When it succeeds, the matching pages appear in the panel: Redis gets its key browser, Memcached its statistics, and so on.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
