#!/usr/bin/env node
// Unified entry point for the Whale CLI.
// Detects platform, resolves the platform-specific optional dependency,
// and spawns the native binary with signal forwarding.

import { spawn } from "node:child_process";
import { existsSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const require = createRequire(import.meta.url);

const PLATFORM_PACKAGE_BY_TARGET = {
  "aarch64-apple-darwin": "@usewhale/whale-darwin-arm64",
  "x86_64-apple-darwin": "@usewhale/whale-darwin-x64",
  "x86_64-unknown-linux-musl": "@usewhale/whale-linux-x64",
  "aarch64-unknown-linux-musl": "@usewhale/whale-linux-arm64",
  "x86_64-pc-windows-msvc": "@usewhale/whale-win32-x64",
  "aarch64-pc-windows-msvc": "@usewhale/whale-win32-arm64",
};

function detectTargetTriple() {
  const { platform, arch } = process;

  switch (platform) {
    case "darwin":
      switch (arch) {
        case "x64":
          return "x86_64-apple-darwin";
        case "arm64":
          return "aarch64-apple-darwin";
      }
      break;
    case "linux":
      switch (arch) {
        case "x64":
          return "x86_64-unknown-linux-musl";
        case "arm64":
          return "aarch64-unknown-linux-musl";
      }
      break;
    case "win32":
      switch (arch) {
        case "x64":
          return "x86_64-pc-windows-msvc";
        case "arm64":
          return "aarch64-pc-windows-msvc";
      }
      break;
  }

  throw new Error(`Unsupported platform: ${platform} (${arch})`);
}

function resolvePlatformPackage() {
  const targetTriple = detectTargetTriple();
  const packageName = PLATFORM_PACKAGE_BY_TARGET[targetTriple];

  if (!packageName) {
    throw new Error(`No platform package for target triple: ${targetTriple}`);
  }

  // Resolve the platform-specific package root
  const packageJsonPath = require.resolve(`${packageName}/package.json`);
  const packageRoot = path.dirname(packageJsonPath);
  const binaryName = process.platform === "win32" ? "whale.exe" : "whale";
  const binaryPath = path.join(packageRoot, "vendor", binaryName);

  if (!existsSync(binaryPath)) {
    const updateCmd = `npm install -g @usewhale/whale@latest`;
    throw new Error(
      `Missing native binary at ${binaryPath}. ` +
        `Try reinstalling: ${updateCmd}`
    );
  }

  return { binaryPath, packageRoot };
}

function detectPackageManager() {
  const userAgent = process.env.npm_config_user_agent || "";
  if (/\bbun\//.test(userAgent)) return "bun";

  const execPath = process.env.npm_execpath || "";
  if (execPath.includes("bun")) return "bun";

  if (
    __dirname.includes(".bun/install/global") ||
    __dirname.includes(".bun\\install\\global")
  ) {
    return "bun";
  }

  return userAgent ? "npm" : null;
}

function getUpdatedPath(newDirs) {
  const pathSep = process.platform === "win32" ? ";" : ":";
  const existingPath = process.env.PATH || "";
  return [...newDirs, ...existingPath.split(pathSep).filter(Boolean)].join(
    pathSep
  );
}

async function main() {
  const { binaryPath, packageRoot } = resolvePlatformPackage();
  const additionalDirs = [];

  // Add path directory if present (for companion tools, etc.)
  const pathDir = path.join(packageRoot, "vendor", "path");
  if (existsSync(pathDir)) {
    additionalDirs.push(pathDir);
  }

  const updatedPath = getUpdatedPath(additionalDirs);
  const env = { ...process.env, PATH: updatedPath };

  // Mark as managed by npm/bun
  const pkgMgr = detectPackageManager();
  const mgrEnvVar =
    pkgMgr === "bun" ? "WHALE_MANAGED_BY_BUN" : "WHALE_MANAGED_BY_NPM";
  env[mgrEnvVar] = "1";
  env.WHALE_MANAGED_PACKAGE_ROOT = realpathSync(path.join(__dirname, ".."));

  // Use asynchronous spawn so Node can respond to signals (Ctrl-C, etc.)
  // and forward them to the child process.
  const child = spawn(binaryPath, process.argv.slice(2), {
    stdio: "inherit",
    env,
  });

  child.on("error", (err) => {
    console.error(err);
    process.exit(1);
  });

  // Forward termination signals to the child so it shuts down gracefully.
  const forwardSignal = (signal) => {
    if (child.killed) return;
    try {
      child.kill(signal);
    } catch {
      /* ignore */
    }
  };

  ["SIGINT", "SIGTERM", "SIGHUP"].forEach((sig) => {
    process.on(sig, () => forwardSignal(sig));
  });

  // Wait for child to exit and mirror its exit status.
  const childResult = await new Promise((resolve) => {
    child.on("exit", (code, signal) => {
      if (signal) {
        resolve({ type: "signal", signal });
      } else {
        resolve({ type: "code", exitCode: code ?? 1 });
      }
    });
  });

  if (childResult.type === "signal") {
    process.kill(process.pid, childResult.signal);
  } else {
    process.exit(childResult.exitCode);
  }
}

main().catch((err) => {
  console.error("Failed to start whale:", err.message);
  process.exit(1);
});
