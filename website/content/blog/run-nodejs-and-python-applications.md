---
title: Run Node.js and Python applications
slug: run-nodejs-and-python-applications
excerpt: Install a runtime, deploy an application as a worker and keep it running.
date: 2026-11-08
---
Besides PHP sites, NimVor runs Node.js and Python applications as supervised workers. This guide installs a runtime and shows where the application, its environment and its logs live.

## Step 1: Runtimes: Node.js and Python versions

![Runtimes: Node.js and Python versions (NimVor panel)](/assets/blog/run-nodejs-and-python-applications/01.jpg)

The Runtimes page installs Node.js and Python for your websites. Several versions can live side by side, so one site can run the long-term-support release it was built for while another uses a newer one. They are installed separately from the system, so they never replace or break the Node.js or Python the operating system itself needs.

## Step 2: Install Node.js

![Install Node.js (NimVor panel)](/assets/blog/run-nodejs-and-python-applications/02.jpg)

Install Node.js shows the current stable versions, with their long-term-support names. Tick one or more. Each is downloaded from nodejs.org, verified against its published checksum, and installed next to the others. Let's install the 22 and 24 releases.

## Step 3: The install runs as a background task

![The install runs as a background task (NimVor panel)](/assets/blog/run-nodejs-and-python-applications/03.jpg)

The install runs as a background task with a live log, so you can close the window and come back. The log shows the download, the checksum check and the unpacking. When it finishes the versions appear in the list of installed runtimes.

## Step 4: Install Python

![Install Python (NimVor panel)](/assets/blog/run-nodejs-and-python-applications/04.jpg)

Python works the same way. Install Python lists the available versions, you tick the ones you want, and each is installed on its own with pip and virtual environment support, ready for web applications. Both lists come from the official download sites, so the server needs internet access to open them.

## Step 5: Choosing a runtime for a website

![Choosing a runtime for a website (NimVor panel)](/assets/blog/run-nodejs-and-python-applications/05.jpg)

To use a runtime, create a website of the Node.js or Python type. The application type is chosen when the website is created, together with the folder it lives in. NimVor runs the application as the website's own user, behind the web server, with the domain and the certificate in place.

## Step 6: Workers keep the application running

![Workers keep the application running (NimVor panel)](/assets/blog/run-nodejs-and-python-applications/06.jpg)

A worker is a program kept running in the background: your Node.js server, a Python queue consumer, a scheduler. On a website's Workers tab you give it a name, the command to run, the working folder, any environment variables, what to do when it stops, how much memory it may use and which runtime version it should use.

NimVor then keeps it alive, restarts it if it crashes and starts it at boot.

## Where to go next

The full reference for every page is in the [documentation](https://nimvor.com/docs). A short video of this guide is on the [Videos page](https://nimvor.com/resources/videos) when it has been published, and questions or problems go to [GitHub](https://github.com/NimVor/nimvor/issues).
