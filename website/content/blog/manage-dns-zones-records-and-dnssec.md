---
title: Manage DNS zones, records and DNSSEC
slug: manage-dns-zones-records-and-dnssec
excerpt: Create a zone, add records, import and export them, choose the name server software and turn on DNSSEC.
date: 2026-11-15
---
NimVor can answer DNS for your domains with BIND, NSD, Knot DNS or PowerDNS. This guide walks through the DNS pages: zones, records, mail records, import and export, zone settings and DNSSEC. Every record is checked before it is loaded, so a typo cannot take a zone offline.

## Step 1: DNS: every zone this server answers for

![DNS: every zone this server answers for (NimVor panel)](/assets/blog/manage-dns-zones-records-and-dnssec/01.jpg)

The DNS page lists the zones this server answers for, with the number of records, the serial and whether each zone is live. A zone can belong to one of your websites or stand on its own. NimVor works with four nameserver programs: BIND, NSD, Knot DNS and PowerDNS.

## Step 2: Nameserver selection: BIND, NSD, Knot DNS or PowerDNS

![Nameserver selection: BIND, NSD, Knot DNS or PowerDNS (NimVor panel)](/assets/blog/manage-dns-zones-records-and-dnssec/02.jpg)

Nameserver selection is where you choose the program. Each choice lists its advantages and drawbacks. Switching moves every zone to the new program, checks the result before it takes over, and goes back to the old one if anything fails. Zones, records and DNSSEC settings are kept.

## Step 3: Create a zone

![Create a zone (NimVor panel)](/assets/blog/manage-dns-zones-records-and-dnssec/03.jpg)

Back on the DNS page, New zone creates a zone. Pick one of your websites, or leave it empty and type any domain. With the template ticked, NimVor adds the usual records for you: the name servers, an A record pointing at this server, a mail-friendly SPF record, a CAA record for Let's Encrypt and www.

## Step 4: The zone is live; point the domain's name servers here

The zone is created and loaded. The message tells you the last step: at your domain registrar, point the domain's name servers at the ones shown. Until then the world keeps asking the old servers.

## Step 5: Records: type, name, value, TTL

Here are the records. Each row shows the type, the name, the value and the time to live. The at sign means the domain itself. You can search, filter by type, edit a record, turn it off without deleting it, or delete it.

## Step 6: Add a record

![Add a record (NimVor panel)](/assets/blog/manage-dns-zones-records-and-dnssec/06.jpg)

Add record opens a form. The buttons at the top fill in the common cases: an A record for the domain, a CNAME for www, mail, SPF text, or a CAA record. Let's add a shop address: an A record named shop that points to the server.

## Step 7: Mail records: MX and SPF

![Mail records: MX and SPF (NimVor panel)](/assets/blog/manage-dns-zones-records-and-dnssec/07.jpg)

Now mail. First an A record for the mail server, named mail. Then an MX record sends the domain's email to it, with a priority; the lowest number is tried first. Names without a dot at the end are relative to the zone, so mail means mail dot your domain.

## Step 8: Turn a record off, edit it, delete it

The buttons at the end of a row edit the record, switch it off and on, or delete it. A record that is off stays in the list but is no longer published. Tick several rows to turn them on, off or delete them together.

## Step 9: Export and import

Export downloads the zone in the standard zone file format, and Import reads one: handy for moving a zone from another provider. Records that are already there are left alone.

## Step 10: Zone settings

Below the records are the zone settings. The default time to live applies to new records. Secondary servers are the addresses of other name servers that may copy the zone and are told about changes; leave it empty and transfers are refused. Load the zone again re-reads it after you reinstall the DNS server.

## Step 11: DNSSEC

The DNSSEC card signs the zone, so resolvers can tell your answers are genuine. Turn it on, then publish the DS record it shows at your registrar. BIND, Knot DNS and PowerDNS sign zones themselves; NSD does not support it.

## Step 12: Defaults: name servers and server address

![Defaults: name servers and server address (NimVor panel)](/assets/blog/manage-dns-zones-records-and-dnssec/12.jpg)

Under Settings, DNS, you set the name servers new zones use and the default IPv4 address, so every zone you create is ready for the registrar.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
