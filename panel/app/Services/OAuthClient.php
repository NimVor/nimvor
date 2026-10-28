<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Support\UserFacingException;

/**
 * Minimal OAuth 2.0 client for connecting Google Drive: builds the consent
 * URL and exchanges the authorisation code for tokens. Replaced by a fake
 * in tests.
 */
class OAuthClient
{
    public const GOOGLE_AUTH = 'https://accounts.google.com/o/oauth2/v2/auth';
    public const GOOGLE_TOKEN = 'https://oauth2.googleapis.com/token';

    /** @param array<string, string> $params */
    public function authUrl(array $params): string
    {
        return self::GOOGLE_AUTH . '?' . http_build_query($params);
    }

    /**
     * @param array<string, string> $form
     * @return array<string, mixed>
     */
    public function token(array $form): array
    {
        $ch = curl_init(self::GOOGLE_TOKEN);
        curl_setopt_array($ch, [
            CURLOPT_POST => true,
            CURLOPT_POSTFIELDS => http_build_query($form),
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT => 20,
            CURLOPT_PROTOCOLS => CURLPROTO_HTTPS,
            CURLOPT_HTTPHEADER => ['Accept: application/json'],
        ]);
        $body = curl_exec($ch);
        $err = curl_error($ch);
        curl_close($ch);
        if (!is_string($body)) {
            throw new UserFacingException('Could not reach Google.', $err, 'Check that the panel server can reach oauth2.googleapis.com and try again.');
        }
        return (array) json_decode($body, true);
    }
}
