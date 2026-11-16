---
title: SSL certificates and TLS settings
slug: ssl-certificates-and-tls-settings
excerpt: Free Let's Encrypt certificates, your own certificates, AutoSSL, keys and CSRs, and shared TLS settings.
date: 2026-11-14
---
HTTPS should be the default for every website. This guide shows how NimVor issues and renews free certificates, installs one you bought, creates keys and certificate requests, and sets the TLS protocols for every site at once.

## Step 1: SSL overview: every host name and its certificate

![SSL overview: every host name and its certificate (NimVor panel)](/assets/blog/ssl-certificates-and-tls-settings/01.jpg)

The SSL page lists every host name on the server with its certificate: whether HTTPS works, who issued the certificate, how many days are left and whether NimVor manages it. The summary at the top counts the secure ones, the ones that need attention and the ones without HTTPS.

## Step 2: A free certificate for a website

![A free certificate for a website (NimVor panel)](/assets/blog/ssl-certificates-and-tls-settings/02.jpg)

To secure a website, open it and choose SSL. A free Let's Encrypt certificate is one click: Issue certificate. NimVor proves you control the domain, installs the certificate, redirects HTTP to HTTPS and renews it before it expires. Include aliases covers the www name as well. The staging option tries the whole process against a practice server first.

A new website also gets one automatically once its domain points here.

## Step 3: Security headers and your own certificate

![Security headers and your own certificate (NimVor panel)](/assets/blog/ssl-certificates-and-tls-settings/03.jpg)

Security headers adds the common protective headers to the website. And if you bought a certificate, paste it here: the certificate, its private key and, if you have one, the chain. NimVor checks that the key matches the certificate and that the chain is complete before it installs anything, and keeps the old certificate if the check fails.

## Step 4: AutoSSL: all websites at once

![AutoSSL: all websites at once (NimVor panel)](/assets/blog/ssl-certificates-and-tls-settings/04.jpg)

AutoSSL does this for every website automatically. It issues certificates for new domains, renews the ones that are close to expiring, and keeps a log of what it did. You can run it for all websites by hand, and the renewal log from certbot is shown below it.

## Step 5: Certificates on this server

![Certificates on this server (NimVor panel)](/assets/blog/ssl-certificates-and-tls-settings/05.jpg)

The certificate list shows every certificate on the server: the names it covers, the issuer, the expiry date and where the file lives. Private keys are never shown.

## Step 6: Keys and certificate requests

![Keys and certificate requests (NimVor panel)](/assets/blog/ssl-certificates-and-tls-settings/06.jpg)

Keys and CSR creates a private key and a certificate signing request. A CSR is what you send to a certificate authority when you buy a certificate. Fill in the host names and the organisation details, choose the key type, and the key stays on this server. You can also tick self-signed to make a certificate right away for testing.

## Step 7: TLS settings: protocols and ciphers

![TLS settings: protocols and ciphers (NimVor panel)](/assets/blog/ssl-certificates-and-tls-settings/07.jpg)

TLS settings controls how websites talk to browsers. Choose the oldest protocol version to allow: TLS 1.2 for the widest reach, or TLS 1.3 only. The cipher choice is a default or the intermediate profile. Applying it rewrites one shared file that every website uses, checks it with nginx dash t, and reloads the web server.

## Step 8: SSL for the panel, mail and the server host name

![SSL for the panel, mail and the server host name (NimVor panel)](/assets/blog/ssl-certificates-and-tls-settings/08.jpg)

Under SSL services you register your Let's Encrypt account once, and then secure the panel itself, the mail server and the server's own host name, each with its own certificate.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
