---
title: Manage WordPress with the WordPress Toolkit
slug: manage-wordpress-with-the-wordpress-toolkit
excerpt: Install WordPress, keep it updated, check its security and take backups from one page.
date: 2026-11-12
---
The WordPress Toolkit installs WordPress on a website and keeps it healthy: updates, plugins, themes, security checks and backups. This guide shows each part of it.

## Step 1: Install WordPress in one click

![Install WordPress in one click (NimVor panel)](/assets/blog/manage-wordpress-with-the-wordpress-toolkit/01.jpg)

The quickest way to a WordPress site is the app installer on the website. Open the website, choose Applications, and pick WordPress from the list. NimVor shows a plan before it does anything: the database and user it will create, the files it will put in place, and the address of the admin panel.

## Step 2: After installing: the toolkit finds it

![After installing: the toolkit finds it (NimVor panel)](/assets/blog/manage-wordpress-with-the-wordpress-toolkit/02.jpg)

When WordPress is on a website, the WordPress Toolkit finds it. The WordPress page in the main menu lists every installation on the server, with its version, whether updates are waiting, and whether something needs a look. Scan all websites searches for installations that were put there by hand or arrived in a migration.

## Step 3: The installation dashboard

This is the toolkit page of one installation. At the top: the site name, the WordPress version, and a button when an update is available. Below it, a strip of tools: the File Manager opened at the WordPress folder, the debug log, cloning, and restore points.

## Step 4: Updates with a safety net

Update takes a restore point first, so a failed update can be undone with one click. WordPress core, plugins and themes are listed with their versions. Turn on automatic updates for the ones you trust, and leave the delicate plugins to be updated by hand.

## Step 5: Switches

The switches cover what people most often need. Search engine indexing can be turned off while the site is being built. Debugging turns on the WordPress debug log. Password protection puts a login in front of the whole site, for a staging copy. Hotlink protection stops other sites from embedding your images.

## Step 6: Security: one-click hardening

![Security: one-click hardening (NimVor panel)](/assets/blog/manage-wordpress-with-the-wordpress-toolkit/06.jpg)

The Security tab lists the protective measures and applies the recommended ones in one click: block access to files such as wp-config and readme, forbid PHP in the uploads folder, stop author scans from revealing user names, and refuse known attack tools. XML-RPC is left open unless you close it, because some apps and Jetpack need it.

The rules are written to the web server, checked with nginx dash t, and rolled back if the check fails.

## Step 7: Staging and cloning

![Staging and cloning (NimVor panel)](/assets/blog/manage-wordpress-with-the-wordpress-toolkit/07.jpg)

The Staging tab clones the installation: files and database go to another website on the same server, with the links rewritten, so you can try a redesign or an update safely. Push the changes back when you are happy, or throw the copy away.

## Step 8: Vulnerability protection and logs

![Vulnerability protection and logs (NimVor panel)](/assets/blog/manage-wordpress-with-the-wordpress-toolkit/08.jpg)

The toolkit checks your plugins and themes against a list of known vulnerabilities and flags the ones that need attention. The Logs page shows the WordPress debug log without opening a terminal, which is where you look first when a white screen appears.

## Step 9: Backups and the rest of the toolbox

Backups, SSL, the File Manager, the database browser and cron jobs are all on the same website menu, so a WordPress site is looked after in one place: updated, protected, copied and restored without leaving the panel.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
