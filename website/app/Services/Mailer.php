<?php

declare(strict_types=1);

namespace NimVorSite\Services;

/** Sends plain-text email. The site works without one: tickets are always reachable from the browser. */
interface Mailer
{
    public function send(string $to, string $subject, string $body): bool;
}
