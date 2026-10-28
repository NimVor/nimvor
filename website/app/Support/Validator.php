<?php

declare(strict_types=1);

namespace NimVorSite\Support;

final class Validator
{
    /** @var array<string, string> */
    private array $errors = [];
    /** @var array<string, string> */
    private array $values = [];

    /** @param array<string, mixed> $input */
    public function __construct(private readonly array $input)
    {
    }

    public function value(string $field): string
    {
        return $this->values[$field] ??= $this->clean($field);
    }

    private function clean(string $field): string
    {
        $v = $this->input[$field] ?? '';
        $v = is_string($v) ? trim(str_replace("\0", '', $v)) : '';
        return $v;
    }

    public function text(string $field, int $min, int $max, string $label): self
    {
        $len = mb_strlen($this->value($field));
        if ($len < $min) {
            $this->errors[$field] ??= $min === 1 ? "Enter your {$label}." : "{$label} needs at least {$min} characters.";
        } elseif ($len > $max) {
            $this->errors[$field] ??= "{$label} can have at most {$max} characters.";
        }
        return $this;
    }

    public function email(string $field = 'email'): self
    {
        $v = $this->value($field);
        if ($v === '' || mb_strlen($v) > 190 || filter_var($v, FILTER_VALIDATE_EMAIL) === false) {
            $this->errors[$field] ??= 'Enter a valid email address.';
        }
        return $this;
    }

    /** @param list<string> $allowed */
    public function in(string $field, array $allowed, string $message): self
    {
        if (!in_array($this->value($field), $allowed, true)) {
            $this->errors[$field] ??= $message;
        }
        return $this;
    }

    public function password(string $field = 'password', ?string $email = null, ?string $name = null): self
    {
        $p = $this->clean($field);
        $weak = ['password1234', '123456789012', 'qwertyuiop12', 'letmein12345', 'administrator', 'welcome12345'];
        if (mb_strlen($p) < 12) {
            $this->errors[$field] ??= 'Use at least 12 characters.';
        } elseif (mb_strlen($p) > 200) {
            $this->errors[$field] ??= 'Use at most 200 characters.';
        } elseif (in_array(strtolower($p), $weak, true) || ($email !== null && strcasecmp($p, $email) === 0) || ($name !== null && $name !== '' && strcasecmp($p, $name) === 0)) {
            $this->errors[$field] ??= 'That password is too easy to guess. Choose another.';
        }
        return $this;
    }

    public function fail(string $field, string $message): self
    {
        $this->errors[$field] ??= $message;
        return $this;
    }

    public function fails(): bool
    {
        return $this->errors !== [];
    }

    /** @return array<string, string> */
    public function errors(): array
    {
        return $this->errors;
    }
}
