#!/usr/bin/env bash
#
# Rebuild the SQL editor bundle (CodeMirror 6 + sql-formatter) from panel/assets-src/sql-editor into
# panel/public/assets/vendor/codemirror/sql-editor.min.js. The built file is committed, so servers never need
# Node.js; run this only to change the editor or update its (pinned) dependencies, then commit the result.
set -Eeuo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT/panel/assets-src/sql-editor"
command -v npm >/dev/null || { echo "npm is needed to rebuild the editor" >&2; exit 1; }
npm ci --no-audit --no-fund
npm run build
echo "Built $(wc -c < "$ROOT/panel/public/assets/vendor/codemirror/sql-editor.min.js") bytes. Commit the file and, if dependencies changed, package-lock.json."
