# herdr-source-control Implementation Plan

## 1. Document Status

This document is the implementation contract for `herdr-source-control` version 0.2.0.
It is intentionally more specific than a normal roadmap: product behavior, Git semantics,
UI states, architecture, testing, Herdr integration, packaging, and completion criteria are
decided here so implementation can proceed without inventing missing requirements.

When implementation reveals that a decision here is impossible or unsafe, update this plan
in the same change that alters the decision. Do not silently diverge from it.

### Locked decisions

- Repository name: `herdr-source-control`
- Plugin ID: `herdr-source-control`
- Display and tab name: `Source Control`
- Binary name: `herdr-source-control`
- Module path: `github.com/mariotmc/herdr-source-control`
- Language: Go 1.25
- UI: Bubble Tea v2, Lip Gloss v2, and Bubbles v2
- Platforms in v1: Linux, including Herdr running inside WSL
- Git implementation: invoke the user's system `git`; do not use `go-git` or libgit2
- Primary presentation: one dedicated Herdr tab, never an automatically docked sidebar
- Changed-file list in v1: read-only
- Mutations in v1: branch checkout, branch creation, fetch, fast-forward, and push only
- No Nerd Font, emoji, or private-use glyph requirement
- No Herdr startup hooks. Two focus event hooks exist, and they only run a short-lived background
  fetch; they never open a pane or tab
- No filesystem watcher in v1; use polling, focus refresh, and manual refresh

## 2. Product Summary

`herdr-source-control` restores the small part of VS Code's Source Control experience that is
useful while working exclusively in Herdr:

1. See every changed file, grouped by Git state and sorted by path.
2. See the current branch and its upstream ahead/behind state.
3. Refresh the view manually when desired, while normally keeping it current automatically.
4. Synchronize the current branch with its configured upstream.
5. Search local branches, check one out, or create a new branch.

It is not intended to be a complete Git client in v1. Its value is immediate repository
awareness with a deliberately small interaction surface.

### 2.1 VS Code reference implementation

The design was cross-checked against Microsoft VS Code at commit
[`4b2897c7cb6c64e193664d4a19d62a01c00857c8`](https://github.com/microsoft/vscode/commit/4b2897c7cb6c64e193664d4a19d62a01c00857c8),
the `main` revision inspected on 2026-07-31. VS Code is a behavioral reference, not a code base to
port: its Git extension runs in an extension host and publishes SCM resources across an Electron
process boundary, while this plugin is one in-process terminal application.

Primary references:

- Git status/resource projection:
  [`extensions/git/src/repository.ts`](https://github.com/microsoft/vscode/blob/4b2897c7cb6c64e193664d4a19d62a01c00857c8/extensions/git/src/repository.ts#L2955-L3096)
- Git status invocation/parser:
  [`extensions/git/src/git.ts`](https://github.com/microsoft/vscode/blob/4b2897c7cb6c64e193664d4a19d62a01c00857c8/extensions/git/src/git.ts#L821-L882)
- Refresh/watch/debounce behavior:
  [`extensions/git/src/repository.ts`](https://github.com/microsoft/vscode/blob/4b2897c7cb6c64e193664d4a19d62a01c00857c8/extensions/git/src/repository.ts#L3176-L3225)
- Atomic model refresh and operation tracking:
  [`extensions/git/src/repository.ts`](https://github.com/microsoft/vscode/blob/4b2897c7cb6c64e193664d4a19d62a01c00857c8/extensions/git/src/repository.ts#L2707-L2937)
- SCM tree identity/sorting/update serialization:
  [`src/vs/workbench/contrib/scm/browser/scmViewPane.ts`](https://github.com/microsoft/vscode/blob/4b2897c7cb6c64e193664d4a19d62a01c00857c8/src/vs/workbench/contrib/scm/browser/scmViewPane.ts#L726-L866)
- Branch checkout picker and branch creation UX:
  [`extensions/git/src/commands.ts`](https://github.com/microsoft/vscode/blob/4b2897c7cb6c64e193664d4a19d62a01c00857c8/extensions/git/src/commands.ts#L2852-L3076)
- Sync orchestration:
  [`extensions/git/src/repository.ts`](https://github.com/microsoft/vscode/blob/4b2897c7cb6c64e193664d4a19d62a01c00857c8/extensions/git/src/repository.ts#L2397-L2453)
- Status parser tests:
  [`extensions/git/src/test/git.test.ts`](https://github.com/microsoft/vscode/blob/4b2897c7cb6c64e193664d4a19d62a01c00857c8/extensions/git/src/test/git.test.ts#L12-L136)
- Git SCM smoke tests:
  [`extensions/git/src/test/smoke.test.ts`](https://github.com/microsoft/vscode/blob/4b2897c7cb6c64e193664d4a19d62a01c00857c8/extensions/git/src/test/smoke.test.ts#L70-L206)

Battle-tested patterns adopted:

- Conflicts are projected first and only into Merge Changes.
- Index and worktree status are projected independently, so one path can appear in both Staged
  Changes and Changes.
- Resource identity includes the group and raw path, not rendered text or numeric row position.
- One refresh runs at a time with at most one trailing refresh.
- Mutations block competing mutations and always trigger a complete status refresh on both
  success and failure.
- Model publication is atomic; stale read results do not replace current state.
- Branches default to recent-commit ordering and may show age, author, abbreviated OID, and
  subject.
- Checkout first attempts a normal non-forced operation and relies on Git to detect dirty-tree
  conflicts.

Intentional improvements over the inspected implementation:

- Porcelain v2 rather than v1, with byte-preserving paths rather than UTF-8 decoding.
- Request IDs and repository epochs in addition to cancellation.
- No incremental cross-process resource splices because the TUI is in one process.
- Exact Git validation for branch names instead of silently sanitizing user input.
- Proactive linked-worktree branch annotation instead of waiting for checkout failure.
- Conservative fast-forward-only Sync with exact-ref fetch/push and phase identity checks.

## 3. Goals and Non-Goals

### 3.1 Goals

- Open or focus one Source Control tab for the repository associated with the invoking pane.
- Start the next automatic Git status check within two seconds of an external working-tree or
  index change while the pane is focused. The visible update follows when that command completes.
- Keep remote-tracking refs current in the background so ahead/behind is truthful in the pane and
  in Herdr's own sidebar indicators, including while no Source Control tab is open.
- Preserve a manual Refresh control as an explicit source-of-truth action.
- Match VS Code's group vocabulary and overall information hierarchy.
- Make branch and sync state understandable without color or special fonts.
- Keep all Git subprocesses off the Bubble Tea update/render path.
- Never directly issue a discard, reset, stash, force-checkout, or force-push command. Git hooks,
  filters, transports, and concurrent external processes remain outside the plugin's control.
- Work correctly in ordinary repositories, linked worktrees, unborn branches, detached HEAD,
  repositories under the WSL Linux filesystem, and repositories under `/mnt/c`.
- Keep the Git/domain layer independent of Bubble Tea so later diff and staging features do
  not require replacing repository logic.

### 3.2 Non-goals for v1

- Viewing file contents or diffs
- Staging or unstaging files or hunks
- Discarding changes
- Creating, amending, or undoing commits
- Resolving conflicts
- Browsing commit history
- Stash, tag, merge, rebase, reset, cherry-pick, or worktree management
- Branch rename or deletion
- Remote-only branch checkout
- Detached checkout
- Creating or selecting remotes/upstreams
- Publishing a branch without an upstream
- Pull request or hosting-provider integration
- Multi-repository aggregation inside one tab
- Native Windows execution
- Automatic plugin tabs in every Herdr workspace
- Runtime configuration reload
- Custom themes or user-remappable in-app keys in v1

### 3.3 Future features explicitly supported by the architecture

- Unified or side-by-side diff when a file is activated
- File and hunk staging/unstaging
- Commit message input and commit execution
- Conflict detail and resolution guidance
- History, stash, remotes, and richer branch operations
- Multiple repositories in one workspace

These are extension seams, not v1 requirements. Do not implement speculative UI or commands
for them in v1.

## 4. User Journeys

### 4.1 Open Source Control

1. The user focuses any pane rooted inside a Git repository.
2. The user invokes the configured Herdr plugin action, recommended as `prefix+shift+s`.
3. If that repository already has a Source Control tab in the current Herdr workspace, Herdr
   focuses it.
4. Otherwise the plugin opens a new tab named `Source Control`, rooted at that repository.
5. The initial loading state is replaced atomically with the first repository snapshot.

Invoking the action from a non-repository directory still opens the tab. The tab shows the
No Repository state and can be refreshed after a repository is initialized.

### 4.2 Monitor changes

1. The user or an agent edits files or the Git index.
2. The next two-second poll starts a refresh of the groups.
3. Selection remains on the same group and raw path when that row still exists.
4. The user may click Refresh or press `r` at any time to request an immediate refresh.

### 4.3 Switch branches

1. The user clicks the branch control or presses `b`.
2. A modal opens with search focused, a Create New Branch action, and local branches.
3. The user filters, selects an available branch, and confirms.
4. Git performs a non-forced checkout.
5. Success closes the modal and refreshes the complete repository snapshot.
6. Failure leaves the modal open and displays a sanitized Git error; user work remains intact.

### 4.4 Create a branch

1. The user selects Create New Branch or presses `n` from the branch picker.
2. A modal text input opens.
3. The name is validated with Git.
4. Git creates the branch from current HEAD and switches to it.
5. Success closes both modals and refreshes; failure preserves the input for correction.

### 4.5 Synchronize

1. The user clicks Sync or presses `s`.
2. The plugin verifies attached HEAD, configured upstream, no unresolved conflict operation,
   and no other plugin mutation.
3. It fetches the exact configured upstream and fast-forwards locally when possible.
4. It refreshes and verifies that branch/upstream identity did not change.
5. If the branch is ahead, it pushes the verified commit object to the exact upstream ref.
6. It refreshes after every outcome and displays a concise result.

## 5. Functional Specification

### 5.1 Repository selection

- The `open` action obtains a start directory from `focused_pane_cwd` in
  `HERDR_PLUGIN_CONTEXT_JSON`, falling back to `workspace_cwd`, then `$PWD`.
- The action attempts repository discovery but does not reject a non-repository directory.
- For a repository, the pane cwd and `HERDR_SOURCE_CONTROL_ROOT` are the absolute top-level
  working-tree path.
- For a non-repository, they are the absolute invoking directory.
- The TUI performs discovery again at startup and on each manual refresh. The action context
  is not trusted as durable application state.
- Nested repositories and submodules resolve to the innermost repository selected by Git.
- Bare repositories are unsupported and render the No Working Tree error state.

### 5.2 Changed-file grouping

Use VS Code-style groups in this fixed order:

1. `Merge Changes`: all unmerged/conflicted entries
2. `Staged Changes`: non-conflicted entries with a changed index status
3. `Changes`: non-conflicted entries with a changed worktree status plus untracked files

Rules:

- Hide empty groups.
- A file changed in both index and worktree appears once in Staged Changes and once in Changes.
- A conflict appears only in Merge Changes.
- Untracked files appear in Changes, not in a separate section.
- Ignored files are not shown.
- Sort each group by unsigned raw bytes of the current repository-relative path.
- Sort renames/copies by destination path.
- Group counts count rendered rows, not unique paths.
- The list is read-only. Activating a file shows `Diff view is not available yet.` for three
  seconds and performs no Git operation.

### 5.3 Visible statuses

Rows use compact status codes at every width; color supplements these text codes:

| Git state | Label | Compact code |
| --- | --- | --- |
| Added | `Added` | `A` |
| Modified | `Modified` | `M` |
| Deleted | `Deleted` | `D` |
| Renamed | `Renamed` | `R` |
| Copied | `Copied` | `C` |
| Type changed | `Type changed` | `T` |
| Unmerged | `Conflict` | `!` |
| Untracked | `Untracked` | `U` |
| Unknown future code | `Unknown (<code>)` | `?` |

Render a rename or copy as `old/path -> new/path`. Use ASCII `->`, not a Unicode arrow.

### 5.4 Refresh behavior

- Initial startup runs one immediate snapshot load.
- A recurring timer requests a snapshot every two seconds by default, but only while the pane is
  focused. A hidden tab keeps scheduling ticks and skips the snapshot, so it costs no subprocess.
- Focus defaults to visible. A terminal that never reports focus events therefore keeps polling
  rather than silently going stale.
- An unfocused-to-focused terminal transition requests a refresh.
- Clicking Refresh or pressing `r` requests a refresh immediately.
- Completion of checkout, branch creation, fetch, fast-forward, or push requests a refresh after
  every possible outcome.
- A running refresh does not blank the current snapshot.
- Manual refresh during an active refresh merges `RefreshManual` into one queued-reason bitset.
  Do not queue more than one additional refresh.
- Poll refreshes while one is active are dropped.
- Manual/focus/mutation refresh requests during a mutation merge their reason and run once after
  mutation; poll requests are dropped.
- A failed refresh preserves the last successful snapshot, marks it stale, and displays error
  feedback.
- Background poll and focus refreshes do not replace the stable footer status with progress text.
  Initial, manual, and mutation refreshes remain visible.
- Manual refresh also clears a transient informational message and retries repository discovery.
- Refresh itself never fetches or contacts a remote. It reads local remote-tracking refs, which a
  separate background auto-fetch keeps current.

#### 5.4.1 Background auto-fetch

Ahead/behind is only as truthful as the local remote-tracking ref, and Herdr renders its own
sidebar indicators from the same refs. A background fetch therefore runs independently of refresh.

- The running TUI fetches on a three-minute timer. The timer is deliberately not gated on
  visibility: its purpose is keeping the persistent sidebar truthful while the user is not looking
  at the Source Control tab.
- The TUI also fetches once after its first snapshot, and again after a successful checkout or
  branch creation. The post-mutation fetch ignores the throttle and runs after the replacement
  snapshot lands, so it uses the branch Git just confirmed.
- The one-shot `herdr-source-control fetch` subcommand runs from the `workspace.focused` and
  `pane.focused` Herdr event hooks, so refs refresh as the user moves around Herdr even when no
  Source Control tab is open.
- All of these share one throttle of three minutes per checkout. The throttle record is keyed by
  the working-tree root, so each linked worktree throttles independently: a fetch only updates the
  current branch's upstream, so a key shared across worktrees would let whichever worktree fetched
  first stop the others from refreshing their own branch. The record is stored under
  `$HERDR_PLUGIN_STATE_DIR/fetch/<key>.json` with an atomic replace. A missing, unreadable, or
  corrupt record reads as "never fetched" instead of failing.
- A fetch that fails because the upstream branch no longer exists on the remote does not mark the
  repository stale. It fails identically on every retry, so warning about it would be permanent
  noise on every merged branch rather than something the user can act on.
- The fetch is the canonical Section 8.8 fetch: the exact configured upstream refspec, no tags, no
  prune, no submodules, non-interactive. It runs only for the current branch and only when that
  branch has a usable upstream. It never touches the index or working tree.
- Auto-fetch never sets the mutation state, so it neither blocks the UI nor interferes with a
  user-initiated Sync, and it is skipped while a mutation or Sync is active.
- Failures are silent and non-blocking. The last known counts remain on screen; only the footer
  freshness text and the sidebar staleness token change.
- A completed auto-fetch requests one ordinary refresh so new counts are published.
- The hook command discovers the repository before doing anything else, so a hook firing on every
  pane focus costs one cheap Git call when the throttle is closed. It always exits zero and prints
  nothing.

#### 5.4.2 Freshness reporting

Silence would otherwise mean both "nothing to pull" and "could not check".

- While the footer status is `Ready` and the layout is not the small size, it is suffixed with
  freshness: `Ready · checked 2m ago`, `Ready · check failed 12m ago`, or
  `Ready · not checked yet`.
- The `fetch` subcommand reports a Herdr workspace metadata token named `sc` through
  `herdr workspace report-metadata <workspace-id> --source herdr-source-control`. It sets
  `sc=stale` when the attempt failed and no fetch has succeeded for at least fifteen minutes, and
  clears the token otherwise. It is skipped when `HERDR_WORKSPACE_ID` is absent, and the TUI timer
  does not report it.
- The token is only visible if the user adds `$sc` to a Herdr sidebar row; the plugin never edits
  Herdr configuration.

### 5.5 Branch state

Display:

- Attached branch: `Branch: <name>`
- Unborn branch: `Branch: <name> (no commits yet)`
- Detached HEAD: `Detached at <8-character-oid>`
- Upstream present: `Upstream: <short-upstream>`
- No upstream: `No upstream`
- Upstream configured but unavailable: `Upstream unavailable`

Ahead/behind text:

- `Up to date`
- `Ahead 1`
- `Ahead N`
- `Behind 1`
- `Behind N`
- `Ahead N, behind M`

Compact rendering uses `+N -M` and Help explains that `+` means ahead/pushable and `-` means
behind/pullable. Do not use arrows as the only indication.

### 5.6 Branch picker

- List local branches only (`refs/heads`).
- Refresh branch data each time the picker opens.
- Include the current branch as the first item, marked `(current)` and disabled.
- Then sort other branches by committer date descending, with branch name as a deterministic
  ascending tie-breaker.
- Mark a branch checked out in another linked worktree as unavailable and show the escaped
  worktree path in its description.
- Search is case-insensitive fuzzy matching against the full branch name.
- Each branch row contains:
  - Branch name and relative commit age
  - Latest commit author, abbreviated object ID, and one-line subject
- Do not show tags, remote-only branches, or a detached-checkout action.
- Create New Branch is always the first action above branch results.
- Selecting the current branch shows `Already on branch "<name>".` and does not run Git.
- Checkout uses no force and no stash. Git decides whether dirty changes can move safely.

### 5.7 Branch creation

- Create from current HEAD only; no custom start point in v1.
- Allow creation from detached HEAD.
- Disable creation before the repository has its first commit. This avoids ambiguous unborn
  branch behavior in v1.
- Input is UTF-8 text and is not trimmed silently. Leading/trailing whitespace is invalid.
- Blank input displays `Enter a branch name.`
- Validation uses `git check-ref-format --branch <name>`.
- Existing branch names display `Branch "<name>" already exists.`
- Other validation failures display `Invalid branch name.` plus sanitized Git detail where
  useful.
- Creation uses the canonical non-recursive switch command defined in Section 8.6.
- Never overwrite an existing branch.

### 5.8 Sync semantics

V1 chooses conservative, deterministic behavior rather than reproducing every configurable
VS Code/Git pull mode.

Preconditions:

- HEAD is attached to a local branch.
- The branch has a usable configured upstream.
- HEAD is not unborn.
- No merge, rebase, cherry-pick, or revert is in progress.
- No checkout, create, or sync operation is active.
- Initial repository status has loaded.

A dirty working tree does not disable Sync. Git decides whether the fast-forward can proceed without
overwriting local work. The plugin never stashes or resets.

Algorithm:

1. Capture current branch name, upstream short/full local ref, upstream remote name, and upstream
   remote ref.
2. Run the canonical no-prune, no-submodule fetch command from Section 8.8. This network-only
   phase is cancellable.
3. Refresh and verify current branch/upstream still equal the captured identities.
4. If both ahead and behind are positive, stop with a divergence message.
5. If behind is positive, run the canonical no-autostash fast-forward command from Section 8.8.
   This local working-tree mutation is not cancellable or hard-timed-out once started.
6. Refresh and verify current branch/upstream identity again.
7. If behind remains nonzero, stop with `Upstream changed during sync.`
8. If ahead is zero, finish successfully without push.
9. If ahead is positive, capture the verified full commit OID and run the canonical exact-ref,
   no-tags, no-submodule push command from Section 8.8. Push is non-cancellable once spawned.
10. Refresh after push and report the final state.

Safety guarantees:

- Never create a merge commit or rebase. A fast-forward update uses `git merge --ff-only`.
- Never force push.
- Never set/change an upstream.
- Never push another local branch due to `push.default` or a concurrent HEAD switch; push uses a
  captured commit OID, not mutable `HEAD`.
- Never create a stash.
- The plugin itself never issues commands to delete files, refs, or lock files. User-configured
  hooks/filters and external processes are outside this guarantee.
- Never proceed to push after fetch/fast-forward failure or identity change.

Clicking Sync while already up to date still performs the fetch. This contacts the remote and
discovers remote work that the cached ahead/behind state could not know about.

### 5.9 Intentional differences from VS Code

| Area | Inspected VS Code behavior | herdr-source-control v1 |
| --- | --- | --- |
| Sync integration | Configurable merge or rebase pull | Fast-forward only |
| Divergence | Pull may merge/rebase | Stop and require external resolution |
| No upstream | Publish branch flow | Sync disabled |
| Dirty sync | Optional autostash | Never stash |
| Tags | Pull tags configurable/defaulted on | Never fetch/push tags |
| Push source | Mutable local branch ref | Verified captured full OID |
| Race checks | No branch/upstream check between pull/push | Verify after every phase |
| Confirmation | Optional confirmation dialog | Explicit button/key is sufficient |
| Auto-fetch | Optional periodic fetch, off by default | Always on; current branch's upstream only |
| Authentication | Integrated Askpass prompts | Strictly non-interactive |
| Cancellation | Optional pull cancellation; push not cancelled | Fetch cancellable; local mutations and push not cancellable |
| Checkout choices | Local, remote, tags, detached | Local branches only |
| Dirty checkout recovery | May offer stash/migrate/force | Refuse without destructive fallback |
| Worktree conflict | Classified after checkout failure | Annotated and disabled before checkout |
| Branch input | Trimmed/sanitized | Exact input validated by Git |
| Unborn creation | Attempted in broader workflows | Disabled in v1 |

These differences are deliberate consequences of a smaller, deterministic, non-destructive
terminal plugin. Do not add VS Code's broader behavior implicitly while fixing an edge case.

## 6. UX Specification

### 6.1 Visual principles

- Preserve Herdr's terminal-native visual language; do not imitate a desktop window.
- Use information density similar to VS Code Source Control.
- Use text, shape, and position for meaning; color is supplementary.
- Use standard Unicode only where a common monospace terminal can render it. The baseline UI
  must remain complete with ASCII.
- Respect `NO_COLOR` and Bubble Tea/Lip Gloss terminal color downsampling.
- Every mouse action has a keyboard equivalent.
- No terminal bell, emoji, animations beyond an ASCII spinner, or private-use icons.

### 6.2 Main wide layout (`>= 96x24`)

```text
 SOURCE CONTROL                                               Refresh
 /home/user/project
────────────────────────────────────────────────────────────────────
 Branch: feature/source-control  origin/feature/source-control  ↑2  ↓0  Sync
────────────────────────────────────────────────────────────────────
 MERGE CHANGES  1  ─────────────────────────────────────────────────
› !  app/models/user.go
 STAGED CHANGES  2  ────────────────────────────────────────────────
  A  internal/git/status.go
  M  README.md
 CHANGES  3  ───────────────────────────────────────────────────────
  M  cmd/herdr-source-control/main.go
  D  docs/old.md
  U  notes.txt
 Tab focus   Enter open   b branch   s sync   r refresh   ? help   q close
 Ready · checked 2m ago                            refresh 2s · fetch 3m
```

### 6.3 Narrow layout (`60-95` columns, `>=18` rows)

```text
 SOURCE CONTROL                               Refresh
 ~/project
────────────────────────────────────────────────────────
 Branch: feature/source-control
 origin/feature/source-control  ↑2 ↓0              Sync
────────────────────────────────────────────────────────
 STAGED CHANGES  2  ────────────────────────────────────
› A  internal/git/status.go
  M  README.md
 CHANGES  3  ───────────────────────────────────────────
  M  cmd/herdr-source-control/main.go
  D  docs/old.md
  U  notes.txt
 b branch   s sync   r refresh   ? help
 Ready · checked 2m ago  refresh 2s · fetch 3m
```

### 6.4 Small layout (`40-59` columns or `12-17` rows)

```text
 SOURCE CONTROL                      R
 ~/project
 Branch: feature/source-control
 ↑2 ↓0                               S
────────────────────────────────────────
 STAGED  2  ────────────────────────────
› A  internal/git/status.go
  M  README.md
 CHANGES  3  ───────────────────────────
  M  cmd/herdr-source.../main.go
 b branch   ? help   q close
 Ready
```

Below `40x12`, render only:

```text
 Source Control

 Terminal too small
 Resize to at least 40x12.

 q: close
```

### 6.5 Responsive rules

- Contract `$HOME` to `~` in the displayed repository root.
- Preserve full paths in model state; truncate only at rendering.
- Truncate paths in the middle, preserving the first component and filename.
- Use Lip Gloss cell-width measurement; never slice by bytes or runes alone.
- Never let body content overwrite the final Help and Status rows.
- Group headings remain visible as ordinary list rows; they are not selectable.
- Selection remains in view when navigating or resizing.
- Status codes and paths remain visible before secondary metadata.
- The branch name may truncate in the middle but must never overwrite Sync.

### 6.6 Focus order

Main screen:

1. Branch control
2. Sync control
3. Refresh control
4. Changed-file list, when nonempty
5. Return to Branch

Initial focus:

- Changed-file list when files exist
- Branch control when the working tree is clean
- Refresh in the No Repository or load-error state

Focused controls are distinguishable without color through reverse video. A selected row begins
with `›` and receives a full-row background highlight.

### 6.7 Main keymap

| Key | Action |
| --- | --- |
| `Tab` / `Shift+Tab` | Move focus forward/backward |
| `Up`, `k` | Previous selectable file |
| `Down`, `j` | Next selectable file |
| `PageUp`, `Ctrl+u` | Scroll one half/full viewport up |
| `PageDown`, `Ctrl+d` | Scroll one half/full viewport down |
| `Home`, `g` | First selectable file |
| `End`, `G` | Last selectable file |
| `Enter`, `Space` | Activate focused control/row |
| `b` | Open branch picker |
| `s` | Sync when enabled |
| `r` | Refresh |
| `?` | Open Help |
| `Esc` | Clear transient feedback; otherwise focus file list |
| `q`, `Ctrl+c` | Close the plugin tab when no text input is active |

While a text input is focused, printable global shortcuts do not fire. `Esc` cancels the modal.

### 6.8 Mouse behavior

| Target/event | Behavior |
| --- | --- |
| Branch control click | Open picker |
| Sync click | Start sync when enabled; otherwise explain why |
| Refresh click | Request refresh |
| File row click | Select row |
| File row double-click | Show future-diff notice |
| Wheel over file list | Scroll list |
| Branch row click | Select |
| Branch row double-click | Checkout if enabled |
| Modal button click | Activate |
| Click outside modal | No action |

Implement click targets as explicit rectangles calculated during render. Do not add a mouse-zone
dependency until the number of targets proves that manual rectangles are unmaintainable.

### 6.9 Branch picker layout

```text
+------------------------- Switch Branch --------------------------+
| Search: feature/auth_                                             |
|                                                                   |
| > + Create new branch...                                          |
|                                                                   |
|   feature/auth                                      2 hours ago   |
|   Mario T.  a1b2c3d  Add authorization checks                     |
|                                                                   |
|   main                                             3 days ago     |
|   Phil R.   e4f5a6b  Merge pull request #123                      |
|                                                                   |
| 2 branches                                             [Cancel]   |
+-------------------------------------------------------------------+
```

- Modal width: `min(76, terminal width - 4)`
- Modal height: `min(22, terminal height - 4)`
- Search focused on open
- Bubbles list supplies fuzzy filtering and pagination
- Custom item delegate renders two-line branch rows
- Current branch: append `(current)` and disable activation
- Other-worktree branch: append `(in <escaped-path>)` and disable activation
- `Enter`: activate selected action/branch
- `Up/Down`, `Ctrl+p/Ctrl+n`: navigate
- `PageUp/PageDown`: page
- `n`: open Create Branch when search input is empty; otherwise input receives `n`
- `Esc`: cancel

### 6.10 Create Branch modal

```text
+------------------------- Create Branch --------------------------+
| New branch name                                                   |
| feature/source-control_                                           |
|                                                                   |
| Creates from current HEAD and switches to the branch.             |
|                                                                   |
| <validation or Git error>                                         |
|                                      [Create] [Cancel]             |
+-------------------------------------------------------------------+
```

- Input focused on open
- `Enter`: validate and submit
- `Esc`: return to branch picker
- `Tab`/`Shift+Tab`: cycle input, Create, Cancel
- Failure keeps modal and text intact

### 6.11 Help modal

Help states that the file list is read-only and Sync uses fetch, fast-forward-only update, and
non-force push. It lists the context-appropriate keymap. Help scrolls when the terminal is too
short.

### 6.12 Loading, empty, operation, and error states

Initial: `Loading repository...`

Clean:

```text
Working tree clean
No staged, modified, conflicting, or untracked files.
```

No repository:

```text
No Git repository found
Source Control could not find a working tree from:
<path>

Initialize or open a repository, then refresh.
[Refresh]
```

Git unavailable:

```text
Git is not available
Install Git and ensure "git" is on PATH, then refresh.
[Refresh]
```

Operations use ASCII spinner frames `|`, `/`, `-`, `\` at no more than ten frames/second.
Status priority is:

1. Blocking error
2. Active operation
3. Disabled-control explanation
4. Most recent success/information
5. `Ready`

Success remains five seconds; information remains three seconds; errors remain until the next
user action or successful refresh. `Ready` alone carries the remote-freshness suffix defined in
Section 5.4.2; no other status text is suffixed.

## 7. Technical Architecture

### 7.1 Project structure

```text
herdr-source-control/
├── cmd/
│   └── herdr-source-control/
│       └── main.go
├── internal/
│   ├── app/
│   │   ├── commands.go
│   │   ├── keymap.go
│   │   ├── messages.go
│   │   ├── model.go
│   │   ├── update.go
│   │   ├── view.go
│   │   ├── update_test.go
│   │   └── view_test.go
│   ├── domain/
│   │   └── repository.go
│   ├── git/
│   │   ├── branches.go
│   │   ├── command.go
│   │   ├── discovery.go
│   │   ├── errors.go
│   │   ├── porcelain.go
│   │   ├── repository.go
│   │   ├── sync.go
│   │   └── *_test.go
│   ├── herdr/
│   │   ├── client.go
│   │   ├── context.go
│   │   ├── launcher.go
│   │   └── *_test.go
│   ├── logging/
│   │   └── logging.go
│   ├── state/
│   │   ├── fetch.go
│   │   └── fetch_test.go
│   └── ui/
│       ├── branches.go
│       ├── changes.go
│       ├── layout.go
│       ├── sanitize.go
│       ├── styles.go
│       └── *_test.go
├── scripts/
│   ├── build.sh
│   └── install-release.sh
├── bin/
│   └── .gitkeep
├── .github/workflows/ci.yml
├── .github/workflows/release.yml
├── herdr-plugin.toml
├── go.mod
├── go.sum
├── LICENSE
├── PLAN.md
├── README.md
└── VERSION
```

Do not split packages further without repeated ownership pressure. Keep app state transitions in
one `app` package and Git behavior in one `git` package.

### 7.2 Dependencies

Pin exact compatible releases at implementation start:

```go
module github.com/mariotmc/herdr-source-control

go 1.25.0

require (
    charm.land/bubbles/v2 v2.1.1
    charm.land/bubbletea/v2 v2.0.8
    charm.land/lipgloss/v2 v2.0.5
)
```

The versions above were current stable releases when this plan was written. Use their v2 module
paths, not legacy `github.com/charmbracelet/...` paths. Upgrade intentionally, with tests.

Use only the standard library beyond these dependencies. Do not add a Git library, logging
framework, filesystem watcher, or mouse-zone library in v1.

### 7.3 Executable modes

One binary has three subcommands:

```text
herdr-source-control open
herdr-source-control tui
herdr-source-control fetch
```

- `open`: short-lived Herdr action launcher; stdout/stderr are captured by Herdr plugin logs.
- `tui`: long-lived Bubble Tea application running inside the plugin pane.
- `fetch`: short-lived background fetch invoked from Herdr event hooks. It writes nothing to
  stdout/stderr, suppresses the logging warning, and always exits zero.

Unknown/missing subcommands print usage to stderr and exit 2.

### 7.4 Domain model

```go
type Status uint8

const (
    StatusUnmodified Status = iota
    StatusModified
    StatusAdded
    StatusDeleted
    StatusRenamed
    StatusCopied
    StatusTypeChanged
    StatusUnmerged
    StatusUnknown
)

type Change struct {
    Path          []byte
    OriginalPath  []byte
    IndexStatus   Status
    WorktreeStatus Status
    RawXY         [2]byte
    Submodule     string
    Score         int
    Untracked     bool
    Conflicted    bool
}

type HeadState uint8

const (
    HeadAttached HeadState = iota
    HeadDetached
    HeadUnborn
)

type BranchState struct {
    State       HeadState
    Name        string
    OID         string
    Upstream    string
    UpstreamRef string
    RemoteName  string
    RemoteRef   string
    Ahead       uint64
    Behind      uint64
    CountsKnown bool
}

type OperationState struct {
    Merge       bool
    Rebase      bool
    CherryPick  bool
    Revert      bool
}

type Snapshot struct {
    Root        string
    CanonicalRoot string
    GitDir      string
    CommonDir   string
    Branch      BranchState
    Operation   OperationState
    Changes     []Change
    CapturedAt  time.Time
}

type LocalBranch struct {
    Name          string
    OID           string
    Subject       string
    Author        string
    CommitTime    time.Time
    Current       bool
    WorktreePath  string
}
```

Use raw bytes for Git paths. A Go string is acceptable for Git ref names because ref names are
valid byte sequences constrained by Git and branch input is UTF-8, but never shell-interpolate
them.

### 7.5 Git repository interface

```go
type Repository interface {
    Snapshot(context.Context) (domain.Snapshot, error)
    LocalBranches(context.Context) ([]domain.LocalBranch, error)
    ValidateBranch(context.Context, string) error
    Checkout(context.Context, string) error
    CreateBranch(context.Context, string) error
    Fetch(context.Context, domain.BranchState) error
    FastForward(context.Context, domain.BranchState) error
    Push(context.Context, domain.BranchState, string) error
}
```

The TUI depends on this interface. Tests supply fakes; production uses a CLI implementation.

### 7.6 Bubble Tea model

Bubble Tea v2 requires `View() tea.View`, not `View() string`.

```go
type Mode uint8

const (
    ModeMain Mode = iota
    ModeBranches
    ModeCreateBranch
    ModeHelp
)

type Operation uint8

const (
    OperationNone Operation = iota
    OperationCheckout
    OperationCreateBranch
    OperationSyncFetch
    OperationSyncFastForward
    OperationSyncPush
)

type RefreshReason uint8

const (
    RefreshStartup RefreshReason = 1 << iota
    RefreshPoll
    RefreshFocus
    RefreshManual
    RefreshMutation
)

type Model struct {
    ctx       context.Context
    repo      git.Repository
    logger    *slog.Logger

    width     int
    height    int
    mode      Mode
    focus     FocusTarget

    snapshot       *domain.Snapshot
    selected       ChangeIdentity
    scrollOffset   int
    flattenedRows  []ui.ChangeRow

    branchList     list.Model
    branchInput    textinput.Model
    spinner        spinner.Model
    helpViewport   viewport.Model
    styles         ui.Styles

    nextRequestID  uint64
    refreshID      uint64
    refreshBusy    bool
    refreshCancel  context.CancelFunc
    queuedRefreshReasons RefreshReason
    branchLoadID   uint64
    mutationID     uint64
    mutation       Operation
    syncState      *SyncState
    repositoryEpoch uint64

    stale          bool
    status         StatusMessage
    lastError      error
}
```

Keep the selected row identity as group plus raw path, not a numeric index. Rebuild flattened
rows after each snapshot. Restore exact identity when present; otherwise scan backward through
the previous selectable identities for the first still present, then scan forward, then apply the
normal clean/empty focus rule.

### 7.7 Message types

```go
type pollTickMsg time.Time
type autoFetchTickMsg time.Time
type noticeExpiredMsg struct { ID uint64 }

type autoFetchFinishedMsg struct {
    ID      uint64
    Record  state.Record
    Skipped bool
    Err     error
}

type snapshotLoadedMsg struct {
    RequestID uint64
    Epoch     uint64
    Snapshot  domain.Snapshot
    Err       error
}

type branchesLoadedMsg struct {
    RequestID uint64
    Branches  []domain.LocalBranch
    Err       error
}

type branchValidatedMsg struct {
    RequestID uint64
    Name      string
    Err       error
}

type mutationFinishedMsg struct {
    ID        uint64
    Operation Operation
    Detail    any
    Err       error
}

type SyncState struct {
    ID            uint64
    Phase         Operation
    Branch        string
    Upstream      string
    UpstreamRef   string
    RemoteName    string
    RemoteRef     string
    CommitOID     string
}
```

Only `Update` mutates model state. Bubble Tea commands capture immutable input and return typed
messages. Do not start unmanaged goroutines.

Sync is explicitly orchestrated by `Update`, not by a monolithic repository call:

1. `Update` starts `Fetch` and records `OperationSyncFetch`.
2. Its matching `mutationFinishedMsg` schedules a snapshot with a sync continuation marker.
3. The matching snapshot result validates branch/upstream and either finishes, schedules
   `FastForward`, or reports divergence.
4. Fast-forward completion schedules another snapshot.
5. That snapshot validates identity/counts, captures its full OID, and either finishes or
   schedules `Push`.
6. Push completion schedules the final snapshot and feedback.

Each phase uses the same sync ID; any mismatched message or snapshot epoch is ignored. This
preserves phase-specific UI feedback and keeps every state transition inside `Update`.

### 7.8 Refresh and stale-result rules

- At most one snapshot request is non-retired and eligible to publish. A cancelled safe-read
  process may spend up to the two-second termination grace period winding down while its retired
  request is already stale; it cannot publish or alter current bookkeeping. Brief overlap between
  that read-only teardown and a mutation/replacement read is permitted.
- Every request receives a monotonically increasing ID and captures `repositoryEpoch`.
- Build root metadata, branch state, operation state, and changes in command-local values. Publish
  nothing until the complete snapshot succeeds.
- Apply only the message matching active request ID and current epoch, replacing the snapshot and
  rebuilding rows atomically.
- An ignored late result must not clear `refreshBusy`, stale state, queued reasons, or a newer
  error.
- Starting a mutation increments `repositoryEpoch`, invokes `refreshCancel`, retires that active
  request by clearing `refreshCancel`/`refreshBusy`, advances the active request ID, and queues
  `RefreshMutation`. Do not wait for a slow `/mnt/c` status read. A later cancelled-result message
  is stale and cannot affect bookkeeping for a newer request.
- Poll ticks always schedule the next tick, even when this tick cannot refresh.
- Poll while refresh/mutation is active is dropped.
- Manual, focus, or mutation refresh while busy merges its reason into
  `queuedRefreshReasons`; there is still at most one trailing refresh.
- Manual semantics survive coalescing: that trailing refresh retries discovery and clears old
  transient feedback.
- Only an actual unfocused-to-focused transition contributes `RefreshFocus`.
- Mutation completion always merges `RefreshMutation` and starts the queued refresh when safe.
- On either refresh success or failure, service accumulated reasons after applying/retaining the
  last-good snapshot.
- Poll ticks that arrive while the pane is unfocused schedule the next tick and nothing else.
- Branch list messages apply only while the branch modal remains open and IDs match.
- Mutation results apply only when IDs match.
- Auto-fetch results apply only when the fetch ID matches. A throttled result updates freshness
  bookkeeping only; a real attempt also requests one refresh so new counts are published.
- Closing the app cancels the root context and all cancellable read/network command contexts.
  Plugin quit is disabled while any spawned non-cancellable operation is active, explicitly
  including checkout, create, local fast-forward, and push.
- Never optimistically alter branch, status, or ahead/behind before Git confirms it.

### 7.9 Fixed runtime limits

V1 has no user configuration file. Use named constants:

| Limit | Value |
| --- | --- |
| Poll interval | 2 seconds |
| Auto-fetch interval and shared throttle | 3 minutes |
| Staleness threshold for the `sc` sidebar token | 15 minutes |
| Version/discovery timeout | 5 seconds |
| Status/branch-list timeout | 30 seconds |
| Fetch timeout | 5 minutes |
| Status success message | 5 seconds |
| Informational message | 3 seconds |
| Displayed stderr cap | 8 KiB |

Checkout, create-branch, local fast-forward, and push commands have no hard timeout and are not
cancellable after launch because killing a process while it updates the index/worktree can leave
partial state or make a remote push outcome unknowable. While one runs, show the operation and disable plugin quit; the user can still
terminate the outer Herdr pane, which is explicitly outside guarantees the plugin can enforce.

Do not add color, keybinding, sync-policy, timeout, or layout configuration until requested.

### 7.10 Terminal view contract

Every normal `tea.View` returned by `View` must set:

```go
view.AltScreen = true
view.ReportFocus = true
view.MouseMode = tea.MouseModeCellMotion
```

Set `view.OnMouse` to a closure capturing the rectangles calculated for that exact rendered
frame. It returns typed commands/messages handled by `Update`; rendering never mutates model
state. If Herdr or the outer terminal does not forward focus/mouse events, polling and keyboard
controls remain fully functional.

### 7.11 Logging

Use standard-library `log/slog`. Append to:

```text
$HERDR_PLUGIN_STATE_DIR/herdr-source-control.log
```

Create parent directories and file with user-only permissions (`0700` directory, `0600` file).
If file logging fails, continue with `io.Discard` and show one nonfatal warning.

Log:

- Version, startup mode, and repository root
- Refresh duration and change count at debug
- Mutation operation, duration, and outcome
- Parser errors and Herdr launcher errors

Never log:

- Complete environment
- Credential helper output
- Tokens or passwords
- Remote URLs containing user info
- File contents or diffs

## 8. Git CLI Contract

### 8.1 Baseline and discovery

Require Git 2.31 or newer. On startup parse `git --version`; reject older versions with an
actionable message.

Discovery commands, run independently:

```text
git rev-parse --is-inside-work-tree
git rev-parse --is-bare-repository
git rev-parse --path-format=absolute --show-toplevel
git rev-parse --path-format=absolute --git-dir
git rev-parse --path-format=absolute --git-common-dir
```

Remove exactly one trailing LF or CRLF. Do not call `strings.TrimSpace` on paths. First classify
`--is-bare-repository == true` as Bare Repository. Otherwise require
`--is-inside-work-tree == true`; a false/error result is No Repository. Do not assume `.git` is
a directory.

After discovery, attempt `filepath.EvalSymlinks(Root)`:

- `Root` remains Git's selected path used for display and `Cmd.Dir`.
- `CanonicalRoot` is the evaluated path when successful, otherwise `Root`.
- Repository/tab identity hashes `CanonicalRoot`, preventing symlink and real-path openings of
  the same worktree from creating duplicate tabs.

### 8.2 Subprocess policy

- Invoke Git directly; never use a shell.
- Use repository root as `Cmd.Dir` after discovery.
- stdin is closed/null for every Git process.
- Capture stdout/stderr separately.
- Preserve inherited environment except explicit overrides.
- Start each child in a new process session with no controlling terminal (`Setsid: true`) so SSH
  and credential tools cannot open `/dev/tty` and consume TUI input.
- Add `LC_ALL=C`, `LANG=C`, `LANGUAGE=C`, `GIT_PAGER=cat`, `GIT_TERMINAL_PROMPT=0`,
  `GCM_INTERACTIVE=Never`, `GIT_ASKPASS=/bin/false`, `SSH_ASKPASS=/bin/false`, and
  `SSH_ASKPASS_REQUIRE=never`, overriding inherited values.
- Add `GIT_OPTIONAL_LOCKS=0` only to read operations, not fetch/push/merge/switch/create.
- Sanitize stderr before terminal rendering and cap displayed detail at 8 KiB.
- Redact credential-bearing URLs in logs/errors.
- Read operations and network-only fetch use context timeouts. On their timeout/cancellation,
  terminate the process session: SIGTERM, two-second grace, then SIGKILL.
- Checkout, create, local fast-forward, and push do not use `exec.CommandContext`, hard timeouts,
  or cancellation once started. Wait for completion, then refresh. Push may be cancelled before
  launch but never after spawn because the remote outcome could become unknowable.
- Never remove Git lock files.

Existing non-interactive credential helpers and SSH agents remain supported. Interactive HTTPS
credentials, SSH passphrases, and unknown-host confirmation must be completed outside Herdr.
User-defined credential helpers/transports may have side effects outside the plugin's no-prompt
guarantee.

### 8.3 Status command

```text
git status --porcelain=v2 --branch -z --untracked-files=all --renames --ahead-behind
```

Parse stdout as bytes split on NUL. Recognize:

- `# branch.oid`
- `# branch.head`
- `# branch.upstream`
- `# branch.ab +N -M`
- `1` ordinary changed entry
- `2` rename/copy entry followed by original path as the next NUL field
- `u` unmerged entry
- `?` untracked entry
- `!` ignored entry (ignore defensively, although not requested)

Unknown record types are parser errors. Preserve the last valid snapshot rather than displaying
incomplete state.

Framing requirements adapted from VS Code's parser tests:

- Successful output must end on a complete NUL-delimited record; reject an unterminated trailing
  field.
- A type-2 rename/copy record must have exactly one following original-path field.
- Validate the count and syntax of fixed fields before indexing them.
- Unknown record type invalidates the whole snapshot; unknown future XY status code becomes
  `StatusUnknown` without losing the path.
- Accumulate command-local records and publish none if a later framing/parse error occurs.
- Capturing complete stdout means a streaming parser is unnecessary, but tests must split fixture
  construction at every field boundary to preserve the same framing discipline.

Ordinary entry format:

```text
1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>
```

- `X` is index state; `Y` is worktree state; `.` is unchanged.
- Parse fixed ASCII fields, then treat the remainder as raw path bytes.

Rename/copy format:

```text
2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <R-or-C><score> <new-path>\0<old-path>\0
```

Unmerged format:

```text
u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>
```

Untracked format:

```text
? <path>
```

Parse ahead/behind as nonnegative uint64 and reject overflow.

### 8.4 Operation-in-progress detection

Use discovered worktree-specific Git dir and test for Git's documented state files/directories:

- Merge: `MERGE_HEAD`
- Rebase: `rebase-merge` or `rebase-apply`
- Cherry-pick: `CHERRY_PICK_HEAD`
- Revert: `REVERT_HEAD`

These checks inform display and Sync preconditions only. Never modify these files.

### 8.5 Branch list command

Use `git for-each-ref` with NUL-separated fields and an unambiguous record terminator. The
implementation must construct and test a format equivalent to:

```text
%(refname:strip=2)%00%(HEAD)%00%(worktreepath)%00%(objectname)%00%(authorname)%00%(committerdate:unix)%00%(subject)%00%00
```

Target `refs/heads/`, sorted by `-committerdate`, then `refname`. Git appends one LF after each
formatted record, so frame each record as seven NUL-terminated fields followed by a second NUL
and exactly one LF (`\0\0\n`). Parse that terminator explicitly; neither a newline in a worktree
path nor display metadata can become a record boundary. Do not parse human-oriented
`git branch` output.

Subject/author are display data and must be terminal-sanitized. A nonempty `worktreepath` means
the branch is checked out in a worktree; current branch is identified by `HEAD == "*"`.

### 8.6 Checkout and create

Checkout:

```text
git switch --no-guess --no-recurse-submodules -- <branch>
```

Only allow a branch returned by the current branch-list snapshot. Pass the branch as one argv
element. Revalidate disabled/current/worktree state immediately before launch; Git remains
authoritative against races after validation.

Validate creation:

```text
git check-ref-format --branch <name>
```

Create:

```text
git switch --no-recurse-submodules -c <name> --
```

### 8.7 Resolve exact upstream

For the current local branch, query:

```text
git for-each-ref \
  --format=%(upstream)%00%(upstream:short)%00%(upstream:remotename)%00%(upstream:remoteref)%00%00 \
  refs/heads/<branch>
```

Parse the double-NUL-plus-LF framing as in branch enumeration. Capture:

- `%(upstream)` as the full local tracking ref (`UpstreamRef`)
- `%(upstream:short)` as display/identity `Upstream`
- `%(upstream:remotename)` as `RemoteName`
- `%(upstream:remoteref)` as the full remote source ref (`RemoteRef`)

Empty values mean no usable upstream. Never derive refs by splitting the short upstream name.
For remote Sync require `UpstreamRef` to begin with `refs/remotes/`, `RemoteRef` to begin with
`refs/heads/`, and remote name not equal to `.`. Local-branch upstreams (`remote=.`) are
unsupported in v1. Reject remote names beginning with `-`, containing NUL, or failing
`git check-ref-format refs/remotes/<remote>/probe`; this prevents option injection on commands
where remote position is parsed specially.

Run this query as part of every attached-branch snapshot and combine it with status output before
atomic publication. For a configured upstream:

1. Require the query's upstream short name to equal `branch.upstream` from status.
2. Compute authoritative counts against that exact full local ref with
   `git rev-list --left-right --count HEAD...<upstream-full-ref>`; use its left count as Ahead and
   right count as Behind instead of publishing provisional `branch.ab` counts.
3. Repeat the exact-upstream query and require the full tuple (full ref, short ref, remote name,
   remote ref) to remain identical.
4. Verify branch name with `git symbolic-ref --quiet --short HEAD` and committed OID with
   `git rev-parse --verify HEAD` (with expected handling for unborn HEAD).

If any identity differs, reject the snapshot as inconsistent and queue a replacement rather than
publishing branch metadata/counts assembled from different repository states.

Before Sync, run `git config --bool --get remote.<remote-name>.mirror`. Exit 1 with no output
means unset/false. If the value is true, reject Sync with `Sync is unavailable for mirror
remotes.` Git's remote mirror configuration can broaden or reject an otherwise explicit push and
cannot be safely overridden by the planned command.

### 8.8 Fetch, fast-forward, and push

Fetch:

```text
git fetch --no-tags --no-prune --no-prune-tags --no-recurse-submodules -- <remote-name> +<remote-ref>:<upstream-full-ref>
```

Fast-forward:

```text
git merge --ff-only --no-autostash -- <upstream-full-ref>
```

Push:

```text
git push --porcelain --no-follow-tags --recurse-submodules=no -- <remote-name> <captured-full-oid>:<remote-ref>
```

Use exit status as success. Human output is diagnostic only. Verify the captured OID is the full
hex OID from the post-fast-forward snapshot before using it in the refspec. Do not use aliases,
force flags, `--follow-tags`, `--set-upstream`, or broad refspecs.

These explicit negative flags override side-effectful user configuration such as fetch pruning,
merge autostash, push tag/submodule behavior, and switch submodule recursion. Mirror remotes are
rejected before Sync. The plugin still respects transport, credential, URL rewrite, and ordinary
repository configuration that does not broaden the requested mutation.

The forced fetch refspec is deliberate and limited to the one configured tracking ref. It ensures
the local upstream ref advances even when `remote.<name>.fetch` is missing/custom and permits a
legitimate remote force-update to be represented locally. It must not update or prune unrelated
remote-tracking refs.

## 9. Path and Terminal Safety

- Linux Git paths are arbitrary non-NUL bytes and may not be UTF-8.
- Keep current and original paths as `[]byte` identity values.
- Display valid printable UTF-8 normally.
- Escape backslash as `\\`.
- Escape tab, LF, CR, ESC, C0 controls, DEL, and invalid UTF-8 bytes as `\xNN`.
- Never print raw Git output into the terminal.
- Use escaped display strings for branch/file search, while preserving raw identity.
- Never pass a displayed/escaped path back to Git.
- V1 does not mutate paths, but preserving raw paths is required for future diff/staging safety.
- Branch names and arguments always occupy one argv element and are preceded by command-specific
  option termination where supported. No shell interpolation means branch names cannot inject
  commands.

## 10. Error Model

Define typed errors:

```go
type ErrorKind uint8

const (
    ErrorGitUnavailable ErrorKind = iota
    ErrorGitTooOld
    ErrorNotRepository
    ErrorBareRepository
    ErrorTimeout
    ErrorCancelled
    ErrorNoUpstream
    ErrorValidation
    ErrorBusy
    ErrorAuthentication
    ErrorNetwork
    ErrorDirtyCheckout
    ErrorWorktreeConflict
    ErrorRejectedPush
    ErrorParse
    ErrorHerdr
    ErrorCommand
)

type OperationError struct {
    Kind      ErrorKind
    Operation string
    Phase     string
    ExitCode  int
    Stderr    string
    Err       error
}
```

Use structural preconditions before stderr classification. Since locale is fixed to C, classify
known Git failures only when needed for better user guidance. Unknown failures remain generic
Git errors.

Required user-facing outcomes:

| Condition | Message/behavior |
| --- | --- |
| Missing Git | `Git is not installed or not in PATH.` |
| Unsafe repository | Preserve Git's sanitized `safe.directory` guidance; do not execute it |
| Repository/index lock | `Repository is busy. Finish the other Git operation and refresh.` |
| Checkout would overwrite changes | Explain that local changes block checkout; never force |
| Branch used by worktree | Show occupying worktree path |
| Invalid/existing branch | Keep create modal open |
| No upstream | Disable Sync and explain |
| Authentication failure | `Configure Git credentials outside Herdr, then retry.` |
| Network failure | `Could not reach the remote. Check your connection and retry.` |
| Push rejection | Refresh; never force |
| Sync divergence | Explain fast-forward-only refusal and require external resolution |
| Timeout/cancel | Mark snapshot stale and refresh |
| Parse failure | Keep previous snapshot and report unsupported/malformed Git output |

## 11. Herdr Integration

### 11.1 Manifest

```toml
id = "herdr-source-control"
name = "Source Control"
version = "0.2.0"
min_herdr_version = "0.7.5"
description = "Lightweight Git status, branch switching, and sync in a dedicated Herdr tab."
platforms = ["linux"]

[[build]]
command = ["sh", "scripts/install-release.sh"]

[[panes]]
id = "source-control"
title = "Source Control"
placement = "tab"
command = ["sh", "-c", "exec \"$HERDR_PLUGIN_ROOT/bin/herdr-source-control\" tui"]

[[actions]]
id = "open"
title = "Open Source Control"
description = "Open or focus Source Control for the current repository."
contexts = ["workspace", "pane"]
command = ["./bin/herdr-source-control", "open"]

[[events]]
on = "workspace.focused"
command = ["sh", "-c", "exec \"$HERDR_PLUGIN_ROOT/bin/herdr-source-control\" fetch"]

[[events]]
on = "pane.focused"
command = ["sh", "-c", "exec \"$HERDR_PLUGIN_ROOT/bin/herdr-source-control\" fetch"]
```

No `[[startup]]` section. The two event hooks only run the throttled background fetch from
Section 5.4.1. They never open, focus, close, or rename a pane or tab.

### 11.2 Recommended keybinding

Document, but do not automatically edit, the user's Herdr configuration:

```toml
[[keys.command]]
key = "prefix+shift+s"
type = "plugin_action"
command = "herdr-source-control.open"
description = "open source control"
```

The installer must not overwrite or append user keybindings. If the key conflicts, the user can
choose another binding.

### 11.3 Launcher algorithm

The `open` subcommand:

1. Parse `HERDR_PLUGIN_CONTEXT_JSON` using explicit structs with optional fields.
2. Require a workspace ID. If absent, print an actionable error and exit 1.
3. Resolve invoking cwd and repository/cwd identity, including `CanonicalRoot` from discovery.
   For a non-repository cwd, evaluate symlinks on its absolute path with the same fallback rule.
4. Compute a stable identity value as SHA-256 of canonical root/cwd while preserving Git's `Root`
   for display, pane cwd, and Git execution.
5. Acquire an exclusive Linux `flock` on
   `$HERDR_PLUGIN_STATE_DIR/open.lock` to serialize duplicate action invocations.
6. Run `HERDR_BIN_PATH pane list --workspace <id>` and decode JSON.
7. Find a pane whose metadata token `herdr-source-control` equals the identity hash.
8. For a match, run `HERDR_BIN_PATH pane process-info --pane <pane-id>` and classify liveness:
   - Live: a foreground process has executable basename `herdr-source-control` and, when argv is
     available, contains `tui`.
   - Definitely exited: process-info succeeds and reports foreground process data that clearly
     contains only the replacement shell/another command, not the plugin binary.
   - Indeterminate: process-info fails, omits optional process/argv/cmdline data, or returns a
     transiently empty snapshot.
9. If live, run `HERDR_BIN_PATH plugin pane focus <pane-id>`, close restored duplicates as defined
   in Section 11.3.1, and exit success.
10. If definitely exited, close that plugin pane and take a fresh pane snapshot before opening a
    replacement.
11. If indeterminate, focus the identity-matched plugin pane, close restored duplicates, and
    return success. Never close a pane solely because process inspection is unavailable.
12. If no identity-matched pane was focused, attempt to reclaim a restored pane as defined in
    Section 11.3.1. On success, close any other restored duplicate and return success.
13. Otherwise run:

```text
HERDR_BIN_PATH plugin pane open
  --plugin herdr-source-control
  --entrypoint source-control
  --placement tab
  --workspace <workspace-id>
  --cwd <root-or-cwd>
  --env HERDR_SOURCE_CONTROL_ROOT=<root-or-cwd>
  --env HERDR_SOURCE_CONTROL_ID=<identity-hash>
  --focus
```

14. Decode the response and obtain pane/tab IDs.
15. Before releasing the lock, establish identity atomically from the launcher's perspective:

```text
HERDR_BIN_PATH pane report-metadata
  --source herdr-source-control
  --token herdr-source-control=<identity-hash>
  <pane-id>
```

16. If metadata reporting fails, close the just-opened plugin pane and return an error rather
    than leaving an unidentifiable duplicate candidate.
17. Run `HERDR_BIN_PATH tab rename <tab-id> Source Control`.
18. Release the lock.

Use `HERDR_BIN_PATH`, falling back to `herdr`. Invoke directly through argv. Treat malformed Herdr
JSON as an error, not as pane absence.

#### 11.3.1 Restored-pane reclaim

When the Herdr server restarts, snapshot restore brings the tab back as a plain shell. Herdr
persists only a pane's cwd and label; the identity token is display-only and is not persisted, so
such a pane cannot be matched by token and would otherwise cause a second Source Control tab, one
blank and one working.

Reclaim restarts the plugin inside the restored pane in place:

```text
HERDR_BIN_PATH pane run <pane-id> env \
  HERDR_SOURCE_CONTROL_ROOT=<root> \
  HERDR_SOURCE_CONTROL_ID=<identity-hash> \
  HERDR_PANE_ID=<pane-id> \
  <plugin-binary> tui
```

It then reports identity, renames the tab, and focuses it with `tab focus`. A reclaimed pane is an
ordinary pane rather than a plugin-owned one, so `plugin pane focus` does not apply to it.

Once a live pane is in use, every other restored pane for the same repository is closed with
`pane close`. Cleanup is best effort: failing to tidy up must never fail the open.

A pane qualifies as restored only when all of the following hold:

- Its label is exactly `Source Control`.
- Its cwd equals the repository root, or the canonical root when discovery evaluated a symlink.
- It carries no `herdr-source-control` metadata token.
- `pane process-info` succeeds and every foreground process is a bare shell.

Anything that cannot be positively identified as a shell counts as in use by the user, and an
absent or empty process list never qualifies. These conditions exist so reclaim and cleanup can
never take over or close a pane the user is working in.

### 11.4 TUI identity

At startup, after `HERDR_PANE_ID` is available, refresh metadata through `HERDR_BIN_PATH`:

```text
herdr pane report-metadata --source herdr-source-control --token herdr-source-control=<identity> <pane-id>
```

Metadata failure here is nonfatal because the launcher already established it. No heartbeat is
required because the event hooks run a separate short-lived process and do not depend on pane
liveness. Restored dead plugin panes are detected by `pane process-info` on the next explicit Open
action, closed, and replaced; panes that snapshot restore left as bare shells are reclaimed in
place instead, as defined in Section 11.3.1.

### 11.5 Quit behavior

- `q` or `Ctrl+c` cancels cancellable reads/fetch and returns `tea.Quit` when no non-cancellable
  operation is active. During checkout/create/fast-forward/push, show
  `Wait for the Git operation to finish.` and remain open.
- Bubble Tea restores terminal modes.
- The plugin process exits zero.
- Herdr closes the plugin-owned pane/tab according to normal pane-process lifecycle.
- Do not invoke `pane.close` from inside the exiting process unless live testing proves Herdr
  leaves an empty pane. If it does, add a best-effort close only after terminal restoration.

## 12. Packaging and Distribution

### 12.1 Local development

`scripts/build.sh`:

```sh
#!/bin/sh
set -eu
mkdir -p bin
exec go build -trimpath -o bin/herdr-source-control ./cmd/herdr-source-control
```

Development flow:

```text
./scripts/build.sh
herdr plugin link .
herdr plugin action invoke herdr-source-control.open
```

`plugin link` does not run build commands.

### 12.2 Release artifacts

Release Linux binaries for:

- `linux-amd64`
- `linux-arm64`

Build with:

```text
CGO_ENABLED=0 GOOS=linux GOARCH=<arch> go build -trimpath \
  -ldflags="-s -w -X main.version=<version> -X main.commit=<sha>" \
  -o herdr-source-control ./cmd/herdr-source-control
```

For version `0.1.0`, package each binary at the archive root and name assets exactly:

```text
herdr-source-control_0.1.0_linux_amd64.tar.gz
herdr-source-control_0.1.0_linux_arm64.tar.gz
checksums.txt
```

`checksums.txt` uses standard sha256sum format: lowercase digest, two spaces, filename.

### 12.3 Install script

`scripts/install-release.sh` runs during `herdr plugin install`:

1. Read the exact version from repository-root `VERSION`, containing one line such as `0.1.0`.
   CI verifies it matches `herdr-plugin.toml`.
2. Require `uname -s == Linux`; map `x86_64`/`amd64` to `amd64` and
   `aarch64`/`arm64` to `arm64`.
3. Construct asset
   `herdr-source-control_${version}_linux_${arch}.tar.gz` and URL
   `https://github.com/mariotmc/herdr-source-control/releases/download/v${version}/${asset}`.
4. Create a same-filesystem temporary directory under `bin/.install-<pid>`, with an EXIT trap
   that removes it.
5. Download the archive and
   `https://github.com/mariotmc/herdr-source-control/releases/download/v${version}/checksums.txt`
   using `curl -fL` or `wget`.
6. Extract only the line whose filename exactly equals the asset; verify with `sha256sum -c` or
   `shasum -a 256` by comparing the computed lowercase digest.
7. Extract an archive that must contain exactly one regular file named `herdr-source-control`;
   reject absolute paths, traversal, links, or extra members.
8. Install atomically with executable mode as `bin/herdr-source-control.new`, then `mv` to
   `bin/herdr-source-control` on the same filesystem.
9. If the release returns HTTP 404, fall back to local `go build` only when Go 1.25+ is available.
   Other download/checksum/extraction failures are fatal and do not silently fall back.
10. Otherwise fail with a concise list of requirements.

Never use `sudo`, package managers, or modify PATH. Use temporary directories and atomic rename
so failed installs do not leave a partial binary.

During initial unreleased development, `[[build]]` may temporarily call `scripts/build.sh`.
Before v0.1.0 is published, switch it to `scripts/install-release.sh` and test remote install.

### 12.4 CI

Pull-request/main CI:

```text
gofmt -l .                 # fail if output is nonempty
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/herdr-source-control
```

Run on Linux amd64. Add arm64 build verification without execution.

Release CI:

- Trigger on `v*` tags.
- Verify tag, manifest version, and version constant match.
- Run full CI.
- Build both artifacts.
- Generate checksums.
- Create GitHub release and attach assets.
- Do not publish if the working tag is not on main.

## 13. Test Strategy

### 13.1 Unit tests: porcelain parser

Cover:

- Clean repository
- Staged modification
- Unstaged modification
- Same file staged and unstaged
- Added then modified
- Staged/worktree deletions
- Type change
- Rename and copy, including score and old/new paths
- Rename plus worktree modification
- All unmerged XY combinations
- Individual nested untracked files
- Ignored record ignored
- Spaces, tabs, newlines, backslashes, quotes, leading dashes, ESC, and invalid UTF-8 in paths
- Very long paths
- Detached HEAD
- Unborn branch
- No upstream
- Missing upstream counts
- Ahead only, behind only, diverged
- Integer overflow
- Unknown/malformed record
- Exactly one trailing NUL accepted without an empty record
- Unterminated final record rejected
- Rename/copy missing its original-path field rejected
- Valid records followed by one malformed record publish nothing

### 13.2 Unit tests: grouping and presentation

- Fixed group order
- Empty groups hidden
- Mixed file duplicated into staged/changes projections
- Conflict excluded from other groups
- Untracked included in Changes
- Raw-byte path sorting
- Rename sorted by destination
- Selection retention after refresh
- Selection fallback for disappearing first, middle, last, and entire-group rows follows the
  exact backward-then-forward rule
- Terminal sanitization prevents escape-sequence injection
- Cell-safe truncation for ASCII, combining characters, CJK, and invalid bytes
- Wide/narrow/small/minimum-size render snapshots
- `NO_COLOR` output remains semantically complete

### 13.3 Unit tests: Bubble Tea update loop

- Initial refresh and recurring polls
- Poll schedules next poll even when refresh cannot run
- Manual refresh coalesces to one queued request
- Manual/focus/mutation refresh reasons survive coalescing and preserve manual semantics
- Poll while busy is dropped
- Stale refresh ID ignored
- Ignored stale result cannot clear newer busy/stale/error/queued state
- Refresh from old repository epoch ignored after mutation
- Starting a mutation cancels an active safe read without waiting for it
- Cancelling an active snapshot retires its busy bookkeeping, and mutation completion immediately
  starts the queued refresh rather than deadlocking refreshes
- Complete snapshot publishes atomically; no branch/group subfield updates early
- Mutation exclusivity
- Mutation completion always refreshes
- Modal remains open after checkout/create failure
- Successful checkout/create closes modal
- Late branch response ignored after modal close
- Expired status ID cannot clear a newer message
- Resize preserves focus/modal/input
- Render-local `View.OnMouse` rectangles activate only intended controls without mutating model
  state during render
- Text input suppresses global printable shortcuts

### 13.4 Integration tests with real Git

Use `t.TempDir`, isolated HOME, explicit user.name/email, and local bare remotes. No internet or
developer Git config.

Scenarios:

- Discovery from subdirectory
- Discovery through symlink and real path yields one canonical identity
- Linked worktree with `.git` file and common dir
- Nested repository
- Bare/non-repository rejection
- Every status/group state
- Successive snapshots replace rather than accumulate rows across
  modified+untracked -> staged+untracked -> modified+untracked -> clean
- Rename/delete merge conflict appears only once in Merge Changes with other groups empty
- Weird filename bytes on Linux
- Branch metadata and recent ordering
- Branch checkout clean
- Dirty compatible checkout carried safely
- Dirty conflicting checkout refused without data loss
- Branch checked out in another worktree disabled/refused
- Create valid/invalid/existing branch
- Create from detached HEAD
- Checkout/create refresh replaces complete HEAD, upstream, counts, groups, and selection state
- Ahead/behind against bare remote
- Successive local commits refresh exact ahead counts from 1 to 2
- Exact ahead-only, behind-only, and shared-merge-base diverged counts
- Upstream already contained in local merge history reports ahead-only, not divergence
- Deleted tracking ref with intact branch config reports Upstream unavailable and disables Sync
- Sync up to date
- Sync ahead-only pushes exact branch
- Sync behind-only fast-forwards
- Diverged sync refuses fast-forward and never pushes
- Fast-forward blocked by dirty worktree
- Fetch/fast-forward/push failure refresh behavior
- Branch/upstream changed between fetch and push prevents push
- Upstream configuration changed between status and exact-upstream queries rejects the snapshot;
  counts are never published against a different upstream tuple
- HEAD changed after verification but before push still pushes only the captured verified OID
- Missing `remote.<name>.fetch` still updates the exact configured local upstream ref
- Custom fetch refspec does not prevent exact upstream update
- Remote force-update changes only the exact configured tracking ref
- No unrelated remote-tracking ref is updated or pruned
- Local-branch upstream (`remote=.`) is rejected as unsupported
- `fetch.prune=true` and `remote.<name>.pruneTags=true` do not prune refs
- `merge.autoStash=true` does not create a stash
- `push.followTags=true` and recursive-submodule configuration do not broaden the exact push
- Mirror remote configuration disables Sync before network or ref mutation
- `submodule.recurse=true` does not make switch/create update submodules
- Repository lock is never removed

Do not coordinate integration tests with arbitrary sleeps. Use command completion, channels, and
filesystem assertions.

### 13.5 Fake Git tests

Place an executable fake `git` first in PATH to verify:

- No shell is involved
- Arguments are exact and injection-safe
- Leading-hyphen branch/remote candidates are terminated or rejected before becoming options
- stdin is closed
- Locale/pager/prompt environment is set
- `GIT_OPTIONAL_LOCKS=0` applies only to reads
- stderr sanitization/truncation
- Read/fetch timeout kills descendants; local working-tree mutations and push are never hard-killed
- Fetch or fast-forward failure never invokes push
- Identity change never invokes push
- Ahead zero skips push
- Positive ahead uses exact push refspec
- Push uses captured full OID rather than `HEAD`
- Push ignores quit/cancel/timeout after spawn and completes before final refresh
- Inherited `GIT_ASKPASS` and `SSH_ASKPASS` are overridden with `/bin/false`
- A child trying to open `/dev/tty` fails because it has no controlling terminal
- A real local SSH transport fixture verifies password/passphrase prompts cannot consume TUI input

### 13.6 Herdr launcher tests

Use a fake `HERDR_BIN_PATH` that records argv and returns fixture JSON:

- Context cwd precedence
- Missing workspace ID
- Repository and non-repository identity
- Symlink and real-path invocations focus the same identity-matched pane
- Matching pane focused
- Matching stale pane detected by process-info, closed, and replaced
- Missing/optional process-info fields are indeterminate and never cause pane closure
- Different repository pane ignored
- Duplicate concurrent opens serialized
- Launcher reports identity before releasing the open lock
- New pane uses tab placement, workspace, cwd, env, and focus
- Tab renamed Source Control
- Malformed/error JSON returns nonzero
- Launcher metadata-report failure closes the new pane and fails the action
- TUI metadata-refresh failure is nonfatal

### 13.7 Manual acceptance matrix

Run on:

- Native Linux, repository in native filesystem
- WSL, repository under `/home`
- WSL, repository under `/mnt/c`
- Windows Terminal with Cascadia Mono and no Nerd Font
- `NO_COLOR=1`
- Terminal sizes: `120x35`, `80x24`, `50x16`, `39x11`

Complete branch switch, refresh, and sync independently with keyboard and mouse. Complete branch
creation with keyboard text entry and mouse-accessible Create/Cancel controls.

Performance fixture:

- Generate 10,000 tracked files and 1,000 changed paths in a temporary repository.
- While a deliberately blocked fake status command is active, injected navigation/resize
  messages must update/render within 100 ms on the development WSL machine.
- Automatic refresh must start no later than one poll interval after the previous poll tick;
  actual Git duration is measured and reported but not assigned a cross-filesystem hard limit.

## 14. Implementation Phases

### Phase 0: Scaffold

- [ ] Add `go.mod`, license, README skeleton, manifest, scripts, and package directories.
- [ ] Pin dependencies.
- [ ] Implement version/usage output and `open`/`tui` dispatch.
- [ ] Add CI.
- [ ] Verify `go test ./...`, race, vet, and build.

Exit criterion: empty plugin builds, links, and opens a dedicated tab with a static screen.

### Phase 1: Git discovery and status parser

- [ ] Implement subprocess runner and process-group cancellation.
- [ ] Implement Git version check and repository discovery.
- [ ] Implement porcelain-v2 parser with raw paths.
- [ ] Implement operation-in-progress detection.
- [ ] Implement grouping/sorting projection and sanitization.
- [ ] Add parser and real-Git integration tests.

Exit criterion: CLI/debug path can produce a complete deterministic snapshot for all fixtures.

### Phase 2: Main read-only UI

- [ ] Implement Bubble Tea model/messages/commands.
- [ ] Implement polling/manual/focus refresh and stale-result handling.
- [ ] Implement responsive layouts and list navigation.
- [ ] Implement Refresh keyboard/mouse control.
- [ ] Implement clean/loading/error/no-repository states.
- [ ] Implement Help and status feedback.

Exit criterion: changed files stay current, sorted, safe, and readable at all target sizes.

### Phase 3: Herdr launcher

- [ ] Parse plugin context.
- [ ] Implement identity lock, pane discovery, focus, and open.
- [ ] Report TUI metadata.
- [ ] Document recommended keybinding.
- [ ] Verify repeated Open never creates duplicate live tabs for one repo/workspace.

Exit criterion: the action opens/focuses a dedicated Source Control tab without modifying other
tabs or layouts.

### Phase 4: Branch picker and checkout

- [ ] Implement branch query/parser and metadata.
- [ ] Implement Bubbles searchable list and custom branch delegate.
- [ ] Implement worktree/current disabled states.
- [ ] Implement safe checkout and refresh.
- [ ] Add keyboard/mouse and failure tests.

Exit criterion: local branches can be searched and safely checked out without data loss.

### Phase 5: Branch creation

- [ ] Implement modal input and validation.
- [ ] Implement create-and-switch.
- [ ] Preserve modal input/errors on failure.
- [ ] Test valid, invalid, duplicate, detached, and race cases.

Exit criterion: a valid branch can be created from HEAD and invalid attempts are harmless.

### Phase 6: Sync

- [ ] Resolve exact upstream remote/ref.
- [ ] Implement preconditions and disabled explanations.
- [ ] Implement fetch-refresh-fast-forward-refresh-verify-push-refresh state machine.
- [ ] Add phase-specific spinner/status feedback.
- [ ] Test up-to-date, ahead, behind, divergence, dirty, auth, network, rejection, and races.

Exit criterion: Sync never performs an unsafe history or working-tree operation and reaches the
expected upstream state when fast-forward synchronization is possible.

### Phase 7: Release hardening

- [ ] Complete README.
- [ ] Add release workflow and verified install script.
- [ ] Test install from GitHub shorthand in a clean environment without Go.
- [ ] Test update by reinstalling while preserving plugin state/logs.
- [ ] Run manual acceptance matrix.
- [ ] Publish v0.1.0 and add GitHub topic `herdr-plugin`.

Exit criterion: a user can install, bind, open, use, update, and uninstall without manual runtime
or font setup.

## 15. Definition of Done for v0.1.0

Version 0.1.0 is done only when all are true:

- Every v1 goal and functional behavior in this plan is implemented.
- Every non-goal remains absent.
- Full automated suite, race detector, vet, formatting, and release builds pass.
- No known plugin-issued data-loss or force-operation path exists; documented guarantees exclude
  side effects from user hooks, filters, transports, and external concurrent processes.
- No command can be injected through branch names, paths, remote names, or Git output.
- No raw Git output can inject terminal control sequences.
- No Git child can take over the TUI for a pager, editor, or credential prompt.
- During a slow Git command, keyboard navigation and repaint messages already queued in Bubble Tea
  are processed within 100 ms on the 10,000-file/1,000-change acceptance fixture; Git command
  duration itself is not bounded on `/mnt/c`.
- Poll/manual refresh and async generation handling are verified under concurrent external edits.
- The UI works with keyboard only and with mouse for every non-text action; branch-name entry
  necessarily uses the keyboard. It also works with `NO_COLOR` and Cascadia Mono.
- Repeated Open focuses one tab rather than creating duplicates.
- The plugin never auto-opens or changes unrelated Herdr pane layouts.
- Linux amd64 and arm64 artifacts install with verified checksums.
- README documents scope, safety, keybinding, update, and uninstall.
- Manual acceptance has passed on native Linux and both WSL filesystem locations.

## 16. Future Extension Notes

### Diff view

When added, file activation should transition to a dedicated `ModeDiff` backed by a new Git
method accepting raw path bytes and staged/worktree side. The existing `Change` model already
contains both index and worktree state. Keep Git diff execution asynchronous and cap output size.
Do not reinterpret escaped display paths as Git paths.

### Staging

Add mutations to the repository interface rather than embedding Git calls in the UI. Serialize
them through the existing mutation IDs/epoch mechanism. Whole-file operations must pass raw paths
after `--`. Hunk staging requires snapshot/race checks and is a separate project phase.

### Commit

Use a temporary message file and `git commit -F`, not shell quoting or `-m` concatenation. Treat
hooks/signing and cancellation as first-class long-running operations.

### Filesystem watcher

Do not add one in v1. If polling later proves insufficient, follow the useful VS Code watcher
rules rather than recursively refreshing on every event:

- Watch the worktree and worktree-specific Git dir; use the common Git dir for shared refs.
- Ignore `index.lock` creation and watcher cookie files; refresh after a lock disappears.
- Reconfigure the watched upstream tracking ref after each successful snapshot.
- Fall back to broader Git-dir watching when refs are packed and a direct ref file does not exist.
- Debounce bursts and retain exactly one trailing refresh.
- Retain poll, focus, and manual refresh because filesystem events can be lost, especially on
  WSL-mounted filesystems.

### Very large change sets

VS Code can stop status at a configured limit and suppress watcher refresh for huge repositories.
V1 intentionally promises the complete changed-file list, so it does not truncate. Measure status
duration and rendered row count in logs and acceptance fixtures. Add a user-visible limit only in
response to demonstrated performance problems, and make incompleteness explicit if one is added.

### When to reconsider OpenTUI

Bubble Tea remains appropriate for diffs, staging, commits, history, and multiple panels. Revisit
OpenTUI only if the product becomes strongly mouse-first with many nested click targets, drag
resizing, context menus, and editor-like code widgets. The domain and Git packages must remain UI
independent so that decision would not discard repository logic.
