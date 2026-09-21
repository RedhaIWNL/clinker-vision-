"""SQLite snapshot: restart recovery without re-observing 3 loops.

Saved every completed loop: identity alignment, all godet rows (upsert by
loop+slot, so re-fed rows are idempotent), computed loops, alert keys, bundle
version. On startup, restore() returns state iff the bundle version matches;
otherwise the caller starts fresh (new calibration = new history).
"""
from __future__ import annotations

import json
import sqlite3
import threading
from pathlib import Path


SCHEMA = """
CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE IF NOT EXISTS loops (
    loop_no INTEGER PRIMARY KEY, start_slot INTEGER, score REAL, margin REAL);
CREATE TABLE IF NOT EXISTS godet_rows (
    loop INTEGER, slot INTEGER, godet_id INTEGER, payload TEXT,
    PRIMARY KEY (loop, slot));
CREATE TABLE IF NOT EXISTS alert_keys (
    kind TEXT, godet_id INTEGER, loop INTEGER,
    PRIMARY KEY (kind, godet_id, loop));
"""


class Store:
    def __init__(self, path):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()
        # gRPC serves Infer/GetGodetState on a thread pool and the shutdown
        # handler persists from the signal thread: the same connection is
        # used cross-thread, so disable the default same-thread check and
        # serialize all access. WAL + busy_timeout keeps concurrent
        # readers/writers from failing under load.
        self.con = sqlite3.connect(str(self.path), check_same_thread=False,
                                   timeout=30.0)
        with self._lock:
            self.con.execute("PRAGMA journal_mode=WAL;")
            self.con.execute("PRAGMA busy_timeout=5000;")
            self.con.executescript(SCHEMA)
            self.con.commit()

    def save(self, version, identity_snap, rows, computed, alert_keys, loop_starts):
        snap = dict(identity_snap)
        tail = snap.pop("tail", [])
        with self._lock:
            cur = self.con.cursor()
            cur.execute("INSERT OR REPLACE INTO kv VALUES ('version', ?)", (version,))
            cur.execute("INSERT OR REPLACE INTO kv VALUES ('identity', ?)",
                        (json.dumps(snap),))
            cur.execute("INSERT OR REPLACE INTO kv VALUES ('loop_starts', ?)",
                        (json.dumps({str(k): v for k, v in loop_starts.items()}),))
            cur.execute("INSERT OR REPLACE INTO kv VALUES ('tail', ?)",
                        (json.dumps(tail),))
            for i, st in enumerate(snap.get("starts", [])):
                sc = identity_snap.get("scores", [])
                mg = identity_snap.get("margins", [])
                cur.execute("INSERT OR REPLACE INTO loops VALUES (?, ?, ?, ?)",
                            (i, st, sc[i] if i < len(sc) else None,
                             mg[i] if i < len(mg) else None))
            for r in rows:
                cur.execute("INSERT OR REPLACE INTO godet_rows VALUES (?, ?, ?, ?)",
                            (int(r["loop"]), int(r["slot"]), int(r["godet_id"]),
                             json.dumps(_jsonable(r))))
            for k in alert_keys:
                cur.execute("INSERT OR IGNORE INTO alert_keys VALUES (?, ?, ?)", tuple(k))
            for c in computed:
                cur.execute("INSERT OR REPLACE INTO kv VALUES (?, ?)",
                            (f"computed_{c}", "1"))
            self.con.commit()

    def load(self, version):
        with self._lock:
            cur = self.con.cursor()
            row = cur.execute("SELECT value FROM kv WHERE key='version'").fetchone()
            if row is None or row[0] != version:
                return None
            ident = json.loads(cur.execute(
                "SELECT value FROM kv WHERE key='identity'").fetchone()[0])
            ls = cur.execute("SELECT value FROM kv WHERE key='loop_starts'").fetchone()
            loop_starts = {int(k): v for k, v in json.loads(ls[0]).items()} if ls else {}
            tl = cur.execute("SELECT value FROM kv WHERE key='tail'").fetchone()
            tail = json.loads(tl[0]) if tl else []
            rows = [_restore(json.loads(r[0])) for r in
                    cur.execute("SELECT payload FROM godet_rows ORDER BY loop, slot")]
            keys = [tuple(k) for k in cur.execute("SELECT kind, godet_id, loop FROM alert_keys")]
            computed = sorted(int(k[0].split("_", 1)[1]) for k in
                              cur.execute("SELECT key FROM kv WHERE key LIKE 'computed_%'"))
            loops = {r["loop"] for r in rows}
            return {"identity": ident, "rows": rows, "alert_keys": keys,
                    "computed": computed, "loops": loops, "loop_starts": loop_starts,
                    "tail": tail}

    def close(self):
        with self._lock:
            self.con.close()


NAN = "__nan__"  # JSON has no NaN: sentinel round-trips NaN exactly.


def _jsonable(row):
    import math

    out = {}
    for k, v in row.items():
        if isinstance(v, float) and math.isnan(v):
            out[k] = NAN
        else:
            out[k] = v
    return out


def _restore(row):
    import math

    out = {}
    for k, v in row.items():
        out[k] = float("nan") if v == NAN else v
    return out
