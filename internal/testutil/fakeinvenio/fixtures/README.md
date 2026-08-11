# Zenodo sandbox fixtures (Phase 0 spike, 2026-08-10)

Real request/response captures from sandbox.zenodo.org, taken by the Phase 0
spike documented in `docs/zenodo-notes.md` (which cites fixtures by number).
Tokens and cookies are redacted. Each probe `NN-name` has up to three files:

- `NN-name.request` — method, URL, and body sent
- `NN-name.headers` — response status line + headers
- `NN-name.body` — response body, verbatim

These seed the `fakeinvenio` hermetic test server (plan §6): when fakeinvenio
lands, its responses should match these shapes — including the Zenodo
legacy/RDM hybrid serialization quirks the notes call out.
