# Roadmap

This roadmap covers planned content-management features for agents. The scope
is deliberately limited to _content_ operations (files, metadata, wiki). User
management, permissions, and project administration are out of scope.

## v1.1 — File operations and node metadata — Released (v1.1.0)

**Status:** ✅ Released 2026-06-05 ([`v1.1.0`](https://github.com/BU-Neuromics/datapin/releases/tag/v1.1.0)). All four commands below are shipped.

Builds on infrastructure already in place (Waterbutler client, OSF metadata
client) with minimal new API surface.

| Command | Description |
|---------|-------------|
| `datapin mv <src> <dest>` | Rename or move a file or folder within OSF Storage |
| `datapin cp <src> <dest>` | Copy a file or folder (across projects supported) |
| `datapin mkdir <project>:<path>` | Create a folder in OSF Storage |
| `datapin set <project> [flags]` | Update node title, description, category, or tags |

`datapin mv` updates `datapin.toml` automatically if the moved path has a manifest
entry.

`datapin set` flags: `--title`, `--description`, `--category`, `--tags`.

## v1.2 — Wiki and components

New API surface (node write path, wiki endpoints); deserves its own release
and test coverage.

| Command | Description |
|---------|-------------|
| `datapin wiki ls <project>` | List wiki pages |
| `datapin wiki get <project> <page>` | Print wiki page content |
| `datapin wiki set <project> <page>` | Create or update a wiki page (`--file` or `--message`) |
| `datapin mkproject [parent] --title <t>` | Create a top-level project, or a sub-component when a parent GUID is given |

## Later / under consideration

- CEDAR / custom file metadata (`/cedar_metadata_records/`)
- Comments (`POST /nodes/{id}/comments/`)
- `datapin status --remote-newer` CI mode (fail only on REMOTE_NEWER)
