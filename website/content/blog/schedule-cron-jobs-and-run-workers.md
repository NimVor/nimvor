---
title: Schedule cron jobs and run workers
slug: schedule-cron-jobs-and-run-workers
excerpt: Run a command on a schedule, or keep a long-running process alive, per account.
date: 2026-11-07
---
Cron jobs run something on a schedule and workers keep something running. This guide shows both, with the limits a hosting plan puts on them.

## Step 1: Cron jobs on the server

![Cron jobs on the server (NimVor panel)](/assets/blog/schedule-cron-jobs-and-run-workers/01.jpg)

The Cron jobs page in the main menu lists every scheduled task on the server: the system ones, the ones from packages such as certificate renewal, and the ones users added themselves. It is a read-only snapshot, so you can see everything at once. Passwords on command lines are hidden.

## Step 2: A website's own cron jobs

![A website's own cron jobs (NimVor panel)](/assets/blog/schedule-cron-jobs-and-run-workers/02.jpg)

Each website has its own Cron jobs tab. These run as the website's user, never as root, and they are counted against the hosting plan. Add cron job opens a short form.

## Step 3: Schedule and command

![Schedule and command (NimVor panel)](/assets/blog/schedule-cron-jobs-and-run-workers/03.jpg)

Choose when it runs from a list of common choices, such as every minute, every five minutes, hourly or daily, or type your own schedule. Then the command. For a Laravel site that is the scheduler: php artisan schedule colon run. A description helps you remember what it is for, and you can have the output emailed to you.

## Step 4: Run now, turn off, read the log

The job is in the list. Run now starts it immediately, so you can test a command without waiting for the schedule. The log shows the last output, and the switch turns the job off without deleting it. NimVor writes the system schedule file for you and removes it again if the website is deleted.

## Step 5: Background workers

![Background workers (NimVor panel)](/assets/blog/schedule-cron-jobs-and-run-workers/05.jpg)

A worker is different from a cron job: it is a program that must keep running, such as a queue consumer, a Node.js server or a Python application. The Workers tab of a website lists them. Add worker asks for a name, the command and the working folder.

## Step 6: Environment, restarts, memory and copies

![Environment, restarts, memory and copies (NimVor panel)](/assets/blog/schedule-cron-jobs-and-run-workers/06.jpg)

Environment variables are stored in a file only the worker can read. Restart says what happens when the program stops: always, only on failure, or never. You can cap its memory, and run several identical copies, for example four queue consumers. Managed Node.js and Python versions can be chosen per worker.

## Step 7: systemd keeps it alive

Behind the scenes each worker is a systemd service that runs as the website's user, with its own private temporary folder and no extra privileges. It starts at boot and restarts by itself if it crashes. Restart, stop and delete are on its row, and the log is one click away. When the website is disabled, its workers stop too.

## Step 8: Limits

![Limits (NimVor panel)](/assets/blog/schedule-cron-jobs-and-run-workers/08.jpg)

How many cron jobs and workers a customer can have comes from the hosting plan. The processor and memory limits of the account apply to them as well, so a runaway job cannot starve the rest of the server.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
