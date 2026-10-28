<?php

declare(strict_types=1);

// Node.js and Python websites. These are reverse-proxy sites bound to a
// NimVor-managed runtime (e.g. node:20.17.0); the app itself runs as a Worker.
// app_runtime is empty for plain PHP/static/proxy sites.

return [
    "ALTER TABLE websites ADD COLUMN app_runtime TEXT NOT NULL DEFAULT ''",
];
