#!/usr/bin/env bash
set -euo pipefail

DUMP_FILE="${1:-zedstream_dump.sql}"

cd /opt/zedstream
echo "Dumping production DB to $DUMP_FILE ..."
docker compose exec -T postgres pg_dump \
  --no-owner \
  --no-acl \
  --clean \
  --if-exists \
  -U zedstream zedstream > "$DUMP_FILE"

gzip -f "$DUMP_FILE"
echo "Created: ${DUMP_FILE}.gz ($(du -h "${DUMP_FILE}.gz" | cut -f1))"
