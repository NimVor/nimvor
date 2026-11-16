---
title: Configure the firewall
slug: configure-the-firewall
excerpt: Open and close ports, allow or block addresses and keep yourself from being locked out.
date: 2026-11-10
---
NimVor manages the server firewall for you and checks a change before it is applied. This guide covers the firewall pages: rules, allowed and blocked addresses, and the safety net that keeps you from locking yourself out.

## Step 1: The security overview

![The security overview (NimVor panel)](/assets/blog/configure-the-firewall/01.jpg)

The Security page gives you the picture: how many failed logins today, yesterday and this week, the last logins to SSH and to the panel, and a list of protections with a button to turn each on. The firewall is one of them.

## Step 2: The firewall page

![The firewall page (NimVor panel)](/assets/blog/configure-the-firewall/02.jpg)

The Firewall page shows whether the firewall is on, which program does the work, and the SSH port. NimVor uses the firewall your system has, such as ufw or firewalld, and presents the same screen for either.

## Step 3: Ports NimVor keeps open

Before anything is blocked, NimVor lists the ports it keeps open: SSH, so you cannot lock yourself out, the panel, and the ports of the services that are running, such as websites, mail, FTP and DNS. With the box ticked they are opened for you when the firewall is turned on.

## Step 4: Turn it on with a safety net

![Turn it on with a safety net (NimVor panel)](/assets/blog/configure-the-firewall/04.jpg)

Turn on firewall applies the rules, and then gives you two minutes to confirm that you can still connect. If something went wrong and the panel stops answering, you do nothing, and the change is undone by itself. This safety net applies to every change that could cut your access.

## Step 5: Keep the change

![Keep the change (NimVor panel)](/assets/blog/configure-the-firewall/05.jpg)

Open a second session to test, then Keep change makes it permanent. Undo now puts everything back immediately.

## Step 6: Add a rule

![Add a rule (NimVor panel)](/assets/blog/configure-the-firewall/06.jpg)

Rules are for single ports and addresses. Add rule asks what to do: allow, allow with a rate limit, deny quietly, or reject with a message. Then the protocol, the port or range, and optionally an address to apply it to. Let's allow a development port, 8080.

## Step 7: Block an address

![Block an address (NimVor panel)](/assets/blog/configure-the-firewall/07.jpg)

To block one address, add a rule that denies everything from it. The rule list has three views: all rules, port rules and IP rules. You can search, switch direction between inbound and outbound, and delete a rule with one click. The last rule that keeps SSH and the panel open cannot be deleted while the firewall is on.

## Step 8: Protection against floods

![Protection against floods (NimVor panel)](/assets/blog/configure-the-firewall/08.jpg)

Further down, the attack protection settings: drop invalid packets, limit new connections per second, answer pings at a limited rate, cap simultaneous connections per port and rate-limit specific ports. Addresses you trust can be exempted. The defaults suit most servers.

## Step 9: Brute-force protection

![Brute-force protection (NimVor panel)](/assets/blog/configure-the-firewall/09.jpg)

Brute Force Protection watches failed logins to SSH, FTP, mail and the panel, and blocks the address that keeps guessing. You choose how many failures in how many minutes, how long the block lasts, and which addresses are never blocked. Blocks can be applied at the firewall for every port, or only for the login services.

## Step 10: Countries and reports

![Countries and reports (NimVor panel)](/assets/blog/configure-the-firewall/10.jpg)

The Countries tab can block whole countries from the login services, or from every port. The Failed Logins report lists who tried what, with the country of each address, and a button to block or unblock.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
