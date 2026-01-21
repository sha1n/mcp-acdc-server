# Release Process Setup Guide

This project uses [GoReleaser](https://goreleaser.com/) and GitHub Actions to automate releases. This guide explains how to configure the necessary permissions and secrets to enable automated Homebrew tap updates.

## Prerequisites

1.  **Homebrew Tap Repository**: You must have a GitHub repository for your Homebrew tap (e.g., `sha1n/homebrew-tap`).
2.  **GitHub Token**: A Personal Access Token (PAT) with `repo` scope is required to push formulas to the tap repository.

## Configuration Steps

### 1. Create a Personal Access Token (PAT)

1.  Go to **GitHub Settings** > **Developer settings** > **Personal access tokens** > **Tokens (classic)**.
2.  Click **Generate new token**.
3.  Give it a name (e.g., `HOMEBREW_TAP_GITHUB_TOKEN`).
4.  Select the **`repo`** scope (or `public_repo` if the tap is public).
5.  Generate the token and copy it.

### 2. Add the Secret to the Repository

1.  Navigate to this repository on GitHub.
2.  Go to **Settings** > **Secrets and variables** > **Actions**.
3.  Click **New repository secret**.
4.  **Name**: `HOMEBREW_TAP_GITHUB_TOKEN`
5.  **Secret**: Paste the PAT you generated in the previous step.
6.  Click **Add secret**.

### 3. Workflow Permissions

The `.github/workflows/release.yml` workflow requires write access to repository contents to create releases. This is configured in the workflow file itself:

```yaml
permissions:
  contents: write
```

Ensure that your repository settings allow workflows to have read and write permissions (Settings > Actions > General > Workflow permissions).

## Triggering a Release

To trigger a release, simply push a tag to the repository:

```bash
git tag -a v0.1.0 -m "First release"
git push origin v0.1.0
```

The GitHub Action will:
1.  Build the binaries.
2.  Create a GitHub Release.
3.  Upload the artifacts.
4.  Update the Homebrew formula in `sha1n/homebrew-tap`.
