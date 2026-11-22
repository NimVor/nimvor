---
title: Read and arrange the dashboard
slug: read-and-arrange-the-dashboard
excerpt: What each dashboard widget tells you, how to move and resize them, and how often each one refreshes.
date: 2026-11-22
---
The dashboard is the first page you see after signing in. It is made of widgets: small cards that each answer one question about the server. This guide shows how to read them, and how to arrange the page so that the answers you need most are on top. The screenshots come from a demo server that has just been installed, so some widgets say there is not enough history yet. That is on purpose: NimVor shows what it has measured and never fills a gap with an invented number.

## Step 1: The top of the page

![The top of the dashboard (NimVor panel)](/assets/blog/read-and-arrange-the-dashboard/01.jpg)

The header shows the server's name, the system it runs, the NimVor version and the load averages. Below it, the CPU, memory, disk and load cards show the live readings with a small sparkline. A reading that could not be measured is shown as N/A, not as zero.

## Step 2: The widgets

![The widget area (NimVor panel)](/assets/blog/read-and-arrange-the-dashboard/02.jpg)

Everything under the **Widgets** heading is a widget. The **Alert center** counts open alerts by severity (critical, warning, information) and what was resolved in the last 24 hours. **Needs attention** lists websites with something to fix, such as mail records, certificates or backups. **Disk** shows how full each filesystem is. Each card has a link to the page behind it (for example Monitoring or Disk usage), the time it was measured, and a small refresh button that measures it again now.

## Step 3: History charts

![The performance history widget (NimVor panel)](/assets/blog/read-and-arrange-the-dashboard/03.jpg)

**Performance history** draws CPU, memory, load or network over the last hour, 6 hours, 24 hours or 7 days. The chart shows what the monitor recorded, one sample about every five minutes. When the server has just been installed, or the monitor was not running for a while, the chart says so and leaves gaps as gaps instead of connecting the dots. Readings that NimVor only started recording later, such as swap, the 5 and 15 minute load, service availability and database activity, fill in from the moment you update to the version that records them.

## Step 4: Websites, certificates and security

![Website health, statistics and certificates (NimVor panel)](/assets/blog/read-and-arrange-the-dashboard/04.jpg)

**Website health** asks every enabled website for its home page and shows how many are online, how many answer with errors, the slowest ones and the uptime over 24 hours. **SSL certificates** counts valid certificates and those about to expire, and tells you which enabled websites have no HTTPS. Further down are the security overview and the database, cache and backup widgets. If a website is meant to be offline, or cannot be reached from the server itself, you can leave it out of the checks on its Advanced page, under Monitoring (a switch that comes with the first release after 1.9.0).

## Step 5: Arrange the page

![Arrange mode (NimVor panel)](/assets/blog/read-and-arrange-the-dashboard/05.jpg)

Press **Arrange** above the widgets. Every widget now has a dashed outline and a small **S M L Full** picker. Drag a widget by its card to move it, click a size to make it narrower or wider, or drag its right edge. Keyboard users can focus a widget and press Alt with an arrow key to move it, and use the arrow keys on its edge to resize it. Each change is saved as you make it, for your own account only. Press **Done** when you are finished.

## Step 6: Choose what is shown and how often it refreshes

![The Customize dialog (NimVor panel)](/assets/blog/read-and-arrange-the-dashboard/06.jpg)

**Customize** lists every widget your role may see. Untick a widget to hide it, choose its width, move it up or down, and set its refresh: the widget's own default, only when you ask, or every 30 seconds, minute, 5 or 15 minutes. Automatic refreshes pause while the tab is in the background and while you are arranging. **Restore default** puts the standard layout back.
