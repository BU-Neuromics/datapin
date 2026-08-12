# Roadmap

datapin is the reboot of gosf as a general-purpose FAIR data publication
tool: archive backends (Zenodo/InvenioRDM, Figshare, Dataverse) with
DOI-minting `publish`, and journal-versioned workspace remotes (dir/S3/SFTP)
for mutable intermediate results. The build-out phases are done; what
follows is the **release ladder to v1.0**, tracked live in
[issue #40](https://github.com/BU-Neuromics/datapin/issues/40) and the
[v1.0 milestone](https://github.com/BU-Neuromics/datapin/milestone/1).

## Context: the OSF sunset

OSF announced it is sunsetting its projects service (2026-08). datapin's
OSF support — its entire pre-reboot surface — is **frozen** (working, but
receiving no new investment) and will be removed after the shutdown (#31).
The migration tool (#30) is the deadline-driven centerpiece of the ladder:
it only has value while OSF's API is still up.

## Shipped

| Release | Scope |
|---------|-------|
| v0.1.0 | Backend interface, Zenodo/InvenioRDM driver, manifest v2 `[[datasets]]`, `publish`, metadata subsystem (`check`/`export`/`cite`), site generator (plan §8 phases 0–3) |
| v0.2.0 | Figshare + Dataverse adapters, cross-adapter contract suite, dir/S3/SFTP workspace backends with journal versioning (phases 4–5) |

## The ladder (details in #40)

| Release | Theme | Key items |
|---------|-------|-----------|
| v0.3.0 | Correctness + live verification — **on dev now** | License policy D37 (explicit license at publish), `contact_email` (D38), Dataverse live-verified end to end (D37–D42, six divergences fixed), live CI tiers for Zenodo/Dataverse/workspace protocols + CLI E2E, pull pin-integrity gate |
| v0.4.0 | The OSF exit | `datapin migrate` (#30), skills.sh re-registration (#26), #28 sign-off |
| v0.5.0 | First-run experience + robustness | `onboard` rewrite around publish (#25), invenio multipart uploads (#19), per-instance InvenioRDM Caps probing (#20) |
| v1.0 | Declare it | Docs pass, **published docs site + task guides + troubleshooting + `CITATION.cff` (#54)**, fresh-eyes walkthrough, live suites green for weeks — see #40 |
| v2.0 | After the OSF shutdown | Remove the OSF surface, `migrate`, and gosf back-compat (#31) |

Not gating v1.0 (demand-driven): Figshare live verification (#14), Dryad
(#27), versioned-S3 native scheme (#18), site extras (#21, #22), embargo
(#23), DVC interop (#24).

Architecture: [`docs/reboot-plan.md`](./docs/reboot-plan.md). Settled
decisions: D1–D10 in [`docs/datapin-handoff.md`](./docs/datapin-handoff.md),
D11–D42 in [`docs/decisions.md`](./docs/decisions.md). The pre-rename gosf
roadmap (v1.x/v2.x OSF content operations, all shipped) is preserved in this
file's git history.
