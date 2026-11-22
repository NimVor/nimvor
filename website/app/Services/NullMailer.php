<?php

declare(strict_types=1);

namespace NimVorSite\Services;

/** No mail is configured (MAIL_FROM is empty): nothing is sent. */
final class NullMailer implements Mailer
{
    public function send(string $to, string $subject, string $body): bool
    {
        return false;
    }
}
