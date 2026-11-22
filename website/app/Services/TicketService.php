<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Repositories\Tickets;

/** What happens to a ticket: opening, answers from either side, status changes, and the emails that follow. */
final class TicketService
{
    /** A closed ticket can still be reopened by its owner for this many days. */
    public const REOPEN_DAYS = 30;
    /** Resolved tickets close by themselves after this many days without a reply. */
    public const AUTO_CLOSE_DAYS = 7;

    /** @param list<string> $notify staff addresses that hear about new tickets and customer replies */
    public function __construct(
        private readonly Tickets $tickets,
        private Mailer $mailer,
        private readonly string $baseUrl,
        private readonly array $notify = [],
    ) {
    }

    public function setMailer(Mailer $mailer): void
    {
        $this->mailer = $mailer;
    }

    /** The private address of a ticket for someone who has no account (or does not want to sign in). */
    public function guestUrl(array $ticket): string
    {
        return rtrim($this->baseUrl, '/') . '/support/tickets/' . rawurlencode((string) $ticket['ref']) . '/' . rawurlencode((string) $ticket['access_token']);
    }

    /** @return array{id: int, ref: string, token: string} */
    public function open(?int $userId, string $name, string $email, string $subject, string $body, string $category = 'general'): array
    {
        $category = in_array($category, Tickets::CATEGORIES, true) ? $category : 'general';
        // Security reports start at the top of the queue; nothing else is decided by the sender.
        $priority = $category === 'security' ? 'high' : 'normal';
        $t = $this->tickets->create($userId, $name, $email, $subject, $category, $priority);
        $this->tickets->addReply($t['id'], $userId, 'customer', $body);
        $ticket = (array) $this->tickets->find($t['id']);
        $this->mailer->send(
            $email,
            '[' . $t['ref'] . '] We received your message',
            "Hello {$name},\n\nWe received your message \"{$subject}\" and will reply here. Your ticket number is {$t['ref']}.\n\nFollow it, add to it or close it at:\n" . $this->guestUrl($ticket) . "\n\nNimVor\n"
        );
        foreach ($this->notify as $to) {
            $this->mailer->send($to, '[' . $t['ref'] . '] New ticket: ' . $subject, "{$name} <{$email}> opened a ticket ({$category}).\n\n" . $this->excerpt($body) . "\n\n" . rtrim($this->baseUrl, '/') . '/admin/tickets/' . $t['id'] . "\n");
        }
        return $t;
    }

    /** An answer from the person who opened the ticket. Returns false when the ticket cannot take one any more. */
    public function customerReply(array $ticket, ?int $userId, string $body): bool
    {
        if (!$this->mayReply($ticket)) {
            return false;
        }
        $id = (int) $ticket['id'];
        $reopened = $ticket['status'] !== 'open';
        $this->tickets->addReply($id, $userId, 'customer', $body);
        if ($reopened) {
            $this->tickets->setStatus($id, 'open');
            $this->tickets->addReply($id, null, 'system', 'The customer replied: status changed from ' . $ticket['status'] . ' to open.', true);
        }
        foreach ($this->notify as $to) {
            $this->mailer->send($to, '[' . $ticket['ref'] . '] Reply: ' . $ticket['subject'], "{$ticket['name']} replied.\n\n" . $this->excerpt($body) . "\n\n" . rtrim($this->baseUrl, '/') . '/admin/tickets/' . $id . "\n");
        }
        return true;
    }

    /** Whether the customer may still write to the ticket: open ones always, closed ones for a while. */
    public function mayReply(array $ticket): bool
    {
        if ($ticket['status'] !== 'closed') {
            return true;
        }
        return $ticket['closed_at'] !== null && strtotime((string) $ticket['closed_at'] . ' UTC') > time() - self::REOPEN_DAYS * 86400;
    }

    /**
     * An answer from the staff, visible to the customer, who gets it by email.
     *
     * @param array<string, mixed> $staff
     */
    public function staffReply(array $ticket, array $staff, string $body, string $status = 'answered'): void
    {
        $id = (int) $ticket['id'];
        $status = in_array($status, Tickets::STATUSES, true) ? $status : 'answered';
        $this->tickets->addReply($id, (int) $staff['id'], 'staff', $body);
        $this->tickets->markFirstResponse($id);
        if ($ticket['assigned_to'] === null) {
            $this->tickets->setAssignee($id, (int) $staff['id']);
            $this->tickets->addReply($id, null, 'system', $staff['name'] . ' took the ticket by answering it.', true);
        }
        $this->setStatus($ticket, $staff, $status);
        $fresh = (array) $this->tickets->find($id);
        $this->mailer->send(
            (string) $ticket['email'],
            '[' . $ticket['ref'] . '] ' . $ticket['subject'],
            "{$staff['name']} replied to your ticket {$ticket['ref']}:\n\n{$body}\n\n---\nReply or follow the ticket at:\n" . $this->guestUrl($fresh) . "\n"
        );
    }

    /** @param array<string, mixed> $staff */
    public function note(array $ticket, array $staff, string $body): void
    {
        $this->tickets->addReply((int) $ticket['id'], (int) $staff['id'], 'staff', $body, true);
    }

    /** @param array<string, mixed> $staff */
    public function setStatus(array $ticket, array $staff, string $status): void
    {
        if (!in_array($status, Tickets::STATUSES, true) || $ticket['status'] === $status) {
            return;
        }
        $this->tickets->setStatus((int) $ticket['id'], $status);
        $this->tickets->addReply((int) $ticket['id'], (int) $staff['id'], 'system', "{$staff['name']} changed the status from {$ticket['status']} to {$status}.", true);
    }

    /**
     * Staff changes to priority, category, owner and status in one go, each recorded in the thread.
     *
     * @param array<string, mixed> $staff
     * @param array{status?: string, priority?: string, category?: string, assignee?: string} $in
     */
    public function update(array $ticket, array $staff, array $in): void
    {
        $id = (int) $ticket['id'];
        $who = (string) $staff['name'];
        $priority = (string) ($in['priority'] ?? '');
        if ($priority !== '' && $priority !== $ticket['priority'] && in_array($priority, Tickets::PRIORITIES, true)) {
            $this->tickets->setPriority($id, $priority);
            $this->tickets->addReply($id, (int) $staff['id'], 'system', "{$who} changed the priority from {$ticket['priority']} to {$priority}.", true);
        }
        $category = (string) ($in['category'] ?? '');
        if ($category !== '' && $category !== $ticket['category'] && in_array($category, Tickets::CATEGORIES, true)) {
            $this->tickets->setCategory($id, $category);
            $this->tickets->addReply($id, (int) $staff['id'], 'system', "{$who} moved the ticket from {$ticket['category']} to {$category}.", true);
        }
        if (array_key_exists('assignee', $in)) {
            $a = (string) $in['assignee'];
            $new = $a === '' || $a === 'none' ? null : (ctype_digit($a) ? (int) $a : -1);
            $valid = $new === null || in_array($new, array_map(static fn (array $s): int => (int) $s['id'], $this->tickets->staff()), true);
            $old = $ticket['assigned_to'] === null ? null : (int) $ticket['assigned_to'];
            if ($valid && $new !== $old) {
                $this->tickets->setAssignee($id, $new);
                $this->tickets->addReply($id, (int) $staff['id'], 'system', $new === null ? "{$who} removed the owner." : "{$who} assigned the ticket.", true);
            }
        }
        if (isset($in['status'])) {
            $this->setStatus($ticket, $staff, (string) $in['status']);
        }
    }

    /** The customer closes their own ticket. */
    public function customerClose(array $ticket): void
    {
        if ($ticket['status'] === 'closed') {
            return;
        }
        $this->tickets->setStatus((int) $ticket['id'], 'closed');
        $this->tickets->addReply((int) $ticket['id'], null, 'system', 'The customer closed the ticket.', true);
    }

    private function excerpt(string $body): string
    {
        return mb_strlen($body) > 600 ? mb_substr($body, 0, 600) . '…' : $body;
    }
}
