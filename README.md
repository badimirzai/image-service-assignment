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

# Health check
curl -s http://localhost:8080/healthz

# Upload an image (raw body)
curl -s -D - -X POST http://localhost:8080/v1/images --data-binary @path/to/image.png

# List metadata
curl -s http://localhost:8080/v1/images

# Get metadata by id
curl -s http://localhost:8080/v1/images/1

# Get raw image bytes
curl -s -o out.png http://localhost:8080/v1/images/1/data

# Cutout (bbox = x,y,w,h; top-left origin)
curl -s -o crop.png "http://localhost:8080/v1/images/1/data?bbox=10,20,100,80"

# Replace an existing image (PUT; 404 if id missing)
curl -s -D - -X PUT http://localhost:8080/v1/images/1 --data-binary @path/to/other.jpg

# Batch upload (multipart field name: images)
curl -s -X POST http://localhost:8080/v1/images/batch \
  -F "images=@path/to/a.png" \
  -F "images=@path/to/b.jpg"
```

**Example**
```bash
# POST create
curl -s -D - -X POST http://localhost:8080/v1/images --data-binary @test-images/mario.png
```

```bash
HTTP/1.1 201 Created
Content-Type: application/json
Location: /v1/images/1
Date: Thu, 10 Sep 2026 13:44:28 GMT
Content-Length: 110

```

Verify list:
```bash
curl -s http://localhost:8080/v1/images
```
```json
[
  {
    "id": 1,
    "filesize": 434232,
    "width": 1200,
    "height": 1098,
    "image_type": "png",
    "upload_date": "2026-09-10T13:44:28Z"
  }
]
```

**Tests:**

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

## API

| Method | Path | Status | Behavior |
|--------|------|--------|----------|
| `GET` | `/healthz` | 200 | Liveness |
| `GET` | `/v1/images` | 200 | List metadata (newest id first); empty → `[]` |
| `GET` | `/v1/images/{id}` | 200 / 404 | Metadata for one image |
| `GET` | `/v1/images/{id}/data` | 200 / 404 | Raw image bytes (`Content-Type` image/*) |
| `GET` | `/v1/images/{id}/data?bbox=x,y,w,h` | 200 / 400 / 404 | Read-time cutout; storage unchanged |
| `POST` | `/v1/images` | 201 | Create from raw body → metadata + `Location` |
| `PUT` | `/v1/images/{id}` | 200 | Replace existing image only (no upsert) |
| `POST` | `/v1/images/batch` | 200 / 400 / 413 / 415 | Multipart batch; partial success JSON |

**PUT details:** raw image body (same as POST). Re-validates JPEG/PNG/GIF from bytes, replaces stored bytes and derived fields (`filesize`, `width`, `height`, `image_type`). Preserves `id` and original `upload_date`. Missing id → **404**; invalid/unsupported image → **400**; too large → **413**.

**bbox details:** optional query on `/data`. Coordinates are top-left origin, `x,y,w,h` with `w,h > 0`; rectangle must lie fully inside the image (strict, no clipping) → **400** if invalid. Response is re-encoded in the same format (JPEG/PNG/GIF). Animated GIF cutouts use the first frame only (stdlib limitation; documented tradeoff). Originals in the store are never modified.

**Batch details:** `Content-Type: multipart/form-data` with one or more file parts named **`images`** (max **12**). Per-part and request size capped at ~10 MiB per image. Handler parses multipart; service uses a **4-worker** pool and the same validation as single create. Response is always **200** when the batch is accepted:

```json
{ "success": [ /* metadata... */ ], "errors": [ { "filename": "...", "error": "..." } ] }
```

Empty batch / too many parts → **400**; whole request body too large → **413**; a single oversized part is a per-item error in the **200** response (other images still processed). Not multipart → **415**. Client disconnect cancels unscheduled work; already-stored images may remain.

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
- **Do not trust client `Content-Type` / filename** - derive format, dimensions, and filesize from decoded bytes (stdlib `image`).
- **HTTP edge** may enforce the same max size via `http.MaxBytesReader` (DoS protection); service still owns the limit policy.
- **Store holds opaque bytes** + service-derived metadata. **Original upload bytes are stored unchanged**; re-encode only for derived responses (bbox).

### API semantics

- **POST creates; PUT replaces existing only** → missing ID returns **404** (not upsert).
- **Formats:** JPEG, PNG, GIF.
- **Limits (initial):** ~10 MiB per image; batch size capped at 12; 4 workers for batch.
- **`bbox`:** read-time only - decode stored original, validate coords (top-left, strict in-bounds → 400 if invalid), crop, encode response. Does **not** mutate storage.
- **Batch:** multipart field `images`; partial success (`success` + `errors`); fixed worker pool; cancel via `r.Context()` on disconnect (stop scheduling / skip further writes). Per-item failures do **not** cancel the batch. Already-stored items before disconnect may remain (no compensating deletes).

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

**Implemented (in-memory):**
- `GET /healthz`
- `GET /v1/images`, `GET /v1/images/{id}`, `GET /v1/images/{id}/data` (optional `?bbox=`)
- `POST /v1/images`, `PUT /v1/images/{id}`, `POST /v1/images/batch`

**Not yet:** SQLite persistence

---

## Submission notes

- Prefer a clean commit history; assignment asks for a git bundle on delivery.
- If AI tooling is used for the submission, include `ai-transcript.txt` per the brief.
