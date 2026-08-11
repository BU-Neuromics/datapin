# Roadmap

datapin is the reboot of gosf as a general-purpose FAIR data publication
tool. The authoritative roadmap is the phased plan in
[`docs/reboot-plan.md` §8](./docs/reboot-plan.md#8-phased-roadmap):

| Phase | Scope |
|-------|-------|
| 0 | Zenodo sandbox spike (`docs/zenodo-notes.md`) — **done** |
| 1 | Backend interface, InvenioRDM/Zenodo driver, manifest v2, `publish` — **shipped v0.1.0** (OSF adapterization deferred → #16) |
| 2 | Metadata model, `check` (+`--fair`), reserve-DOI, `export` — **shipped v0.1.0** |
| 3 | Site generator, landing pages, gh-pages deploy — **shipped v0.1.0** (`migrate` → #17) |
| 4 | Figshare adapter, cross-adapter contract suite, `check --fair` (F-UJI) — **shipped v0.2.0** (Dataverse included) |
| 5 | S3/SFTP/dir workspace backends with journal versioning — **shipped v0.2.0**; remaining breadth below |

Phases 0–5 shipped in v0.1.0/v0.2.0. Remaining work is tracked as
[GitHub issues](https://github.com/BU-Neuromics/datapin/issues): live
verification of the Figshare/Dataverse drivers (#14, #15), OSF behind the
workspace interface (#16) and `migrate` (#17), native versioning for
versioned S3 buckets (#18), invenio multipart uploads (#19), per-instance
InvenioRDM Caps probing (#20), site-generator extensions (#21), Cloudflare
Pages (#22), embargo (#23), DVC interop (#24), the onboard rewrite (#25),
skills.sh re-registration (#26), Dryad-if-demanded (#27), and two policy
sign-offs (#28).

Operational decisions (settled) are recorded in
[`docs/datapin-handoff.md`](./docs/datapin-handoff.md). The pre-rename gosf
roadmap (v1.x/v2.x OSF content operations, all shipped) is preserved in this
file's git history.
