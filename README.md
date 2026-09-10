# Image Service

Small production-quality Go HTTP service for storing and retrieving images. Designed so an evaluator can clone, test, and run with no external infrastructure:

```bash
git clone https://github.com/badimirzai/image-service-assignment.git
cd image-service-assignment
go test ./...
go run ./cmd/server
```

Target: code you would be comfortable merging to main, with motivated tradeoffs where scope is limited.

---

## Setup

**Requirements:** Go 1.22+ (module declares the project version).

```bash
# Run the server (default :8080; override with PORT)
go run ./cmd/server

# Health check (currently the only live route)
curl -s http://localhost:8080/healthz
```

Image API routes are not implemented yet; this section will grow with curl examples as endpoints land.

**Tests** (as they are added):

```bash
go test ./...
```

---

## Project structure

| Path | Role |
|------|------|
| [`cmd/server/main.go`](cmd/server/main.go) | Wires store → service → HTTP server and listens |
| [`internal/httpapi/`](internal/httpapi/) | HTTP handlers: routing, status codes, headers; no image-domain logic |
| [`internal/service/`](internal/service/) | Application logic: validation, metadata, crop, batch (as features are added) |
| [`internal/store/`](internal/store/) | `Store` interface + in-memory implementation |
| [`assignment-instructions.md`](assignment-instructions.md) | Original assignment brief |

Architecture (**Option B**): thin handlers → service → store interface → in-memory store (SQLite later, optional).

```
HTTP request
    → httpapi (parse, limits at edge, status/headers)
        → service (validate, derive metadata, crop, batch)
            → store.Store (opaque bytes + metadata)
                → MemoryStore (now) / SQLite (later)
```

---

## API (planned)

| Method | Path | Behavior |
|--------|------|----------|
| `GET` | `/v1/images` | List metadata (all; newest first when implemented) |
| `GET` | `/v1/images/{id}` | Metadata for one image |
| `GET` | `/v1/images/{id}/data` | Raw bytes; optional `?bbox=x,y,w,h` cutout |
| `POST` | `/v1/images` | Create from raw image body → `201` + `Location` + metadata |
| `PUT` | `/v1/images/{id}` | Replace existing image only (no upsert) |
| `POST` | `/v1/images/batch` | `multipart/form-data`; bounded concurrency; partial success |

**Metadata (JSON):** `id`, `filesize`, `width`, `height`, `image_type`, `upload_date` (UTC RFC3339), optional `filename`. Persisted as `ImageRecord{Metadata, Bytes}` (one logical row later in SQLite).

**Out of scope:** auth, delete, pagination, caching/ETags, config frameworks.

---

## Design choices

Locked decisions from the assignment review and clarifying discussions.

### Architecture

- **Option B - thin handlers → service → store interface.** Enough seams for tests and a later SQLite swap; no DTO soup, middleware stack, or plugin system.
- **Stdlib only** for HTTP and image decode (no Gin/Echo). Keeps clone/run trivial for reviewers.
- **In-memory first.** Zero infra. Store interface is shaped so SQLite (BLOB + columns) can replace memory without rewriting handlers/service.
- **Scaling note (motivated tradeoff):** process-local memory won’t horizontally scale; production path would be object storage + metadata DB (+ CDN). Acceptable for this take-home if documented.

### Identifiers

- **Integer IDs, not UUIDs.** Single-process service; no distributed ID requirement. Integers are easier to inspect in curl and natural for a counter / SQLite `INTEGER PRIMARY KEY`.
- **Server-assigned**, unique within the running store. Clients do not supply create IDs. Allocation under the store lock (or later a DB transaction).

### Trust, validation, storage

- **Validation and metadata extraction live in the service**, shared by POST, PUT, and batch. Storage does not understand formats.
- **Do not trust client `Content-Type` / filename** — derive format, dimensions, and filesize from decoded bytes (stdlib `image`).
- **HTTP edge** may enforce the same max size via `http.MaxBytesReader` (DoS protection); service still owns the limit policy.
- **Store holds opaque bytes** + service-derived metadata. **Original upload bytes are stored unchanged**; re-encode only for derived responses (bbox).

### API semantics

- **POST creates; PUT replaces existing only** → missing ID returns **404** (not upsert).
- **Formats:** JPEG, PNG, GIF.
- **Limits (initial):** ~10 MiB per image; batch size capped (e.g. 20); fixed worker pool for batch.
- **`bbox`:** read-time only — decode stored original, validate coords (top-left, strict in-bounds → 400 if invalid), crop, encode response. Does **not** mutate storage.
- **Batch:** partial success (`success` + `errors`); fixed-size worker pool; cancel via `r.Context()` on disconnect (stop scheduling / skip further writes). Per-item failures do **not** cancel the batch. Already-stored items before disconnect may remain (no compensating deletes).

### HTTP status cheat sheet

| Code | When |
|------|------|
| `200` | List/get/PUT OK; batch completed (including partial success) |
| `201` | Single create |
| `400` | Validation (empty/undecodable image, bad bbox, etc.) |
| `404` | Unknown ID (data not found) |
| `413` | Body too large |
| `415` | Wrong media type for endpoint (e.g. batch not multipart) |
| `500` | Unexpected internal failure only |

Errors: `{"error":"..."}`. Image responses set `Content-Type` / `Content-Length`; creates set `Location: /v1/images/{id}`.

---

## Clarifications we locked

| Topic | Decision | Rationale |
|-------|----------|-----------|
| ID type | Integer, server-assigned | Simpler than UUID for this single-service scope |
| Map safety | `sync.RWMutex` from day one | Correct concurrent practice; SQLite would use transactions later |
| Where to validate | Service layer | One rule set for POST/PUT/batch; store stays format-agnostic |
| Size limit at edge | `MaxBytesReader` + service limit | Transport protection, same policy |
| `bbox` | Read-time transform | Matches GET semantics; keeps storage immutable/simple |
| Batch concurrency | Fixed worker pool + request ctx | Bounds CPU; cancellation without killing partial-success semantics |

---

## Current status

- Runnable skeleton: `go run ./cmd/server` → `GET /healthz`
- Image endpoints not implemented yet
- Persistence: in-memory only

---

## Submission notes

- Prefer a clean commit history; assignment asks for a git bundle on delivery.
- If AI tooling is used for the submission, include `ai-transcript.txt` per the brief.
