# Backup Crunch

Desparate rescue tool for consolidating backup data that is all over the place:
**Your data survived. It's just scattered.**

**Warning:** This sideproject was implemented mainly using AI tools. Use it with a grain
of more salt than with any other open source project you're using.

## The Idea

The original OneDrive is gone. What's left are partly downloaded instances strewn
across laptops and external drives — overlapping, contradictory, riddled
with zero-length "online-only" placeholders that look like files but hold
nothing. Somewhere in that mess is the best surviving copy of everything you
care about. backup-crunch finds it.

Point it at every source you have. For each file, it picks the **best surviving
version** — non-empty and as new as possible — and assembles them into one
clean tree. No copy is overwritten, no version silently lost, no guesswork left
unexplained.

- **Sources are strictly read-only.** A read-only invariant test checksums every
  source file before and after each run to prove nothing was touched.
- **The manifest is the product.** Every decision — winner, rejected copies,
  warnings, unrecoverable paths — is recorded in an auditable JSON manifest,
  written even on a dry run. You can see *why* each file was chosen before a
  single byte is copied.
- **Built for the messy real case:** mixed Windows/macOS filename encodings,
  timestamps clobbered by the backup process, dehydrated cloud placeholders, and
  Recycle Bin debris — all handled, counted, and surfaced rather than hidden.

## How It Works

For every relative path that appears in any source (matched **case- and
Unicode-insensitively**, so Windows `NFC` names group with macOS `NFD` ones):

1. Zero-length copies are discarded — they can never win.
2. Among the remaining non-empty copies, the **newest modification time** wins.
3. Ties on mtime break to the **largest file**; remaining ties break to the
   lowest source index (deterministic).
4. A path whose only copies are zero-length is **omitted** and listed as
   unrecoverable.

Because backups pass through Windows → OneDrive → a backup process, modification
times can be clobbered to the copy date. The tool **flags suspicious clusters**
(many files within one source sharing an identical mtime) so you can judge how
much to trust the "newest wins" result.

## Install / Build

Requires Go 1.22+.

```sh
make build        # produces ./bin/backup-crunch
# or run
go build -o bin/backup-crunch ./cmd/backup-crunch
```

## Usage

```sh
backup-crunch merge --out <dir> [flags] <src1> <src2> [<src3> ...]
```

Always start with a dry run to inspect decisions before copying:

```sh
backup-crunch merge --out ./recovered --dry-run ./laptopA ./laptopB ./laptopC
```

Then run for real (optionally with content hashing to flag divergent copies):

```sh
backup-crunch merge --out ./recovered --hash ./laptopA ./laptopB ./laptopC
```

### Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `--out <dir>` | (required) | Output tree root. Created if absent. Must not resolve inside any source. |
| `--dry-run` | off | Scan + select + write the manifest, but copy **zero** files. |
| `--manifest <file>` | `<out>/manifest.json` | Where to write the JSON decision manifest. |
| `--hash` | off | SHA-256 candidates that tie on (mtime, size) to flag content divergence. |
| `--ts-cluster-threshold <n>` | 50 | Min identical-mtime files per source to flag a suspicious cluster. |
| `--copy-tool {go\|cp\|rsync}` | `go` | Copy backend. `go` (default) is the fully-tested pure-Go path; `cp`/`rsync` are best-effort escape hatches. |
| `--workers <n>` | 1 | Parallel copy/hash workers (1 = sequential). |
| `--exclude <glob>` | (none) | Skip files/dirs matching this glob. **Repeatable.** Matched against both the relative path and the base name; a matching directory prunes its whole subtree. |
| `-v`, `--verbose` | off | Per-file detail: which entries were skipped, excluded, or unreadable. Off by default to keep output quiet. |

Positional source arguments must come **after** the flags.

### Excluding Junk (`--exclude`)

Windows backups carry system folders and deleted-file debris you usually don't
want in a restored tree — e.g. the Recycle Bin (`$RECYCLE.BIN` with its `$I…`
metadata stubs and `$R…` content files), `System Volume Information`, and
per-folder cruft like `Thumbs.db` / `desktop.ini`. Prune them with repeatable
`--exclude` globs:

```sh
backup-crunch merge --out ./recovered \
  --exclude '$RECYCLE.BIN' --exclude 'System Volume Information' \
  --exclude '$I*' --exclude '$R*' \
  --exclude 'Thumbs.db' --exclude 'desktop.ini' --exclude '*.tmp' \
  ./laptopA ./laptopB
```

Patterns use `path.Match` glob syntax and are **case-sensitive**. Excluded
entries are counted in the summary (`excluded`) so the exclusion is auditable.

### Unreadable / Placeholder Files

Dehydrated cloud placeholders (e.g. OneDrive "online-only" files whose content
was never materialized) appear in directory listings but cannot be opened —
`stat`/`open` returns "no such file or directory". These contribute no data, so
the merge routes around them, but they are **not** silently dropped:

- An unreadable **file** is recorded as an unreadable candidate. If a readable
  copy exists on another source it still wins (the record carries an
  `unreadable_source` warning); if *every* copy of a path is unreadable, the
  path is reported as unrecoverable instead of vanishing.
- An unreadable **directory** (whole subtree that can't be entered) is counted
  and its path listed under `unreadable_dirs` in the manifest.
- The per-entry noise is silenced by default; run with `--verbose` to see each
  one. The summary always shows the `unreadable` counts.

## The Manifest

`manifest.json` records run-level totals plus, per output path: the chosen
winner (source, mtime, size), how many candidates were considered, every
rejected candidate, any warnings (`case_collision`, `content_divergent`), and
the status (`recovered`, `flagged`, `unrecoverable_empty_only`). It also lists
suspicious timestamp clusters. The manifest is always written — even on a
dry run.

## Guarantees & limitations

- **Sources are never modified.** Verified by a read-only invariant test
  (checksum every source file before and after a run).
- **Atomic writes.** Each winner is streamed to a temp file *in the destination
  directory* and `rename`d into place, so it stays atomic even when `--out` is
  on an external drive (no cross-filesystem `EXDEV` fallback).
- **Deterministic.** The same sources produce the same output tree and manifest.
- **Streamed.** Only file metadata is held in memory; content is copied in fixed
  buffers, so memory is bounded regardless of file size.
- Symlinks and other non-regular files are skipped and counted (this also
  prevents escaping a source root via a symlink).
- Path components are interpreted host-natively; a backup containing literal
  backslashes in filenames on a non-Windows host treats them as ordinary
  filename characters, not separators.

## Development

```sh
make test    # go test -race ./...
make vet     # go vet ./...
make fmt     # gofmt -w
```

## License

See [LICENSE](LICENSE) for details.