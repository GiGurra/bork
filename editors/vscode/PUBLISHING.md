# Releasing bork for VS Code

The extension ID is `gigurra.bork` in both registries. Its `package.json` version and `vscode-v<version>` tags are independent of the bork compiler's version. Publishing tools are pinned by `package-lock.json`; CI packages every pull request without using credentials.

## One-time human setup

Every automatic compiler GitHub Release attaches the packaged VSIX and includes its SHA-256 in `checksums.txt`. This sharing path needs neither registry token nor Marketplace account. The extension keeps its independent `package.json` version; compiler patch tags do not rewrite it. The setup below applies only to optional registry publishing.

The repository maintainer must create or obtain control of these accounts and credentials before the first release:

1. **Marketplace publisher `gigurra`.** Sign in to [publisher management](https://marketplace.visualstudio.com/manage/publishers/) with your Microsoft account and create that publisher ID (or use the existing one if you own it). Its display name can be GiGurra.
2. **Marketplace token → `VSCE_PAT`.** In Azure DevOps, create a personal access token for **All accessible organizations**, with **Marketplace → Manage** scope, using the account authorized for the publisher. Save it as the repository Actions secret `VSCE_PAT`. See [Microsoft's publishing guide](https://code.visualstudio.com/api/working-with-extensions/publishing-extension).
3. **Open VSX namespace `gigurra`.** Create an Eclipse account, sign in to [Open VSX](https://open-vsx.org/), link the required account, and accept its publisher agreement. Generate an access token under user settings. With `OVSX_PAT` in your local environment, run `npx --no-install ovsx create-namespace gigurra` from `editors/vscode` after `npm ci`. Claim namespace ownership through the [namespace access process](https://github.com/eclipse-openvsx/openvsx/wiki/Namespace-Access) so you control publishing rights.
4. **Open VSX token → `OVSX_PAT`.** Store that access token as the repository Actions secret `OVSX_PAT`. See [Open VSX publishing instructions](https://github.com/eclipse-openvsx/openvsx/wiki/Publishing-Extensions).

Create the secrets in **GiGurra/bork → Settings → Secrets and variables → Actions**. Keep tokens out of commits, issues, and chat. Neither token is needed to test or package the extension. If either publisher ID is unavailable, update `publisher`, the registry links, and this guide consistently before the first release.

Microsoft currently announces retirement of global Azure DevOps PATs on **December 1, 2026**. The prepared workflow uses `VSCE_PAT` as requested; migrate Marketplace publishing to [Microsoft Entra authentication](https://code.visualstudio.com/api/working-with-extensions/publishing-extension#secure-automated-publishing-to-visual-studio-marketplace) before that retirement. That migration requires a human-owned Azure identity authorized for the publisher.

## Prepare a release

Update `editors/vscode/package.json` and its lockfile to the desired version, add that version to `CHANGELOG.md`, and merge the changes through a PR. The manifest validator checks metadata, discovery keywords, packaged files, the PNG icon, and the changelog.

```sh
npm ci --prefix editors/vscode
npm test --prefix editors/vscode
npm run package --prefix editors/vscode
```

Install the resulting VSIX in VS Code and try a `.bork` file with a current compiler. The PNG icon is rendered from `images/icon.svg`; to regenerate it with CairoSVG installed, run `python3 -m cairosvg editors/vscode/images/icon.svg -o editors/vscode/images/icon.png` from the repository root. The vector source stays in the repository; only the PNG is packaged.

When ready, tag the merged commit (for version 0.1.0):

```sh
git fetch origin main
git tag vscode-v0.1.0 origin/main
git push origin vscode-v0.1.0
```

The **VS Code extension release** workflow refuses a tag that differs from `package.json` or a commit outside main. It tests and packages once, uploads the VSIX artifact, then publishes that same artifact independently to Marketplace and Open VSX. The secrets are exposed only in the corresponding publish step.

## Retry and verify

If one registry fails after the other succeeds, fix its account or secret and run the workflow manually using the **same release tag** as the ref. Choose only the failed registry (`marketplace` or `open-vsx`). Duplicate versions are skipped; there is no need to retag a release or bump the version just to retry.

After publishing, check both listings:

- [VS Code Marketplace](https://marketplace.visualstudio.com/items?itemName=gigurra.bork)
- [Open VSX](https://open-vsx.org/extension/gigurra/bork)

Confirm the version, icon, README, and installation in each editor. Compiler installation remains separate. No release tag is created by this preparation PR.
