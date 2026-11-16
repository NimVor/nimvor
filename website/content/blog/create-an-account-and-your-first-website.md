---
title: Create a hosting account and its first website
slug: create-an-account-and-your-first-website
excerpt: From a hosting plan to a customer account and a live website, step by step with screenshots of the NimVor panel.
date: 2026-11-16
---
A hosting account in NimVor is a customer with a plan, a login and one or more websites. This guide goes from an empty panel to a first website, with a screenshot of each step. The server in the pictures is a demo server with sample data.

## Step 1: Hosting plans

![Hosting plans (NimVor panel)](/assets/blog/create-an-account-and-your-first-website/01.jpg)

A hosting plan sets what a customer may use: websites, databases, email accounts, disk space, bandwidth and more. Zero means unlimited. We create a plan called Starter for small sites.

## Step 2: Create an account

![Create an account (NimVor panel)](/assets/blog/create-an-account-and-your-first-website/02.jpg)

Now the account. New account asks for the domain, a user name and a password. Generate makes a strong password for you. The user name is created from the domain if you leave it empty.

## Step 3: Plan, PHP version and mail

![Plan, PHP version and mail (NimVor panel)](/assets/blog/create-an-account-and-your-first-website/03.jpg)

Choose the plan and the PHP version for the first website. Mail routing says where the domain's email is delivered: automatic, on this server, a backup server, or a remote mail server. Spam filtering can be on from the start.

## Step 4: DNS records for the new domain

![DNS records for the new domain (NimVor panel)](/assets/blog/create-an-account-and-your-first-website/04.jpg)

The DNS settings add the records the domain needs the moment it exists: DKIM and SPF for mail, a DMARC policy, and the name servers of this server. Resellers can also be created here, with their own limits.

## Step 5: The account is ready

The account is created: the system user, the home folder, the website and its web server and PHP configuration, all checked before they were switched on. The customer can sign in with the user name and password.

## Step 6: Websites

![Websites (NimVor panel)](/assets/blog/create-an-account-and-your-first-website/06.jpg)

The Websites page lists every website. Add site creates another one for an existing customer.

## Step 7: Create a website

A website needs a domain, and optionally aliases such as www. Choose what it runs: plain files, PHP, WordPress, a Node.js or a Python application. Pick the PHP version, the folder served, the system user and the owner.

## Step 8: PHP limits and performance

Below are the PHP limits: memory, upload size and run time, and the worker pool. The profiles fill in sensible values for a small site or a busy one. The defaults are safe, so most sites never need to change them.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
