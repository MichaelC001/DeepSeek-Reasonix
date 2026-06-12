# macOS Home Directory Migration

This guide explains how to move all Reasonix user data on macOS from the
historical directory to the current documented directory.

Chinese: [macOS 用户目录迁移说明](./HOME_MIGRATION.zh-CN.md)

## Short Version

Close the desktop app first, then run:

```sh
reasonix migrate-home
```

After reviewing the preview, apply the migration:

```sh
reasonix migrate-home --apply
```

Then reopen the desktop app.

## What This Migration Fixes

Older macOS builds used the native system config directory:

```text
~/Library/Application Support/reasonix
```

Fresh installs now default to the documented directory:

```text
~/.config/reasonix
```

For compatibility, Reasonix still reads the old directory, so upgrading does not
make history disappear. The `reasonix migrate-home` command moves all Reasonix
user data into the new directory and, after a successful migration, makes both
the CLI and desktop app use the new directory.

## Who Should Run This

Run this migration if all of these are true:

- You use macOS.
- You used an older Reasonix version.
- `~/Library/Application Support/reasonix` exists on your machine.
- You want config, sessions, desktop state, and other Reasonix data to live
  under `~/.config/reasonix`.

You usually do not need this migration if you are a new user or if you already
set a custom directory with `REASONIX_HOME`.

## Before You Start

The migration is conservative:

- The old directory is not deleted.
- The default command is a preview only and does not write files.
- Files are changed only when you pass `--apply`.
- The completion marker is written only after every migration step succeeds.
- If migration fails, Reasonix does not switch to the new directory. You can fix
  the problem and run the command again.

Close the Reasonix desktop app before applying the migration. This avoids open
tabs continuing to hold old session paths.

## Recommended Steps

1. Close the Reasonix desktop app.

2. Preview the migration plan:

   ```sh
   reasonix migrate-home
   ```

   The preview shows the source directory, destination directory, expected file
   copies, config merges, renamed session conflicts, and archived unknown
   conflicts.

3. Apply the migration:

   ```sh
   reasonix migrate-home --apply
   ```

4. Reopen the Reasonix desktop app.

5. Optional: check diagnostics.

   ```sh
   reasonix doctor
   ```

## What Gets Migrated

The tool processes the whole Reasonix user directory, including:

- `config.toml`: user configuration.
- `credentials`: the Reasonix-managed environment file for secrets.
- `sessions/`: global CLI session history.
- `projects/*/sessions/`: desktop project sessions.
- Desktop state: open tabs, recent workspace, workspace list, window size and
  position.
- Memory, cache, metrics, install id, plugin state, and other Reasonix user
  data.

Unknown files are handled too. If the destination path does not exist, the file
is copied. If a different file already exists at the destination, the source file
is archived instead of overwriting the destination.

## How Config Conflicts Are Handled

If the destination already has `config.toml`, it is not overwritten blindly.

Rules:

- Only the source config exists: copy it to the destination.
- Only the destination config exists: keep the destination config.
- Both files are identical: skip.
- Both files exist and differ: merge them, with destination values taking
  priority.

`[[plugins]]` entries are merged by `name`: destination entries win on matching
names, and source-only plugins are preserved.

Before merging, both original config files are backed up under:

```text
~/.config/reasonix/.reasonix-home-migration-backups/<run-id>/
```

## How Credentials Are Handled

`credentials` is also merged instead of overwritten.

Rules:

- Destination keys win.
- Source-only keys are appended.
- A source key never replaces an existing destination key.
- Both original files are backed up before the merge.

## How Session Conflicts Are Handled

If the source and destination both contain a `.jsonl` session with the same file
name but different content, the destination session is not overwritten.

The source session is copied with a migrated filename, for example:

```text
chat.migrated-20260612-120000-ab12cd34.jsonl
```

Desktop sidecar files such as titles, display text, meta files, and checkpoints
are migrated with the renamed session where possible. This keeps history
available even when names collide.

## How Other File Conflicts Are Handled

For unknown same-name files with different content, the tool does not guess how
to merge and does not overwrite the destination.

The source file is saved under:

```text
~/.config/reasonix/.reasonix-home-migration-conflicts/<run-id>/
```

You can inspect those files manually after migration.

## How The Desktop App Uses The Result

The desktop app does not need a separate migration tool.

After `reasonix migrate-home --apply` succeeds, Reasonix writes this marker:

```text
~/.config/reasonix/.reasonix-home-migration.json
```

That marker means the migration completed successfully. After that, both the CLI
and desktop app use the same path rules and resolve to the new directory.

For the desktop app, use this flow:

1. Close the app before migrating.
2. Run the preview and apply commands from the CLI.
3. Reopen the app after the migration succeeds.

After reopening, history, project sessions, tab state, and window state should
still be available; their storage location has moved to the new directory.

## How To Confirm Success

Check for the completion marker:

```sh
ls "$HOME/.config/reasonix/.reasonix-home-migration.json"
```

You can also run:

```sh
reasonix doctor
```

Then open the desktop app and confirm that global and project history are still
visible.

## If Migration Fails

If migration fails, Reasonix does not write the completion marker and does not
switch to the new directory.

You can:

1. Read the error in the command output.
2. Fix permission, disk space, or file-in-use problems.
3. Run the apply command again:

   ```sh
   reasonix migrate-home --apply
   ```

The old directory remains in place, so a partial failed run does not remove the
original data.

## Temporarily Keep Using The Old Directory

Do not delete the new directory as a rollback strategy. If you need to force the
old directory temporarily, set `REASONIX_HOME`:

```sh
export REASONIX_HOME="$HOME/Library/Application Support/reasonix"
reasonix chat
```

For the desktop app, use the same environment override when launching it instead
of manually deleting migrated files.

## FAQ

### Will my history disappear after migration?

No. The migration copies global sessions, project sessions, and desktop sidecar
files. Same-name session conflicts are renamed and preserved instead of
overwritten.

### Is the old directory deleted?

No. The old directory is left in place so you can verify the result or keep your
own backup.

### Can I run the command more than once?

Yes. After a successful migration, running it again detects the completion marker
and reports that migration has already completed.

### Will an existing destination config be overwritten?

No. `config.toml` is merged with destination values taking priority.
`credentials` is also merged with destination keys taking priority.

### Is there a desktop button for this?

Not currently. Use the CLI migration command. If a desktop button is added later,
it should call the same migration logic.
