<?php

declare(strict_types=1);

// API tokens are for administrators and resellers, and each token can be limited to chosen permissions (like the
// privileges of a WHM API token). `scopes` is a JSON list of permission keys; NULL keeps the old meaning: everything
// the token's user may do. Nothing is deleted: a token that a hosting customer made earlier stays in the table but the
// API refuses it (api_not_available), whatever permissions the customer's role still lists.
return [
    'ALTER TABLE api_tokens ADD COLUMN scopes TEXT NULL',
];
