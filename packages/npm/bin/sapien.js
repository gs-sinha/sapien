#!/usr/bin/env node
"use strict";

/*
 * npm wrapper around the sapien binary (PLAN.md §33): many MCP hosts
 * assume `npx <package> ...` for launching a server, so this package makes
 * `npx @sapien-dev/sapien mcp --workspace .` work without a separate
 * install step.
 *
 * On first run it downloads the matching sapien release binary from GitHub
 * Releases into a per-user cache directory, then execs it with every
 * argument this process was given, stdio inherited. Set SAPIEN_BINARY to
 * skip the download and use a binary you already have (e.g. a local build,
 * or one installed by scripts/install.sh or Homebrew).
 *
 * No dependencies beyond Node's standard library: extraction shells out to
 * the system `tar` (present on macOS, Linux, and Windows 10 1803+).
 */

const fs = require("fs");
const os = require("os");
const path = require("path");
const https = require("https");
const crypto = require("crypto");
const { spawnSync } = require("child_process");

const REPO = "sapien-dev/sapien";
const PKG = require("../package.json");

function log(msg) {
  process.stderr.write("[sapien npm] " + msg + "\n");
}

function fail(msg) {
  log("error: " + msg);
  process.exit(1);
}

// platformInfo maps Node's process.platform/arch to the names goreleaser
// uses for release asset filenames (.goreleaser.yaml: sapien_<version>_<os>_<arch>.<ext>).
function platformInfo() {
  const osMap = { darwin: "darwin", linux: "linux", win32: "windows" };
  const archMap = { x64: "amd64", arm64: "arm64" };

  const sapienOS = osMap[process.platform];
  const sapienArch = archMap[process.arch];
  if (!sapienOS) fail("unsupported platform: " + process.platform);
  if (!sapienArch) fail("unsupported architecture: " + process.arch);

  const ext = sapienOS === "windows" ? "zip" : "tar.gz";
  const binaryName = sapienOS === "windows" ? "sapien.exe" : "sapien";
  return { sapienOS, sapienArch, ext, binaryName };
}

// sapienVersion is the release version to fetch: SAPIEN_VERSION if set
// (e.g. "v0.3.0"), else this package's own version with a "v" prefix. Keep
// the npm package version in lockstep with sapien releases so a plain
// `npx @sapien-dev/sapien` needs no extra configuration.
function sapienVersion() {
  const v = process.env.SAPIEN_VERSION;
  if (v) return v.startsWith("v") ? v : "v" + v;
  return "v" + PKG.version;
}

function cacheDir(version) {
  const base =
    process.env.SAPIEN_CACHE_DIR ||
    path.join(os.homedir(), ".cache", "sapien-npm");
  return path.join(base, version);
}

function download(url, destPath) {
  return new Promise((resolve, reject) => {
    const req = https.get(
      url,
      { headers: { "User-Agent": "sapien-npm-wrapper" } },
      (res) => {
        if (
          res.statusCode >= 300 &&
          res.statusCode < 400 &&
          res.headers.location
        ) {
          res.resume();
          download(res.headers.location, destPath).then(resolve, reject);
          return;
        }
        if (res.statusCode !== 200) {
          res.resume();
          reject(new Error(url + ": HTTP " + res.statusCode));
          return;
        }
        const out = fs.createWriteStream(destPath);
        res.pipe(out);
        out.on("finish", () => out.close(resolve));
        out.on("error", reject);
      },
    );
    req.on("error", reject);
  });
}

function fetchText(url) {
  return new Promise((resolve, reject) => {
    https.get(
      url,
      { headers: { "User-Agent": "sapien-npm-wrapper" } },
      (res) => {
        if (
          res.statusCode >= 300 &&
          res.statusCode < 400 &&
          res.headers.location
        ) {
          res.resume();
          fetchText(res.headers.location).then(resolve, reject);
          return;
        }
        if (res.statusCode !== 200) {
          res.resume();
          reject(new Error(url + ": HTTP " + res.statusCode));
          return;
        }
        let body = "";
        res.setEncoding("utf8");
        res.on("data", (c) => (body += c));
        res.on("end", () => resolve(body));
      },
    );
  });
}

function sha256File(filePath) {
  const hash = crypto.createHash("sha256");
  hash.update(fs.readFileSync(filePath));
  return hash.digest("hex");
}

function extract(archivePath, destDir, ext) {
  fs.mkdirSync(destDir, { recursive: true });
  const args =
    ext === "zip" ? ["-xf", archivePath] : ["-xzf", archivePath];
  const res = spawnSync("tar", args, { cwd: destDir, stdio: "inherit" });
  if (res.status !== 0) {
    fail(
      "extracting " +
        archivePath +
        " failed (is `tar` on PATH? bsdtar, included with Windows 10 1803+, handles both tar.gz and zip)",
    );
  }
}

async function ensureBinary() {
  if (process.env.SAPIEN_BINARY) {
    return process.env.SAPIEN_BINARY;
  }

  const version = sapienVersion();
  const { sapienOS, sapienArch, ext, binaryName } = platformInfo();
  const dir = cacheDir(version);
  const binaryPath = path.join(dir, binaryName);

  if (fs.existsSync(binaryPath)) {
    return binaryPath;
  }

  fs.mkdirSync(dir, { recursive: true });

  const versionNum = version.replace(/^v/, "");
  const archiveName = `sapien_${versionNum}_${sapienOS}_${sapienArch}.${ext}`;
  const baseURL = `https://github.com/${REPO}/releases/download/${version}`;
  const archivePath = path.join(dir, archiveName);

  log(`downloading sapien ${version} (${sapienOS}/${sapienArch})...`);
  await download(`${baseURL}/${archiveName}`, archivePath);

  try {
    const checksums = await fetchText(`${baseURL}/checksums.txt`);
    const line = checksums
      .split("\n")
      .find((l) => l.trim().endsWith(archiveName));
    if (line) {
      const expected = line.trim().split(/\s+/)[0];
      const actual = sha256File(archivePath);
      if (expected.toLowerCase() !== actual.toLowerCase()) {
        fs.unlinkSync(archivePath);
        fail(`checksum mismatch for ${archiveName}`);
      }
    } else {
      log(`warning: no checksum entry for ${archiveName}, skipping verification`);
    }
  } catch (err) {
    log(`warning: could not verify checksum (${err.message})`);
  }

  extract(archivePath, dir, ext);
  fs.unlinkSync(archivePath);

  if (!fs.existsSync(binaryPath)) {
    fail(`extracted archive did not contain ${binaryName}`);
  }
  if (sapienOS !== "windows") {
    fs.chmodSync(binaryPath, 0o755);
  }

  log(`installed to ${binaryPath}`);
  return binaryPath;
}

async function main() {
  const args = process.argv.slice(2);

  // Invoked from package.json's "postinstall": best-effort pre-download so
  // the first real invocation doesn't pay the download cost. Never fails
  // the npm install (package.json runs this with `|| true`).
  if (args[0] === "--sapien-npm-postinstall") {
    try {
      await ensureBinary();
    } catch (err) {
      log(`postinstall pre-download skipped: ${err.message}`);
    }
    return;
  }

  let binaryPath;
  try {
    binaryPath = await ensureBinary();
  } catch (err) {
    fail(err.message);
  }

  const res = spawnSync(binaryPath, args, { stdio: "inherit" });
  if (res.error) {
    fail(res.error.message);
  }
  process.exit(res.status === null ? 1 : res.status);
}

main();
