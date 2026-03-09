# Releasing

Read when:
- You are preparing a new PostFlow release.
- You need to publish CLI archives, the Homebrew formula, and the Docker image.
- You need to verify the GitHub release workflows after tagging.

## Release flow

PostFlow releases are created from `main` and use GitHub Releases as the trigger point.

Publishing a GitHub release starts these workflows:

- `Release CLI + Homebrew`
- `Release Docker Image`

The push to `main` should also leave `Quality Gate` green before cutting the release.

## Pre-release checklist

1. Start from the repository root on `main`.
2. Confirm the worktree is clean enough to release:
   - committed changes ready to ship
   - no accidental binaries, databases, or generated files staged for commit
   - local backup files such as `publisher.db.bak-*` must stay untracked
3. Run the baseline validation:

```bash
go test ./...
```

4. Inspect the latest published version and choose the next semver tag:

```bash
gh release list --limit 10
```

Use a patch bump for normal fixes. Example: if the latest release is `v0.2.3`, the next patch release is `v0.2.4`.

## Publish steps

1. Push the release commits to `main`:

```bash
git push origin main
```

2. Wait for the `Quality Gate` workflow on `main` to finish green:

```bash
gh run list --limit 10
gh run view <run-id>
```

3. Create the GitHub release from `main`:

```bash
gh release create v0.2.4 \
  --target main \
  --title v0.2.4 \
  --generate-notes
```

Replace `v0.2.4` with the actual version you are publishing.

## Post-release verification

After the release is published, monitor the release workflows:

```bash
gh run list --limit 10
gh run view <run-id>
```

Expected outcome:

- `Release CLI + Homebrew` uploads the release tarballs to GitHub Releases
- `Release CLI + Homebrew` updates `antoniolg/homebrew-tap` when `HOMEBREW_TAP_GITHUB_TOKEN` is configured
- `Release Docker Image` publishes `ghcr.io/antoniolg/postflow:<tag>`
- `Release Docker Image` also refreshes `ghcr.io/antoniolg/postflow:latest`

Verify the release page includes the CLI archives:

- `postflow_<version>_darwin_amd64.tar.gz`
- `postflow_<version>_darwin_arm64.tar.gz`
- `postflow_<version>_linux_amd64.tar.gz`
- `postflow_<version>_linux_arm64.tar.gz`

If a workflow fails:

1. Inspect the failing run with `gh run view <run-id> --log`.
2. Fix the underlying issue on `main`.
3. Push the fix.
4. Re-run the failed workflow or cut a fresh release tag if the failure happened after publishing an invalid release.

## Quick command set

```bash
git status --short
go test ./...
gh release list --limit 10
git push origin main
gh run list --limit 10
gh release create v0.2.4 --target main --title v0.2.4 --generate-notes
gh run view <run-id>
```
