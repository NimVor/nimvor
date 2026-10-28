<?php

declare(strict_types=1);

namespace NimVor\Security;

use NimVor\Support\UserFacingException;

/** A security-key ceremony that failed, with a message for the user. */
final class WebAuthnException extends UserFacingException
{
}
