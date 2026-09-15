#!/usr/bin/env node
"use strict";

// This file is the whole npm package. `@digicli/cli` itself ships no binary:
// the real Go executables live in one @digicli/<platform> package each, listed
// as optionalDependencies and gated by "os"/"cpu", so npm installs exactly the
// one that matches the machine and skips the rest. All this shim does is find
// it and hand over the terminal.

const { spawnSync } = require("node:child_process");

const target = `${process.platform}-${process.arch}`;
const exe = process.platform === "win32" ? "digicli.exe" : "digicli";

let binary;
try {
	binary = require.resolve(`@digicli/${target}/bin/${exe}`);
} catch {
	console.error(
		`digicli: no binary installed for ${target}.\n\n` +
			"If this is a supported platform (macOS, Linux, or Windows on x64 or\n" +
			"arm64), the optional dependency was skipped — reinstall with:\n\n" +
			"  npm install -g @digicli/cli --force\n\n" +
			"Otherwise build from source: https://github.com/10txn/digicli",
	);
	process.exit(1);
}

// Bubble Tea puts the terminal in raw mode, where ctrl+c arrives as a keystroke
// rather than a signal, and digicli uses it to interrupt a reply. Before and
// after raw mode the signal does land here, though, and the default action
// would kill this process while the child still owns the screen. Ignoring it
// leaves the child as the only thing that decides what ctrl+c means.
process.on("SIGINT", () => {});

const result = spawnSync(binary, process.argv.slice(2), { stdio: "inherit" });

if (result.error) {
	console.error(`digicli: could not run ${binary}: ${result.error.message}`);
	process.exit(1);
}

// status is null when the child was killed by a signal.
process.exit(result.status === null ? 1 : result.status);
