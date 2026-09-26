#!/usr/bin/env python3
"""Backfill missing track descriptions using the existing admin AI-enrich endpoint.

Why: 73/110 tracks have no description -> thin pages Google deprioritizes
("Crawled - currently not indexed"). This gives every track 2-3 sentences
of unique text via the same Gemini pipeline the import UI uses.

Usage:
  1. Log in at https://play.zedbeatz.com/admin as admin.
  2. DevTools > Application > Local Storage > copy `access_token`.
  3. ADMIN_TOKEN='eyJ...' python3 scripts/backfill-descriptions.py [--dry-run] [--limit N]

Env:
  API_BASE     (default https://api.zedbeatz.com/api/v1)
  ADMIN_TOKEN  (required) admin JWT access token
  BATCH        enrich batch size (default 10)
  DELAY        seconds between batches (default 3)

Writes progress to /tmp/desc-backfill-done.txt so reruns skip finished tracks.
Requires the backend change allowing `description` in PATCH /admin/tracks/{id}.
"""
import json
import os
import sys
import time
import urllib.request

API_BASE = os.environ.get("API_BASE", "https://api.zedbeatz.com/api/v1").rstrip("/")
TOKEN = os.environ.get("ADMIN_TOKEN", "")
BATCH = int(os.environ.get("BATCH", "10"))
DELAY = float(os.environ.get("DELAY", "3"))
DRY_RUN = "--dry-run" in sys.argv
LIMIT = int((sys.argv[sys.argv.index("--limit") + 1] if "--limit" in sys.argv else 0) or 0)
DONE_FILE = "/tmp/desc-backfill-done.txt"

if not TOKEN:
    sys.exit("Missing ADMIN_TOKEN. See header comments.")


def api(method, path, body=None):
    req = urllib.request.Request(
        API_BASE + path,
        data=json.dumps(body).encode() if body is not None else None,
        method=method,
        headers={"Authorization": f"Bearer {TOKEN}", "Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r)


def done_ids():
    if os.path.exists(DONE_FILE):
        with open(DONE_FILE) as f:
            return set(x.strip() for x in f if x.strip())
    return set()


def mark_done(tid):
    with open(DONE_FILE, "a") as f:
        f.write(tid + "\n")


def main():
    tracks = api("GET", "/admin/tracks?limit=200").get("tracks", [])
    print(f"admin track count: {len(tracks)}")
    missing = [t for t in tracks if not (t.get("description") or "").strip()]
    print(f"missing description: {len(missing)}")
    already = done_ids()
    todo = [t for t in missing if t["id"] not in already]
    if LIMIT:
        todo = todo[:LIMIT]
    print(f"to process (after resume/limit): {len(todo)}")
    if DRY_RUN:
        for t in todo[:10]:
            print(" -", t["title"], "by", t.get("artist_name"))
        return

    for i in range(0, len(todo), BATCH):
        chunk = todo[i : i + BATCH]
        payload = {
            "tracks": [
                {
                    "title": t["title"],
                    "artist": t.get("artist_name") or "",
                    "album": t.get("album_name") or "",
                }
                for t in chunk
            ]
        }
        try:
            res = api("POST", "/admin/import/ai-enrich", payload)
        except Exception as e:
            print(f"batch {i//BATCH}: enrich failed: {e} — skipping, will retry next run")
            time.sleep(DELAY)
            continue
        by_index = {r["index"]: r for r in res.get("results", [])}
        for j, t in enumerate(chunk):
            r = by_index.get(j, {})
            desc = (r.get("description") or "").strip()
            if not desc:
                print(f"[{i+j+1}/{len(todo)}] NO-DESC {t['title']}")
                continue
            try:
                api("PATCH", f"/admin/tracks/{t['id']}", {"description": desc})
                mark_done(t["id"])
                print(f"[{i+j+1}/{len(todo)}] OK {t['title']} ({len(desc)} chars)")
            except Exception as e:
                print(f"[{i+j+1}/{len(todo)}] SAVE-FAIL {t['title']}: {e}")
        time.sleep(DELAY)
    print("done. progress in", DONE_FILE)


if __name__ == "__main__":
    main()
