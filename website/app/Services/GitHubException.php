<?php

declare(strict_types=1);

namespace NimVorSite\Services;

/** GitHub could not be reached or refused: the message is fit to show an administrator. */
final class GitHubException extends \RuntimeException
{
}
