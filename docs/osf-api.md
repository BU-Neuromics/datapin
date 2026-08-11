# OSF API notes (workspace adapter reference)

Moved out of CLAUDE.md during the datapin reboot. This documents the OSF
API surface the current sync engine (the future OSF workspace adapter,
see `docs/reboot-plan.md` §4.2/§4.7) is built on.

## OSF API — two-tier architecture

### Tier 1 — Metadata REST API (JSON:API spec)

Base: `https://api.osf.io/v2`
Auth header: `Authorization: Bearer <token>`

Key endpoints:
- `GET /nodes/{id}/` — project metadata
- `GET /nodes/{id}/files/osfstorage/` — list files at root
- `GET /nodes/{id}/files/osfstorage/?path=/subdir/` — list files in subdir
- `GET /files/{file_id}/` — file metadata (includes download link)
- `GET /files/{file_id}/versions/` — all versions, newest-first (no `embed=user`: the OSF versions endpoint has no embeddable user relationship and returns 400 if one is requested)

### Tier 2 — Waterbutler (actual file bytes)

Base: `https://files.osf.io`

- Upload new file: `PUT https://files.osf.io/v1/resources/{node_id}/providers/osfstorage/?name={filename}`
- Upload existing: PUT to the file's `upload` link from metadata API (creates a new version)
- Download: follow the `download` link from file metadata response
- Download specific version: append `?revision={n}` to the download URL (`client.RevisionURL`)

Path resolution: walk Tier 1 tree to resolve a path string to a Waterbutler URL.
This is the core complexity — isolated in `internal/resolver/path.go`.

### Wikis (Tier 1 only — no Waterbutler)

OSF project wikis are versioned markdown pages served entirely from the metadata
API. `internal/client/wiki.go`:

- `GET /nodes/{id}/wikis/` — list pages (paginated, `-modified` order) → `ListWikis`
- `GET /wikis/{wiki_id}/content/` — **plain text** latest content → `GetWikiContent`
- `GET /wikis/{wiki_id}/versions/` — versions (type `wiki-versions`, `id` = integer number) → `GetWikiVersions`
- `GET /wikis/{wiki_id}/versions/{n}/content/` — plain text of a version → `GetWikiVersionContent`
- `POST /nodes/{id}/wikis/` `{data:{type:"wikis",attributes:{name,content}}}` → `CreateWiki`
- `POST /wikis/{wiki_id}/versions/` `{data:{type:"wiki-versions",attributes:{content}}}` → `CreateWikiVersion`
- `PATCH /wikis/{wiki_id}/` (rename) → `RenameWiki`; `DELETE /wikis/{wiki_id}/` → `DeleteWiki`

Notes:
- **No server-side content hash.** Wiki versions expose only integer identifiers
  + size, so datapin computes MD5s itself from fetched content (pages are KB-scale).
- Page names: ≤100 chars, no `/`, non-blank, unique per node. The `home` page
  cannot be renamed or deleted — datapin refuses both client-side (`isHomeWiki`).
- Wiki addon can be disabled per node → `404 "The wiki for this node has been
  disabled."`, recognized by `client.IsWikiDisabled` and mapped to an actionable
  message by `friendlyWikiError`.
- Registrations are read-only via this API (create → 405). Reads work anonymously
  on public projects.
- **Content is canonicalized, not byte-exact.** OSF normalizes wiki content on
  write — CRLF→LF and surrounding whitespace trimmed (its DRF content field is
  `trim_whitespace=True`) — so a byte-exact round trip is impossible. datapin hashes
  and compares a **canonical form** (`client.CanonicalizeWikiContent`: CRLF/CR→LF,
  `TrimSpace`) applied to *both* local and remote content, so idempotent pushes and
  sync classification are stable regardless of OSF's exact rule. Wiki local MD5s use
  `wikiLocalMD5`/`wikiContentMD5` (canonical), not the raw-bytes `computeLocalMD5`
  used for storage files. `fakeosf` independently reproduces OSF's normalization
  (`osfNormalizeContent`) so the hermetic tiers catch regressions; the live tier
  asserts the canonical round trip + idempotency (`TestLive_WikiCanonicalRoundTrip`).
- **Wiki content endpoints speak `text/markdown`, not JSON:API.** `GET
  /wikis/{id}/content/` (and the per-version variant) are served by OSF's
  `PlainTextRenderer`, so `getText` sends `Accept: text/markdown, */*` — sending the
  JSON:API Accept (as the metadata calls do) returns 406 Not Acceptable.

## OSF API notes

### JSON:API response shapes

Files list (`/nodes/{id}/files/osfstorage/`):
```json
{
  "data": [
    {
      "id": "...",
      "attributes": {
        "name": "filename.csv",
        "kind": "file",          // or "folder"
        "size": 12345,
        "date_modified": "...",
        "materialized_path": "/data/results/file.csv"
      },
      "links": {
        "download": "https://files.osf.io/...",
        "upload": "https://files.osf.io/...",
        "delete": "https://files.osf.io/..."
      },
      "relationships": {
        "files": { "links": { "related": { "href": "..." } } }
      }
    }
  ],
  "links": { "next": "..." }
}
```

Node metadata (`/nodes/{id}/`):
```json
{
  "data": {
    "id": "abc12",
    "attributes": {
      "title": "My Project",
      "description": "...",
      "date_created": "...",
      "date_modified": "...",
      "public": true
    }
  }
}
```

### Pagination

All list endpoints paginate. Check `links.next` and follow until null.

### Component addressing

`abc12/xyz34:/path` — `abc12` is the parent project GUID, `xyz34` is the
component (child node) GUID. The path is resolved under `xyz34`.

