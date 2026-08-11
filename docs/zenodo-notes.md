# Zenodo sandbox — Phase 0 spike findings

**Date:** 2026-08-10 · **Target:** `https://sandbox.zenodo.org` (InvenioRDM
API, `/api/records`) · **Auth:** PAT via `Authorization: Bearer`, repo secret
`ZENODO_SANDBOX_TOKEN` · **Fixtures:** real request/response captures in
[`internal/testutil/fakeinvenio/fixtures/`](../internal/testutil/fakeinvenio/fixtures/)
(tokens and cookies redacted), named `NN-probe.{request,headers,body}`.

This answers the open questions from `docs/datapin-handoff.md` §3.5 /
`docs/reboot-plan.md` §8 Phase 0. Each finding cites its fixture. Sandbox can
be wiped at any time and can lag/lead production Zenodo; anything marked
**⚠ differs from plan §2.4** should be re-verified against production before
we rely on it in either direction.

## The nine checklist answers

### 1. Publish-twice status code → **404, not 4xx-on-record**

After a successful publish (`202`, fixture `05c`/`17`), the draft resource is
gone; POSTing publish again returns **`404 {"status":404,"message":"Not
found."}`** (fixture `06`). Consequence for the client: a publish retry after
a 5xx must NOT treat 404 as "record vanished" — reconcile by
`GET /api/records/{id}` and check `status == "published"` (the plan's
"reconcile by re-GET, never blind-retry" rule, confirmed necessary).

### 2. `POST /versions` while a version draft already exists → **idempotent**

Returns **`201` with the SAME draft id** (585301 both times; fixture `08` vs
`11`). No error, no second draft. Safe to re-run after a crash — the
new-version step of the push transaction needs no existence pre-check.

### 3. Do 429s carry `Retry-After`? → **Yes**

A real 429 (search endpoint, tripped at request 31) carried both
**`retry-after: 49`** and `x-ratelimit-reset` (fixture `47-429.headers`).
Body: `{"message":"30 per 1 minute","status":429}`. Bonus: **`retry-after`
also appears on many `200` responses** (e.g. fixtures `34`, `46`) — the
retry layer must key on status code, not header presence. Observed rate
buckets differ per endpoint class:

| Endpoint class | Limit observed |
|---|---|
| general API (records CRUD) | `x-ratelimit-limit: 133`/min (sandbox) |
| search (`/api/records?q=`) | 30/min (429 at 31st) |
| file content download | `x-ratelimit-limit: 1000`/min |

Keep `internal/client/retry.go`'s "decline waits longer than maxRetryDelay"
rule; honor `Retry-After` first, else `X-RateLimit-Reset`.

### 4. Multipart (`M` transfer) availability → **Available**

Registering `{"key":…,"size":…,"transfer":{"type":"M","parts":2,
"part_size":104857600}}` returns `201` with per-part upload URLs under
`links.parts[]`, each with a **~14-day expiration** (fixture `39`).
`Caps.MultipartUpload = true` for Zenodo sandbox; part URLs must be used
promptly and re-registered if expired.

### 5. Draft listing via `GET /api/user/records` → **works, with a trap**

Returns the **latest version per concept** with a `status` field
(`"draft"`/`"published"`) (fixture `34`). ⚠ The documented-looking query
param **`?is_published=false` is silently ignored** (fixture `35` — identical
results). The **search-query form works**: `?q=is_published:false` returns
only drafts (fixture `36`). Preflight cleanup of crashed pushes should use
the `q=` form and double-check `status`.

### 6. Empty-file rejection → **⚠ differs from plan §2.4: NOT rejected**

A zero-byte file registers, uploads (`200`), commits (`200`, `size: 0`,
`checksum: md5:d41d8cd98f00b204e9800998ecf8427e`) and **publishes (`202`)**
(fixtures `23`–`27`, published record 585303). The plan's "empty files are
rejected" assumption does not hold on current sandbox. Keep the zero-byte
warning in `datapin check` as a lint (empty files are almost never intended)
but do not model it as a server-enforced failure in `fakeinvenio`'s default
behavior — make it a configurable strictness knob instead.

### 7. 101st-file rejection → **enforced at registration, atomically**

- 101 keys in one `POST …/draft/files` → **`400 "Uploading selected files
  will result in exceeding the max amount per record."`** and **zero entries
  registered** (batch is atomic; fixtures `31`, then `32` proves 100 clean).
- 100 keys → `201`, all registered (fixture `32`).
- One more key on a full draft → same `400` (fixture `33`).

So the limit surfaces early (registration), not at publish. `datapin check`
can still preflight it client-side for a better message, but the server
catches it before any bytes move.

### 8. Pending-file-blocks-publish → **confirmed**

Publish with a registered-but-never-uploaded entry returns
**`400 {"errors":[{"field":"files","messages":["One or more files have not
completed their transfer, please wait."]}]}`** (fixture `22`). Preflight must
list draft files and `DELETE` entries with `status: "pending"` (a crashed
upload) before publishing — deleting a pending entry works (`204`, fixture
`26`).

### 9. `files-import` semantics → **copy-by-reference, all-or-nothing, empty-draft-only**

- A fresh version draft starts with **zero files** (fixture `09`).
- `POST …/draft/actions/files-import` → `201`, copies **all** previous
  version's files with the **same `file_id`** (no bytes moved), fresh
  `bucket_id`, `status: "completed"`, checksums preserved (fixture `10`).
- Running it again (draft non-empty) → **`400 {"field":"files.enabled",
  "messages":["Please remove all files first."]}`** (fixture `12`). There is
  no partial/selective import: the changed-files flow is import-all → DELETE
  changed keys (`204`, fixture `13`) → re-register/upload/commit → publish
  (fixtures `14`–`17`).

## Findings beyond the checklist

- **`metadata.publisher` is required to publish** (DOI registration):
  publish without it → `400` with structured
  `errors[]: {field: "metadata.publisher", messages: [...]}` (fixture `05`).
  Validation errors generally arrive as this `{status, message, errors[]}`
  shape — `datapin publish` should surface `errors[]` verbatim. The
  manifest metadata block / `datapin check` must include publisher (default
  "Zenodo" for Zenodo remotes is what the UI does).
- **Publish responses are legacy-shaped.** `POST …/actions/publish` (202)
  and several other Zenodo-sandbox responses carry top-level `doi`,
  `conceptdoi`, `recid`, `state` rather than the InvenioRDM-reference
  `pids.doi.identifier` shape (fixtures `05c`, `17`). The draft-create
  response is also a hybrid (fixture `01`). The client must read DOIs
  defensively from both shapes; `fakeinvenio` should mimic the hybrid.
- **DOI mechanics confirmed**: concept DOI `10.5072/zenodo.585299` +
  version DOIs `…585300`/`…585301`; version chain readable via
  `GET /api/records/{id}/versions` with `metadata.relations.version[0]
  {index, is_last}` (fixture `40`). `10.5072` is the sandbox/test prefix.
- **Reserve-DOI works pre-publish**: `POST …/draft/pids/doi` → `201` with
  the DOI at top-level `doi` (+ `doi_url`) on sandbox (fixture `51`) —
  feeds the plan's `push --reserve`.
- **Checksums**: commit/list responses carry `checksum: "md5:<hex>"`
  exactly as assumed (fixtures `04`, `41`). ⚠ Downloads do **not** send
  `Content-MD5`; they send **`oc-checksum: MD5:<hex>`** (fixture `46`) and
  support `Accept-Ranges: bytes`. Verify-after-download should use
  `oc-checksum` when present, else hash the stream (we hash anyway).
- **Draft discard leaves no trace**: `DELETE …/draft` → `204`; the id then
  404s ("The persistent identifier does not exist.", fixtures `43`–`45`) —
  matches the contract-suite assertion planned in §6.
- **`versions/latest` link redirects**: `GET /api/records/{old}/versions/
  latest` → `301` to the latest record (fixture `42`) — follow redirects,
  or use the `links.latest` of a fresh GET.
- **Resource types are instance-defined**: `GET /api/vocabularies/
  resourcetypes?size=100` → 43 entries on sandbox (fixture `53`) —
  confirms "probe, don't hardcode" (plan §2.4).
- **Sandbox account state is visible** via `GET /api/user/records`
  (fixture `00` shows the clean slate) — live tests can assert their own
  residue cleanup.

## Consequences for `fakeinvenio` (seed list)

1. Draft create returns hybrid legacy+RDM shape (fixture `01`).
2. Three-step file flow with real MD5 in commit (fixtures `02`–`04`).
3. Publish: 202 + legacy-shaped body; second publish on same id: 404.
4. Publish validation: missing publisher → 400 `errors[]`; pending file →
   400 `errors[]` (fixtures `05`, `22`).
5. `POST /versions`: idempotent 201 returning the existing draft.
6. `files-import`: all-or-nothing, empty-draft-only, same `file_id`.
7. File-count cap at registration, atomic batch rejection.
8. Zero-byte files accepted end-to-end (strictness knob, default off).
9. Rate-limit headers on every response; `Retry-After` present on 429 …and
   on 200s (client must not misread it).
10. `q=is_published:false` supported; `is_published` query param ignored.

## Live-tier notes

- Sandbox test records created by this spike: 585300/585301 (v1/v2 chain),
  585303 (empty-file probe) under concepts 585299/585302. Published records
  cannot be deleted via API; sandbox is periodically wiped — tests must
  create their own records and never assume persistence.
- The spike deliberately did NOT probe: community submission flows (out of
  scope for v1), embargo/restricted access (Phase 5), files-import when the
  previous version has zero files, and the 30-day post-publish UI edit
  window (not API-verified, per plan).
