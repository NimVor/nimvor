<?php

declare(strict_types=1);

namespace WhaCP\Mail;

/**
 * Sends plain-text email (alerts, password resets).
 */
interface Mailer
{
    /** Whether outgoing mail has been set up. */
    public function configured(): bool;

    /**
     * @param list<string> $to
     * @throws MailException
     */
    public function send(array $to, string $subject, string $text): void;
}
