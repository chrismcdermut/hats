'use strict';
/**
 * manyhats installer: fetch the prebuilt `hats` Go binary that matches this
 * package version and the host platform, from the GitHub release. The npm
 * package is a thin distributor — the one implementation is the Go binary, so
 * npm never drifts from brew/go-install.
 */
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync } = require('child_process');

const REPO = 'chrismcdermut/hats';
const VERSION = require('./package.json').version; // matches the git tag vX.Y.Z

function targetTriple() {
  const platform = process.platform; // 'darwin' | 'linux' | 'win32'
  const arch = process.arch; // 'x64' | 'arm64'
  if (platform !== 'darwin' && platform !== 'linux') {
    throw new Error(
      `manyhats: unsupported platform '${platform}'. Install via 'brew install ${REPO.split('/')[0]}/tap/hats' or 'go install github.com/${REPO}@latest'.`
    );
  }
  const archAliases = arch === 'x64' ? ['amd64', 'x86_64', 'x64'] : arch === 'arm64' ? ['arm64', 'aarch64'] : null;
  if (!archAliases) {
    throw new Error(`manyhats: unsupported arch '${arch}'.`);
  }
  return { osAliases: [platform, platform === 'darwin' ? 'macos' : platform], archAliases };
}

function cacheDir() {
  const base = process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache');
  return path.join(base, 'manyhats');
}

function binaryPath() {
  return path.join(cacheDir(), `hats-${VERSION}`);
}

async function pickAsset() {
  const { osAliases, archAliases } = targetTriple();
  const url = `https://api.github.com/repos/${REPO}/releases/tags/v${VERSION}`;
  const res = await fetch(url, {
    headers: { 'User-Agent': 'manyhats-installer', Accept: 'application/vnd.github+json' },
  });
  if (!res.ok) {
    throw new Error(`manyhats: cannot read release v${VERSION} (${res.status}). It may not be published yet.`);
  }
  const rel = await res.json();
  const assets = rel.assets || [];
  const match = assets.find((a) => {
    const n = a.name.toLowerCase();
    return (
      (n.endsWith('.tar.gz') || n.endsWith('.tgz')) &&
      osAliases.some((o) => n.includes(o.toLowerCase())) &&
      archAliases.some((x) => n.includes(x.toLowerCase()))
    );
  });
  if (!match) {
    throw new Error(
      `manyhats: no release asset for ${process.platform}/${process.arch} in v${VERSION}. Assets: ${assets.map((a) => a.name).join(', ') || '(none)'}`
    );
  }
  return match.browser_download_url;
}

async function download(url, dest) {
  const res = await fetch(url, { headers: { 'User-Agent': 'manyhats-installer' }, redirect: 'follow' });
  if (!res.ok) throw new Error(`manyhats: download failed (${res.status}) ${url}`);
  const buf = Buffer.from(await res.arrayBuffer());
  fs.writeFileSync(dest, buf);
}

// ensureBinary returns the path to a ready-to-exec hats binary, downloading and
// extracting the platform release archive on first use. Cached by version.
async function ensureBinary() {
  const bin = binaryPath();
  if (fs.existsSync(bin)) return bin;

  const dir = cacheDir();
  fs.mkdirSync(dir, { recursive: true });
  const tmpTar = path.join(dir, `hats-${VERSION}.download.tar.gz`);
  const tmpExtract = path.join(dir, `extract-${VERSION}-${process.pid}`);

  const assetUrl = await pickAsset();
  await download(assetUrl, tmpTar);

  fs.mkdirSync(tmpExtract, { recursive: true });
  execFileSync('tar', ['-xzf', tmpTar, '-C', tmpExtract]);

  // find the 'hats' binary inside the extracted tree
  let found = null;
  const walk = (d) => {
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
      const p = path.join(d, e.name);
      if (e.isDirectory()) walk(p);
      else if (e.name === 'hats') found = p;
    }
  };
  walk(tmpExtract);
  if (!found) throw new Error('manyhats: extracted archive did not contain a `hats` binary.');

  fs.copyFileSync(found, bin);
  fs.chmodSync(bin, 0o755);

  // cleanup
  try { fs.rmSync(tmpTar, { force: true }); } catch {}
  try { fs.rmSync(tmpExtract, { recursive: true, force: true }); } catch {}

  return bin;
}

module.exports = { ensureBinary, binaryPath, VERSION };

// When run directly (npm postinstall), fetch the binary now so the first
// invocation is instant. Failures are non-fatal: the bin shim retries lazily.
if (require.main === module) {
  ensureBinary()
    .then((bin) => console.log(`manyhats: installed hats ${VERSION} -> ${bin}`))
    .catch((err) => {
      console.warn(`${err.message}\nmanyhats: will retry on first run.`);
      process.exit(0);
    });
}
