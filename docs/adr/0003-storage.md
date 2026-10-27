# 0003: File storage in the state directory

- Status: accepted
- Date: 2026-10-27

## Context

The inbox principle (see [README](../../README.md#principles)), that no event is lost before `ack`, only holds as long as the data outlives the process. On developer machines the process ends all the time:

- The daemon exits when idle ([ADR 0001](0001-runtime-modes.md)).
- Laptops sleep and reboot.
- Binaries get updated, and services restart.

A typical failure: someone picks a variant in the evening, the daemon exits overnight, and the agent asks for the result the next morning. With memory-only storage, the choice is silently gone.

## Decision

vitrine stores everything as plain files in the state directory (see [ADR 0002](0002-authentication.md) for its location per platform):

```
prototypes/<repo>/<name>/index.html
prototypes/<repo>/<name>/meta.json     # version, expiry, keep_for, context, event token, ack cursor
prototypes/<repo>/<name>/events.jsonl  # append-only
```

- **Files are the only backend.** There is no memory-only mode.
- **No database.** The files are readable by hand and behave the same on all platforms.
- **No locking.** The single instance ([ADR 0001](0001-runtime-modes.md)) is the only writer.
- **Lifetime and cleanup.**
  - A prototype lives 3 days after its last activity, or up to 30 days with `keep_for`.
  - Cleanup deletes directories.
  - Limits stay in place: 2 MB per prototype, 64 KB per event, 10,000 events per prototype, and a total size cap.
  - A full inbox makes room with acknowledged events first, then with unread automatic views and clicks. Unread events a prototype reported itself are never dropped: a new event is refused with `inbox_full` instead.
- **Atomic writes.** Writes go to a temporary file followed by a rename. Appends to `events.jsonl` are flushed before the event is acknowledged to the sender.

## Consequences

- Events survive daemon idle-exit, reboots and upgrades.
- The repository remains the source. The state directory is working state, not an archive.
- Read-only or ephemeral filesystems (some containers) are not supported.

## Alternatives considered

- **Memory only.** Rejected. It breaks the inbox promise on developer machines.
- **Memory by default, files optional.** Rejected. No supported environment needs memory-only storage, and a second backend doubles the test surface.
- **SQLite.** Rejected for now. It adds a dependency (CGO or a large pure-Go port) without benefit at this scale.
