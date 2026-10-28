<?php

declare(strict_types=1);

// The first blog post, so a new site is not empty. Edit or delete it under Admin > Blog.
return [
    'posts' => [
        [
            'slug' => 'why-nimvor-is-built-this-way',
            'title' => 'Why NimVor is built this way',
            'excerpt' => 'A control panel has root-level consequences. These are the rules NimVor follows because of that.',
            'body' => "A hosting control panel can change almost everything on a server, so a mistake in one is not a small mistake. NimVor is built around one rule: every privileged operation must be explicit, validated, audited and reversible.\n\n## The panel never runs shell commands\n\nThe web panel is PHP and has no way to run a command. It asks a separate agent, written in Go, to perform a named action. The agent has a fixed list of actions and a fixed list of programs it may start, and it starts them with arguments it builds itself, never through a shell.\n\n## Changes are checked before they are applied\n\n- Detect what is on the server first.\n- Back up what will change.\n- Validate the new configuration (for example `nginx -t` or `named-checkzone`).\n- Apply it, verify it, and roll back if verification fails.\n\nA configuration that does not validate is never reloaded, and the last working file is kept.\n\n## Read-only first\n\nA new installation starts in read-only mode: it inventories the server and changes nothing. You turn on write mode when you are ready to let NimVor manage websites.\n\n## Free and open\n\nNimVor is MIT licensed and written from scratch. You can read every line of it on [GitHub](https://github.com/NimVor/nimvor).",
        ],
    ],
];
