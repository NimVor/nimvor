---
title: Move accounts to NimVor from another panel
slug: move-accounts-to-nimvor-from-another-panel
excerpt: Copy websites, databases and mailboxes from cPanel, Plesk, DirectAdmin, HestiaCP and others, live or from a backup file.
date: 2026-11-04
---
You do not have to rebuild a server by hand to move to NimVor. The migration wizard copies websites, databases and mailboxes from another panel, either over SSH from the live server or from a backup file you copied over, and can be resumed or rolled back.

## Step 1: Start a migration

![Start a migration (NimVor panel)](/assets/blog/move-accounts-to-nimvor-from-another-panel/01.jpg)

Migrations lists every move you have started and where it stands. Choose New migration to begin: nothing is changed on the old server, and nothing is changed here until you press Start.

## Step 2: Step 1: choose where the account comes from

![Step 1: choose where the account comes from (NimVor panel)](/assets/blog/move-accounts-to-nimvor-from-another-panel/02.jpg)

Pick the panel the account lives on now: cPanel, Plesk, DirectAdmin, HestiaCP, VestaCP, CyberPanel, CloudPanel, aaPanel, ISPConfig and more, or a server without a panel. Each card says what is read. Backup archive imports an account backup you already copied onto this server, so no login to the old server is needed.

## Step 3: Import from a backup file

![Import from a backup file (NimVor panel)](/assets/blog/move-accounts-to-nimvor-from-another-panel/03.jpg)

Choose Backup archive when you have the old panel's backup file: a cPanel cpmove file, a DirectAdmin user backup, or a HestiaCP or VestaCP backup. NimVor lists the backup files it finds in /root, /home, /var/backups and /tmp, or you type the full path. It unpacks the file into a private folder and reads what is inside.

## Step 4: Or connect to a live server

![Or connect to a live server (NimVor panel)](/assets/blog/move-accounts-to-nimvor-from-another-panel/04.jpg)

To copy from a running server instead, choose its panel and enter the address, port and a root login. NimVor shows the server's SSH key so you can confirm it is the right machine before anything is read, and the password is never stored.

## Step 5: What happens next

![What happens next (NimVor panel)](/assets/blog/move-accounts-to-nimvor-from-another-panel/05.jpg)

After the connection you choose which websites, databases and mailboxes to move and where they go, run a compatibility check, and start. Files are copied with rsync, databases are dumped and imported, mailboxes are copied message by message, and every step can be resumed or rolled back. Switch the DNS only when you are happy with the copy.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
