# Built-in Skill Directory

`catalog.json` is the reviewed, embedded Skill directory shipped with Parsar.
Each item points at a pinned upstream commit and a vendored package under
`items/<id>/`. Importing an item uses the embedded files, not a user-supplied
URL or a live Git checkout.

## Included Sources

| Skill | Source | License | Pinned commit |
| --- | --- | --- | --- |
| Frontend Design | `https://github.com/anthropics/skills/tree/main/skills/frontend-design` | Apache-2.0 | `b29e7cf65e5cb78a5ac33d582270551bc74a14eb` |
| Webapp Testing | `https://github.com/anthropics/skills/tree/main/skills/webapp-testing` | Apache-2.0 | `b29e7cf65e5cb78a5ac33d582270551bc74a14eb` |
| React Composition Patterns | `https://github.com/vercel-labs/agent-skills/tree/main/skills/composition-patterns` | MIT | `7c180d9044c9ae2b442b567aad4e42a28dd5ed62` |
| MCP Builder | `https://github.com/anthropics/skills/tree/main/skills/mcp-builder` | Apache-2.0 | `b29e7cf65e5cb78a5ac33d582270551bc74a14eb` |
| Skill Creator | `https://github.com/anthropics/skills/tree/main/skills/skill-creator` | Apache-2.0 | `b29e7cf65e5cb78a5ac33d582270551bc74a14eb` |
| Internal Communications | `https://github.com/anthropics/skills/tree/main/skills/internal-comms` | Apache-2.0 | `b29e7cf65e5cb78a5ac33d582270551bc74a14eb` |
| Algorithmic Art | `https://github.com/anthropics/skills/tree/main/skills/algorithmic-art` | Apache-2.0 | `b29e7cf65e5cb78a5ac33d582270551bc74a14eb` |

The original license files are kept inside the vendored packages where the
upstream source provides them. Vercel's skill declares MIT in its `SKILL.md`.

## Updating an Item

1. Review the upstream package and its license.
2. Copy the complete Skill directory, including `SKILL.md`, references, rules,
   scripts, examples, and license files.
3. Pin `source_ref` to the exact 40-character commit SHA.
4. Update `version` and `updated_at` in `catalog.json`.
5. Run the Skill catalog tests and `make check`.

Catalog packages must not contain API keys, tokens, passwords, or executable
install hooks. Import stores the package and does not run its scripts.
