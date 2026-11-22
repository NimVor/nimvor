---
title: Set up alerts and thresholds
slug: set-up-alerts-and-thresholds
excerpt: Choose when NimVor raises an alert, and how to acknowledge or resolve one when it does.
date: 2026-11-22
---
NimVor raises an alert when something on the server needs a person: a website that stopped answering, a backup that did not run, security updates that are waiting, a mail queue that is not draining, a disk that is filling up. This guide shows where the thresholds are, what each one means and what to do with an alert once it is open. The screenshots come from a demo server, so the Alert center is empty.

## Step 1: The Alert center

![The Alert center widget (NimVor panel)](/assets/blog/set-up-alerts-and-thresholds/01.jpg)

The **Alert center** on the dashboard counts the open alerts by severity and the ones resolved in the last 24 hours. With alerts open it lists them, most severe first. There is only ever one open alert per problem: if a website keeps failing, you do not get a new alert every five minutes, you get one alert that stays open while the problem lasts and closes by itself when the problem is gone.

## Step 2: Open the monitoring settings

![Settings, Monitoring and alerts (NimVor panel)](/assets/blog/set-up-alerts-and-thresholds/02.jpg)

Go to **Settings** and choose **Monitoring and alerts**. The thresholds for the whole server are in one place. The basic ones raise an alert when the disk, the memory or the load per CPU core goes above a level you choose. A reading that cannot be measured never raises an alert, and never closes one either.

## Step 3: The website, backup, update, mail and disk rules

![The alert rules for websites, backups, updates, mail and disk (NimVor panel)](/assets/blog/set-up-alerts-and-thresholds/03.jpg)

Scroll down for the rules that use the dashboard's own data. **Website down** raises an alert when a website fails this many checks in a row (a check runs about every five minutes). **Slow website** raises one when a website needs longer than the time you set to answer in its last three checks. **Missing backup** looks only at websites with an enabled backup schedule and raises an alert when none completed for this many days. **Security updates** and **Mail queue** count what is waiting, and **Disk filling** looks at the forecast: it only speaks when there are about a week of measurements to base it on. Each field says what a value of 0 does: it turns that rule off.

## Step 4: Acknowledge and resolve

An alert in the Alert center has two buttons for administrators. **Acknowledge** says you have seen it: the alert stays open, is shown dimmed and no longer counts in the severity totals. **Resolve** closes it by hand. If you resolve an alert and the same problem is still there within a day, it comes back as the same alert, already acknowledged, and nobody is notified a second time. Both actions are written to the audit log.

## Step 5: Leave a website out

Some websites are meant to be offline, or cannot be reached from the server itself, and their failing checks would raise an alert you do not want. Open the website, go to **Advanced** and turn **Uptime checks** off under Monitoring. The website is then left out of the dashboard's checks, the uptime figures and the alerts, its open alert closes at the next monitoring pass, and its earlier results are kept. Turn the checks on again whenever you like. (This switch comes with the first release after 1.9.0.)
