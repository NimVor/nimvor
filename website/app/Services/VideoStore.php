<?php

declare(strict_types=1);

namespace NimVorSite\Services;

/**
 * Keeps uploaded video files and poster images outside the web root. A file is checked by its
 * content, not by the name or type the browser sent, and is stored under a random name.
 */
final class VideoStore
{
    public const VIDEO_TYPES = ['mp4' => 'video/mp4', 'webm' => 'video/webm'];
    public const POSTER_TYPES = ['jpg' => 'image/jpeg', 'png' => 'image/png', 'webp' => 'image/webp'];
    public const POSTER_MAX = 2 * 1024 * 1024;

    /** @param callable(string, string): bool|null $move moves an uploaded file (move_uploaded_file unless a test replaces it) */
    public function __construct(private readonly string $dir, private readonly int $maxVideoBytes, private $move = null)
    {
    }

    public function maxVideoBytes(): int
    {
        return $this->maxVideoBytes;
    }

    public function path(string $name): ?string
    {
        if (preg_match('/^[a-f0-9]{32}\.(mp4|webm|jpg|png|webp)$/', $name) !== 1) {
            return null;
        }
        $p = $this->dir . '/' . $name;
        return is_file($p) ? $p : null;
    }

    /**
     * @param array<string, mixed> $file one entry of $_FILES
     * @return array{name: string, mime: string, size: int}
     * @throws \RuntimeException with a message for the administrator
     */
    public function saveVideo(array $file): array
    {
        $tmp = $this->uploaded($file);
        $size = (int) filesize($tmp);
        if ($size < 1024) {
            throw new \RuntimeException('That file is empty or too small to be a video.');
        }
        if ($size > $this->maxVideoBytes) {
            throw new \RuntimeException('The video is larger than ' . intdiv($this->maxVideoBytes, 1048576) . ' MB. Make it smaller, or put it on YouTube and paste the link.');
        }
        $head = (string) file_get_contents($tmp, false, null, 0, 16);
        if (substr($head, 4, 4) === 'ftyp') {
            $ext = 'mp4';
        } elseif (str_starts_with($head, "\x1A\x45\xDF\xA3")) {
            $ext = 'webm';
        } else {
            throw new \RuntimeException('Upload an MP4 (H.264) or WebM video. That file is neither.');
        }
        return ['name' => $this->store($tmp, $ext), 'mime' => self::VIDEO_TYPES[$ext], 'size' => $size];
    }

    /**
     * @param array<string, mixed> $file
     * @throws \RuntimeException
     */
    public function savePoster(array $file): string
    {
        $tmp = $this->uploaded($file);
        if ((int) filesize($tmp) > self::POSTER_MAX) {
            throw new \RuntimeException('The cover image is larger than 2 MB.');
        }
        $head = (string) file_get_contents($tmp, false, null, 0, 12);
        if (str_starts_with($head, "\xFF\xD8\xFF")) {
            $ext = 'jpg';
        } elseif (str_starts_with($head, "\x89PNG\r\n\x1A\n")) {
            $ext = 'png';
        } elseif (str_starts_with($head, 'RIFF') && substr($head, 8, 4) === 'WEBP') {
            $ext = 'webp';
        } else {
            throw new \RuntimeException('The cover image must be a JPEG, PNG or WebP picture.');
        }
        return $this->store($tmp, $ext);
    }

    public function delete(?string $name): void
    {
        if ($name !== null && ($p = $this->path($name)) !== null) {
            @unlink($p);
        }
    }

    /** @param array<string, mixed> $file */
    private function uploaded(array $file): string
    {
        $err = (int) ($file['error'] ?? UPLOAD_ERR_NO_FILE);
        if ($err === UPLOAD_ERR_INI_SIZE || $err === UPLOAD_ERR_FORM_SIZE) {
            throw new \RuntimeException("The server's upload limit is smaller than this file. Raise upload_max_filesize and post_max_size in PHP, or put the video on YouTube and paste the link.");
        }
        if ($err !== UPLOAD_ERR_OK || !is_string($file['tmp_name'] ?? null) || $file['tmp_name'] === '') {
            throw new \RuntimeException('The file did not upload. Try again.');
        }
        return $file['tmp_name'];
    }

    private function store(string $tmp, string $ext): string
    {
        if (!is_dir($this->dir) && !@mkdir($this->dir, 0750, true) && !is_dir($this->dir)) {
            throw new \RuntimeException('The folder for uploaded videos could not be created.');
        }
        $name = bin2hex(random_bytes(16)) . '.' . $ext;
        $move = $this->move ?? 'move_uploaded_file';
        if (!$move($tmp, $this->dir . '/' . $name)) {
            throw new \RuntimeException('The file could not be saved. Check that the storage folder is writable.');
        }
        @chmod($this->dir . '/' . $name, 0640);
        return $name;
    }
}
