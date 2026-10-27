<?php

declare(strict_types=1);

namespace WhaCP\Tests\Support;

use WhaCP\Mail\Mailer;

final class ArrayMailer implements Mailer
{
    /** @var list<array{to: list<string>, subject: string, text: string}> */
    public array $sent = [];

    public function __construct(public bool $ready = true)
    {
    }

    public function configured(): bool
    {
        return $this->ready;
    }

    public function send(array $to, string $subject, string $text): void
    {
        $this->sent[] = ['to' => $to, 'subject' => $subject, 'text' => $text];
    }
}
