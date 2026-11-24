<?php

declare(strict_types=1);

// API tokens are for administrators and resellers, and each token can be limited to chosen permissions (like the
// privileges of a WHM API token). `scopes` is a JSON list of permission keys; NULL keeps the old meaning: everything
// the token's user may do. Tokens that hosting customers created earlier could no longer be used (the API refuses
// them now), and a customer can no longer open the page to revoke them, so they are removed here.
return [
    'ALTER TABLE api_tokens ADD COLUMN scopes TEXT NULL',
    "DELETE FROM api_tokens WHERE user_id IN (SELECT u.id FROM users u JOIN roles r ON r.id = u.role_id WHERE r.name = 'customer')",
    "DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'api.tokens') AND role_id IN (SELECT id FROM roles WHERE name = 'customer')",
];
