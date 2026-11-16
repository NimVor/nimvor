---
title: Set up backups and restore a website
slug: set-up-backups-and-restore-a-website
excerpt: Choose where backups go, schedule them, encrypt them and restore a whole website or a single file.
date: 2026-11-11
---
A backup you have never restored is a hope, not a backup. This guide sets up NimVor backups with a destination, a schedule and retention, and then restores from them.

## Step 1: Four kinds of backup

![Four kinds of backup (NimVor panel)](/assets/blog/set-up-backups-and-restore-a-website/01.jpg)

The Backups page has four tabs. Website backups are copies of a single website: its files and databases. Server configuration backs up the settings of the server itself. Remote storage is where copies are kept off the machine. And Account backups pack a whole hosting account into one file you can download and move.

## Step 2: Back up one website now

![Back up one website now (NimVor panel)](/assets/blog/set-up-backups-and-restore-a-website/02.jpg)

To back up a website, open it and choose Backups. Back up now copies its files and its databases, with a label you choose. The work runs as a background task with a live log, so the website stays online while it happens.

## Step 3: Automatic backups

![Automatic backups (NimVor panel)](/assets/blog/set-up-backups-and-restore-a-website/03.jpg)

Automatic backups run on a schedule you choose: every day, every week or every month, at the hour you pick. Decide whether to include the files, the databases or both, how many copies to keep, and whether the backup is encrypted. Older copies are removed when the limit is reached.

## Step 4: Server configuration and the backup key

![Server configuration and the backup key (NimVor panel)](/assets/blog/set-up-backups-and-restore-a-website/04.jpg)

Server configuration backs up the server's own settings: panel database, web server and mail configuration. It also creates a backup key. Backups can be encrypted with that key, so without it a stolen copy is useless. Download the key now and keep it somewhere other than this server, protected by a passphrase. Without the key, encrypted backups cannot be restored.

## Step 5: Remote storage: keep a copy somewhere else

![Remote storage: keep a copy somewhere else (NimVor panel)](/assets/blog/set-up-backups-and-restore-a-website/05.jpg)

A backup on the same disk is not a backup. Remote storage adds somewhere else to send copies: another folder or mounted disk, an S3-compatible bucket, an SFTP or FTP server, or Google Drive. Test and add checks the connection and writes a test file before anything is saved, so you know it works.

## Step 6: Storage that is off is not used

Each storage can be tested again, turned off and on, or edited. A storage that is turned off is not offered for new backups, and nothing is deleted from it.

## Step 7: Account backups: one file for everything

![Account backups: one file for everything (NimVor panel)](/assets/blog/set-up-backups-and-restore-a-website/07.jpg)

An account backup packs a whole hosting account into a single file: its websites, databases, mailboxes and settings. Choose the account, give it a label, and start. When it finishes, the file is listed and you can download it. It is the way to move an account to another NimVor server.

## Step 8: Restoring

![Restoring (NimVor panel)](/assets/blog/set-up-backups-and-restore-a-website/08.jpg)

To restore, go back to the backup you want. A website backup restores the files, the databases or both, over the live site, after taking a safety copy of what is there now. An account backup is restored from its own page, on this server or on another one: the account, its websites and mailboxes come back as they were.

## Step 9: Good habits

![Good habits (NimVor panel)](/assets/blog/set-up-backups-and-restore-a-website/09.jpg)

Keep at least one copy off the server. Keep the backup key somewhere else. And test a restore now and then, on a spare account, so you know it works before you need it.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
