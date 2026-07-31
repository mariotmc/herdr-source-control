# herdr-source-control

`herdr-source-control` is a lightweight, terminal-native Source Control tab for
[Herdr](https://herdr.dev). It shows the current repository's changed files, branch, and upstream
state without turning Herdr into a full Git client.

## Scope

The plugin provides:

- Changed files grouped as Merge Changes, Staged Changes, and Changes
- Automatic local status polling, focus refresh, and manual refresh
- Current branch and upstream ahead/behind status
- Search and safe checkout of local branches
- Creation of a local branch from the current `HEAD`
- Conservative synchronization with an already configured upstream
- Keyboard and mouse operation without a Nerd Font or special terminal glyphs

The changed-file list is read-only. The plugin does not show diffs, stage or unstage files, make
commits, resolve conflicts, manage stashes or tags, publish branches, configure remotes, or manage
worktrees. It does not support native Windows; Herdr running inside WSL is supported.

## Safety

The plugin invokes the system `git` directly and never issues discard, reset, stash,
force-checkout, force-push, branch deletion, or lock-file deletion commands.

Sync is deliberately narrow:

1. Fetch the exact configured upstream ref without tags, pruning, or submodules.
2. Update the local branch only with `git merge --ff-only --no-autostash`.
3. Push only a verified commit object to the exact upstream ref, without force or tags.

Sync stops on divergent history, branch or upstream changes during the operation, an unresolved
merge/rebase/cherry-pick/revert, a missing upstream, or a mirror remote. A dirty working tree does
not automatically block Sync, but Git must be able to fast-forward without overwriting it.

Git hooks, filters, credential helpers, transports, and other concurrently running processes can
have effects outside these guarantees. Authentication is non-interactive: prepare HTTPS
credentials, SSH keys, host verification, and passphrases outside the plugin.

## Requirements

- Herdr 0.7.5 or newer
- Linux amd64 or arm64, including Linux under WSL
- Git 2.31 or newer
- `curl` or `wget`, GNU tar, and `sha256sum` or `shasum` during installation

Published installs use a statically built release binary and do not require Go. If the matching
release asset returns HTTP 404, the installer can build from source when Go 1.25 or newer is
available. Checksum, extraction, and other download failures never fall back to an unverified
build.

## Install

```sh
herdr plugin install mariotmc/herdr-source-control
```

The installer downloads only the release matching the repository's exact `VERSION`, verifies its
SHA-256 checksum, validates that the archive contains only the expected regular file, and replaces
the plugin binary atomically. It never uses `sudo`, installs packages, edits `PATH`, or changes user
keybindings.

Add the recommended binding to your Herdr `config.toml`:

```toml
[[keys.command]]
key = "prefix+shift+s"
type = "plugin_action"
command = "herdr-source-control.open"
description = "open source control"
```

Reload Herdr's configuration after editing it:

```sh
herdr server reload-config
```

The binding is optional and may be changed if it conflicts with an existing command. The plugin
does not add, overwrite, or remove bindings itself. You can also invoke the action directly:

```sh
herdr plugin action invoke herdr-source-control.open
```

Invoke Open Source Control from a pane inside a repository. The plugin opens one dedicated Source
Control tab for that repository, or focuses its existing tab. From a non-repository directory it
opens a No Repository view that can be refreshed after a repository is initialized.

## Keybindings

| Key | Action |
| --- | --- |
| `Tab` / `Shift+Tab` | Move focus |
| `Up` / `k`, `Down` / `j` | Select the previous or next changed file |
| `PageUp` / `Ctrl+u`, `PageDown` / `Ctrl+d` | Scroll the changed-file list |
| `Home` / `g`, `End` / `G` | Select the first or last changed file |
| `Enter` / `Space` | Activate the focused control or row |
| `b` | Open the local branch picker |
| `s` | Sync with the configured upstream |
| `r` | Refresh local repository state |
| `?` | Open help |
| `Esc` | Clear feedback, return to the file list, or cancel a modal |
| `q` / `Ctrl+c` | Close the plugin tab when no non-cancellable Git operation is running |

Activating a changed file only reports that diff view is not available. In the branch picker,
type to search, use `n` with an empty search to create a branch, and press `Enter` to select.

## Configuration

Version 0.1 has no plugin configuration file. Polling interval, colors, in-app keys, sync policy,
timeouts, and layout are fixed. The UI respects `NO_COLOR`; otherwise it adapts to terminal color
capabilities and size. No startup hook or event hook is installed, so Source Control opens only
when its action is invoked.

## Update

Reinstall from GitHub to update the plugin and its verified binary:

```sh
herdr plugin install -y mariotmc/herdr-source-control
```

Your Herdr keybinding and plugin state directory are not modified by the release installer.

## Uninstall

```sh
herdr plugin uninstall herdr-source-control
```

Remove the optional `[[keys.command]]` entry from Herdr's `config.toml` and reload the configuration
if you added the recommended binding. Herdr manages the installed plugin directory; the installer
does not place files elsewhere or modify your `PATH`.

## Development

Go 1.25 or newer is required:

```sh
./scripts/build.sh
herdr plugin link .
herdr plugin action invoke herdr-source-control.open
```

`herdr plugin link` does not run build commands, so rebuild after source changes. Run the checks
used by CI with:

```sh
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/herdr-source-control
```

## License

MIT. See [LICENSE](LICENSE).
