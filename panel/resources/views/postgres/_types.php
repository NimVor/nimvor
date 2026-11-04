<?php
/** The column types the structure forms accept (the same built-in types the agent allows), as input suggestions. */
?>
<datalist id="pg-types">
    <?php foreach (['integer', 'bigint', 'smallint', 'serial', 'bigserial', 'text', 'varchar(255)', 'char(1)', 'boolean', 'numeric(12,2)', 'real', 'double precision', 'date', 'time', 'timestamp', 'timestamptz', 'interval', 'json', 'jsonb', 'uuid', 'bytea', 'inet', 'text[]', 'integer[]'] as $ty): ?><option value="<?= e($ty) ?>"></option><?php endforeach; ?>
</datalist>
