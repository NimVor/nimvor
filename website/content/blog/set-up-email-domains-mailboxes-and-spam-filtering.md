---
title: Set up email: domains, mailboxes and spam filtering
slug: set-up-email-domains-mailboxes-and-spam-filtering
excerpt: Turn on the mail server, add a domain and mailboxes, publish the DNS records and read your mail in the browser.
date: 2026-11-13
---
NimVor sets up Postfix, Dovecot, DKIM signing and spam filtering and gives each mail domain its own pages. This guide takes you from the mail server to a working mailbox, with a screenshot of each step.

## Step 1: The mail server: one time setup

![The mail server: one time setup (NimVor panel)](/assets/blog/set-up-email-domains-mailboxes-and-spam-filtering/01.jpg)

Email starts with the mail server. NimVor uses Postfix to send and receive, Dovecot to let people read their mail, OpenDKIM to sign it and Rspamd to filter spam. If they are installed, the Email page asks for one thing: the host name of the mail server. Mail names such as mail dot your domain point at it, so it must resolve to this server.

## Step 2: Set up and start

![Set up and start (NimVor panel)](/assets/blog/set-up-email-domains-mailboxes-and-spam-filtering/02.jpg)

Tick the confirmation and press Set up and start. NimVor changes the Postfix and Dovecot configuration, keeps a backup of every file it touches, and checks the result before it switches the new configuration on. Afterwards it tells you which ports to open in the firewall: 25, 587 and 993.

## Step 3: Mail settings at a glance

The Email page now shows the server: the address for reading mail, the address for sending, and three switches. The spam filter scans incoming mail. Port 465 adds the older secure submission port for programs that need it. And webmail gives users a browser inbox.

## Step 4: Host email for a domain

![Host email for a domain (NimVor panel)](/assets/blog/set-up-email-domains-mailboxes-and-spam-filtering/04.jpg)

To receive mail for a domain, choose Host email for a domain. Pick one of your websites, or type any domain. NimVor creates the DKIM signing key and shows the DNS records the domain needs.

## Step 5: Mailboxes

![Mailboxes (NimVor panel)](/assets/blog/set-up-email-domains-mailboxes-and-spam-filtering/05.jpg)

Now the mailboxes. Create mailbox asks for the part before the at sign, a password, and a size limit in megabytes. Generate a strong password creates one and shows it once, so you can pass it on. Let's make info.

## Step 6: Forwarders and aliases

![Forwarders and aliases (NimVor panel)](/assets/blog/set-up-email-domains-mailboxes-and-spam-filtering/06.jpg)

A forwarder sends the mail for one address to one or more others. Add forward: sales at the domain goes to info. Anyone can write to sales, and the mail lands in the info mailbox. A forwarder can also point outside, to an address at another provider.

## Step 7: Mail routing

Mail routing says where the domain's mail goes. Local mail exchanger is this server. Backup mail exchanger keeps mail for another server that is down. Remote mail exchanger sends everything to a different mail provider, such as Google or Microsoft, and then there are no local mailboxes.

## Step 8: DNS records for mail to work

Email authentication lists the DNS records the domain needs: an MX record that sends mail here, SPF which says this server may send for the domain, DKIM which signs every message, and DMARC which tells receivers what to do with mail that fails. If NimVor hosts the DNS zone, Add to DNS zone publishes them for you. Check DNS compares the live records with what is expected.

## Step 9: Mail names and app settings

Turn on mail names gives the domain its own mail host, with a free certificate, so mail programs show your customer's name and no certificate warning. Mail app settings lists what to type into a mail program: the user name is the full address, incoming mail is IMAP on port 993 and outgoing mail is SMTP on port 587 with sign-in.

## Step 10: Filters and autoresponders

![Filters and autoresponders (NimVor panel)](/assets/blog/set-up-email-domains-mailboxes-and-spam-filtering/10.jpg)

Each mailbox has its own filters: move, mark or delete messages by sender or subject, forward them, or send an automatic reply while someone is away. The filters run on the server, so they work with every mail program.

## Step 11: The mail queue and the log

![The mail queue and the log (NimVor panel)](/assets/blog/set-up-email-domains-mailboxes-and-spam-filtering/11.jpg)

For the administrator, the Mail server page shows Postfix, Dovecot, OpenDKIM and Rspamd, with start, stop and restart, and their logs. The queue lists messages waiting to be delivered, with the reason for the delay: retry them, deliver now, hold, release or delete.

## Step 12: Troubleshoot a message

![Troubleshoot a message (NimVor panel)](/assets/blog/set-up-email-domains-mailboxes-and-spam-filtering/12.jpg)

The tools page traces an address: type someone at your domain and NimVor follows the route a message would take, and tells you where it would end up and why. Repair mailbox permissions fixes ownership after a migration.

## Step 13: Greylisting and sending limits

![Greylisting and sending limits (NimVor panel)](/assets/blog/set-up-email-domains-mailboxes-and-spam-filtering/13.jpg)

Two settings stop abuse. Greylisting asks unknown senders to try again later, which real mail servers do and most spam programs do not. Sending limits cap how many messages a website account can send each hour, so a hacked contact form cannot send thousands of spam messages. Both are off until you turn them on.

## Step 14: Delivery reports

![Delivery reports (NimVor panel)](/assets/blog/set-up-email-domains-mailboxes-and-spam-filtering/14.jpg)

The delivery reports summarise what was sent, delivered, deferred and bounced, so you can see at a glance when something is wrong.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
