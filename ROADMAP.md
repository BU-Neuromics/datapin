# Roadmap

datapin is the reboot of gosf as a general-purpose FAIR data publication
tool. The authoritative roadmap is the phased plan in
[`docs/reboot-plan.md` §8](./docs/reboot-plan.md#8-phased-roadmap):

| Phase | Scope |
|-------|-------|
| 0 | Zenodo sandbox spike — verify API behaviors, seed `fakeinvenio` fixtures (`docs/zenodo-notes.md`) |
| 1 | Backend adapter interface (workspace/archive roles), OSF workspace adapter carried over, InvenioRDM/Zenodo archive driver, manifest v2, `publish` |
| 2 | Metadata model + `check` linter, SPDX/ORCID/ROR validation, reserve-DOI, `export` (datapackage.json, RO-Crate) |
| 3 | Static site generator (goldmark), dataset landing pages, gh-pages deploy, `migrate` wiki import |
| 4 | Figshare adapter, cross-adapter contract suite, `check --fair` (F-UJI) |
| 5 | S3/SFTP workspace backends, Dataverse, generic InvenioRDM, embargo, Cloudflare Pages |

Operational decisions (settled) are recorded in
[`docs/datapin-handoff.md`](./docs/datapin-handoff.md). The pre-rename gosf
roadmap (v1.x/v2.x OSF content operations, all shipped) is preserved in this
file's git history.
