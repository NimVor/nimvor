<?php

declare(strict_types=1);

namespace WhaCP\Security;

use WhaCP\Support\UserFacingException;

/** A security-key ceremony that failed, with a message for the user. */
final class WebAuthnException extends UserFacingException
{
}
