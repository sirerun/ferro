# Bulk v2 baseline audit

Audit date: 2026-09-24
Scope: read-only G00 source and installed-artifact inventory. This report is
not a contract approval, runtime qualification, or release record.

## Source and saved-work inventory

Current `main` and this audit worktree both resolve to
`38516376385f33f17fccc6079f1e0de65a5c967e` (2026-09-24). The main checkout has
one untracked historical handoff, which was read as historical context and left
untouched. This audit worktree began clean on `codex/bulk-v2-baseline`.

Other relevant worktrees and revisions at audit time:

| Worktree/branch | Revision | Disposition |
|---|---|---|
| `codex/bulk-gate-correction` | `7515f6fbeac8d83127cba4df935caad45af7d97e` | Documentation correction; separate from runtime implementation. |
| `codex/ferro-chat-polish` (remote ref) | `ea581c315e9c302c11fe6e4f8fee39edf7aaae23` | Known runtime fix branch; deliberately pending later release/reconciliation. Not merged or installed by this audit. |
| `codex/amsl-credentials` | `f2d8244cb18fbf637326f2daf9cc7293b722219c` | Credential-adapter work remains a separate branch and separate reconciliation item. |
| `codex/connection-prompts` | `a029906143907dd1e992a2f93b69946f9765412b` | Extension source comparison only; separate worktree. |
| `codex/bulk-v2-docs` | `0b745b363d5108414aec7a505123dda1187202f9` | Documentation worktree. |
| `codex/bulk-v2-integration` | `177b54212d75adf0c0f91fdf55cd44e37aa2f9ee` | Integration preparation worktree. |
| `codex/bulk-v2-g01` | `38516376385f33f17fccc6079f1e0de65a5c967e` | Contract-candidate worktree; not reviewed or accepted here. |
| `codex/hosted-launch-plan` | `87e384d6724a31aa12122ccd2112be15975aadf8` | Separate planning worktree. |
| `codex/plans-cohesion` | `0530abb967c3627f4be1da25114c5686ea1a1e6d` | Separate planning worktree. |
| `codex/zatiti-browser-plan` | `dae806abffa34a1bc76a21179e01ca01cdf17617` | Separate planning worktree. |

The main repository's only saved stash is `stash@{0}` at
`1dee5406d606b50bdf699dcb68b31470981fab06`, described as preservation of
original plan drafts before plan reconciliation. No separate Ferro archive
directory was found among the sibling project directories inspected. The
historical handoff mentions an earlier `STASH` branch, but no current ref by
that name was present; this report does not infer that the current stash is the
same saved work.

## Installed service metadata

The installed service executable SHA-256 is
`e212dc34e8c30b602ed2eb0adc99bd408d9f99948c807bd996aed8c5a7e0b660`.
It was inspected with `go version -m`; it was not executed. Metadata reports:

- Go toolchain: `go1.27.1`; target `darwin/arm64`; cgo enabled.
- Module: `github.com/dndungu/ferro`, version
  `v0.0.0-20260923002235-40b051622340+dirty`.
- Embedded VCS revision: `40b051622340a016cb577829fc87b440f6bb872d`, timestamp
  `2026-09-23T00:22:35Z`, `vcs.modified=true`.
- Embedded dependencies are recorded by `go version -m` on the inspected
  executable; no installed runtime invocation or network action was made.

The embedded revision resolves to a source commit but is not contained in the
currently inspected local or remote branches. The `+dirty` and
`vcs.modified=true` metadata mean the binary cannot be reconciled to that
commit alone. Preserve the installed build as an independent artifact pending
the later runtime release/reconciliation gate; do not infer its patch contents
from the version string.

## Extension inventory and comparison

Installed manifest version is `0.1.8`. Current main's manifest version is
`0.1.3`; `codex/connection-prompts` is `0.1.8`. Versions identify package
metadata only and do not prove that the installed extension is a released or
accepted build.

The following SHA-256 values are for the installed extension files. Status is
relative to the same path in current main. `same` means byte-identical;
`diff` means the path exists in both but differs; `installed-only` means no
same-path source file exists in main. The compared `codex/connection-prompts`
assets match installed files where noted.

| File | Installed SHA-256 | Main comparison | Connection-prompts comparison |
|---|---|---|---|
| `DESIGN-NOTICE.md` | `f8d0790be3c882375f2b8a5c6346d926536cc0a76277120c21979c0ec6e4745d` | same | same |
| `LICENSE` | `9e469d4d5d5a96a61021d0808bfa761fc4f303eca48896056b68e37a2adde829` | installed-only | installed-only |
| `adapter.js` | `a4784d873b7d1063c93943d26e83f074a998e5918baebd966cbae0bd79fd3e35` | same | same |
| `background.js` | `3a441213d919c995f37a144c07a751e586fa74ff4caed6e0965c0cb02aa94011` | diff | same |
| `background.js.before-panel-handoff-20260923` | `df7323ae2848393ba5cac4ed890149a41949dfc3d1bb9f5c9bbcec6e0daef1b7` | installed-only | installed-only |
| `content.js` | `bef447e1f33011922181e73ac47adb5768f9ae75a1b920250fbca636378d3e78` | same | same |
| `glass-chat.css` | `3cd98a96d08f71634c0b4e25c61d90e4a92b0fb55f11360a39125718c82b8151` | same | same |
| `icons/ferro-128.png` | `109395910a63f2e4597c3090d53eaa7045500fb1435c4736c0d2f1840a45a8c1` | same | same |
| `icons/ferro-16.png` | `82e5bb4c0d62ca159fcfb43b3f6090e51b9485c40c9800f3336f49b720802c4a` | same | same |
| `icons/ferro-32.png` | `a343dce50c192de2fede58892df41ff67b49a43d2e72a99ee44c6a299b0f0903` | same | same |
| `icons/ferro-48.png` | `afc53c2f7e296f91d587bfd05ac1d58651eb9993bf83845e840cded554a5a197` | same | same |
| `icons/ferro-logo-source.png` | `533d49e34117a1468cab9d348a2a49a0d9ee617f7b130f5e1368a8137318aaca` | same | same |
| `icons/ferro-mark-master.png` | `25e3095585dc4c28cbd837fc52144e1e95da7879199c95498ed80113a0f4722a` | same | same |
| `icons/ferro-mark.svg` | `3ca27bf7fb34648eae7bb9220fea7bb6a79d76b64af39e62518a51b0740b6398` | same | same |
| `manifest.json` | `0af064da8978634d901ed5346eda648f382de3a0324ded2c61ff7cb7081872f0` | diff | same |
| `popup.css` | `cfaba0777aebb11359d5bad17f12f0452a797b737a6f5d6ae24179c789ad1f7e` | installed-only | same |
| `popup.html` | `2c4f8239de0c8f4a769e0f355f7ad0c76939ba9d252648ab0f14dda8169de3a7` | diff | same |
| `popup.js` | `c802dc9175d15ec5fe4db1bcfc6ade4be166e29b2238a84bc33b523fbd08ef12` | diff | same |
| `sidepanel.css` | `cdfb4688fe02fc280d1941b2bc96037c86e619d7b5192a611387ff01ba92c9e8` | same | same |
| `sidepanel.html` | `8edfe58baa28e85cb133f9b88463082d4a7317fadbb35184fd7e4ee4fda5de08` | same | same |
| `sidepanel.html.before-toggle-20260922` | `8edfe58baa28e85cb133f9b88463082d4a7317fadbb35184fd7e4ee4fda5de08` | installed-only | installed-only |
| `sidepanel.js` | `e6e9b76d50b119b31275a7af4c1ab075f2d606ab7ad28e5023a5672383fbd828` | diff | same |
| `sidepanel.js.before-toggle-20260922` | `92323b87f7f8130a41b579202149ef6ed4879819bbf0da1e5b399e8bf2fa292f` | installed-only | installed-only |
| `testdata/fixture.html` | `23eb72b2633b6b0657adff4e19054fbb5af391526b10540dcd8ba783f8993456` | same | same |

Current main and `codex/connection-prompts` are not identical snapshots: main's
manifest is `0.1.3`, while the connection-prompts manifest matches the
installed `0.1.8` manifest. The installed extension matches connection-prompts
for all shared paths listed above. It also contains three dated backup files
that are absent from both source trees. This is an inventory, not an approval
to copy, install, or discard any extension files.

## Runtime gate and reconciliation

Current main has no `run_task_v2` runtime registration or implementation and no
accepted bulk-v2 contract gate artifact. The phrase `run_task_v2` appears in
planning documents only; those documents are not evidence of shipped
functionality. The candidate worktree at
`38516376385f33f17fccc6079f1e0de65a5c967e` is not reviewed or accepted by this
audit. Do not dispatch L01-L10 based on this report.

Reconciliation dispositions:

1. Installed service and extension versus source: **pending later release
   gate**. Preserve the installed build and runtime-fix branch; require explicit
   source/package review before reconciliation or installation.
2. Credential adapter: **separate branch and review path**. Do not fold it into
   the runtime-fix disposition or infer compatibility from its presence.
3. Bulk v2: **pre-contract audit only**. G00 inventory is documented;
   G01/contract acceptance remains with the coordinator.

No configuration values, tokens, credentials, account identifiers, browser
state, service process state, or client configuration content were read or
recorded. Config field inventory was not needed to establish these artifact
and source relationships.

## Executed checks and limits

Read-only commands used: `git status`, `git rev-parse`, `git worktree list`,
`git branch`, `git log`, `git stash list`, targeted `rg`, `find`, `shasum -a
256`, and `go version -m` against the installed binary. No builds or tests were
run. The service was not executed. No extension, installed application, user
configuration, launch agent, or root-worktree file was changed.

Limitations: the embedded Go build records modified source, so its complete
source tree cannot be reconstructed from the embedded revision. The
connection-prompts worktree is a comparison candidate, not proof of the exact
build inputs. This audit does not establish current live browser connectivity,
behavior, usage, contract correctness, or release readiness.
