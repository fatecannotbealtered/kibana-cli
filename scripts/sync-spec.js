#!/usr/bin/env node
// sync-spec.js — vendor the ai-native-cli-spec assets this tool consumes, at the
// spec version pinned in .agent/SPEC_VERSION:
//
//   .agent/*.md               the specs (AGENT, CLI-SPEC, SEC-SPEC, SKILL-SPEC, VERIFY-SPEC, + _zh)
//   contract/contract.json    the machine contract
//   contract/schemas/*.json   the JSON Schemas
//   scripts/{spec-files,sync-spec,check-spec}.js   this tooling
//   internal/aicli/           the Go library (Go tools), or
//   <pkg>/_aicli/             the Python library (Python tools)
//
// Library files are the ones the spec's MANIFEST.json lists; each is checked
// against its sha256 before anything is written. Files the spec no longer ships
// are removed from contract/schemas/ and from the library directory. There is
// no code generation step: the library carries its generated contract module.
//
// Usage (run from the tool's repository root):
//   node scripts/sync-spec.js                      # the GitHub archive of the tag in .agent/SPEC_VERSION
//   node scripts/sync-spec.js --version v2.0.0     # another tag; rewrites .agent/SPEC_VERSION on success
//   node scripts/sync-spec.js --from <path>        # a local ai-native-cli-spec checkout or release .tar.gz
//   node scripts/sync-spec.js --lang go|py         # only needed when the repo has both go.mod and pyproject.toml
//
// Tools never edit the vendored files; scripts/check-spec.js fails CI when they
// drift from the pinned version.

"use strict";

const fs = require("fs");
const path = require("path");
const { spawnSync } = require("child_process");
const spec = require("./spec-files.js");

const USAGE =
  "usage: node scripts/sync-spec.js [--version <tag>] [--from <spec checkout | archive.tar.gz>] [--lang go|py]";
const REEXEC_ENV = "AICLI_SYNC_SPEC_REEXEC";

function fail(code, msg) {
  console.error(`sync-spec: ${msg}`);
  process.exit(code);
}

function parseArgs(argv) {
  const out = { from: "", version: "", lang: "" };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const value = () => {
      const v = argv[++i];
      if (v === undefined || v.startsWith("--")) fail(2, `${a} needs a value\n${USAGE}`);
      return v;
    };
    if (a === "--from") out.from = value();
    else if (a === "--version") out.version = value();
    else if (a === "--lang") out.lang = value();
    else if (a === "-h" || a === "--help") {
      console.log(USAGE);
      process.exit(0);
    } else fail(2, `unknown argument ${a}\n${USAGE}`);
  }
  return out;
}

function semverCompare(a, b) {
  const pa = /^v(\d+)\.(\d+)\.(\d+)/.exec(a);
  const pb = /^v(\d+)\.(\d+)\.(\d+)/.exec(b);
  if (!pa || !pb) return 0;
  for (let i = 1; i <= 3; i++) {
    if (Number(pa[i]) !== Number(pb[i])) return Number(pa[i]) - Number(pb[i]);
  }
  return 0;
}

// goRequires parses the require directives of a go.mod (single-line and block
// form); with directOnly, "// indirect" requirements are skipped.
function goRequires(text, directOnly) {
  const out = new Map();
  let inBlock = false;
  for (const raw of text.split(/\r?\n/)) {
    const indirect = /\/\/\s*indirect\b/.test(raw);
    const line = raw.replace(/\/\/.*$/, "").trim();
    if (!line) continue;
    let m = null;
    if (inBlock) {
      if (line === ")") {
        inBlock = false;
        continue;
      }
      m = /^(\S+)\s+(v\S+)$/.exec(line);
    } else if (/^require\s*\($/.test(line)) {
      inBlock = true;
      continue;
    } else {
      m = /^require\s+(\S+)\s+(v\S+)$/.exec(line);
    }
    if (m && !(directOnly && indirect)) out.set(m[1], m[2]);
  }
  return out;
}

// Notes that need the maintainer, printed after a successful sync.
function notes(root, layout, source) {
  const out = [];
  if (layout.lang === "go") {
    const libMod = source.read(`${spec.LIBRARIES.go.src}/go.mod`);
    const toolMod = spec.readOrNull(path.join(root, "go.mod"));
    if (libMod && toolMod) {
      const have = goRequires(toolMod.toString("utf8"), false);
      const need = [];
      // Indirect requirements too: the library pins some of them above what its direct
      // dependencies ask for (golang.org/x/net for govulncheck), and a tool resolving
      // its own graph would otherwise fall back to the older, flagged versions.
      for (const [mod, ver] of goRequires(libMod.toString("utf8"), false)) {
        if (!have.has(mod) || semverCompare(have.get(mod), ver) < 0) need.push(`${mod}@${ver}`);
      }
      if (need.length) {
        out.push(`the vendored library needs ${need.join(", ")}; run: go get ${need.join(" ")} && go mod tidy`);
      }
    }
    if (fs.existsSync(path.join(root, "internal", "contract", "contract_gen.go"))) {
      out.push("internal/contract/contract_gen.go is a 1.x leftover: the library carries the contract now (aicli); delete it once nothing imports it");
    }
    if (fs.existsSync(path.join(root, "cmd", "fcc_guard_test.go"))) {
      out.push("cmd/fcc_guard_test.go is a 1.x leftover: the conformance suite and the coverage check replace it; delete it");
    }
  } else {
    const pkg = layout.libDir.slice(0, -(spec.PY_LIB_NAME.length + 1));
    if (fs.existsSync(spec.toAbs(root, `${pkg}/contract_gen.py`))) {
      out.push(`${pkg}/contract_gen.py is a 1.x leftover: the library carries the contract now (_aicli); delete it once nothing imports it`);
    }
    if (fs.existsSync(path.join(root, "tests", "test_fcc_guard.py"))) {
      out.push("tests/test_fcc_guard.py is a 1.x leftover: the conformance suite and the coverage check replace it; delete it");
    }
  }
  if (fs.existsSync(path.join(root, "scripts", "gen-contract.js"))) {
    out.push("scripts/gen-contract.js is no longer used by tools (the spec repository generates the library's contract module); delete it");
  }
  return out;
}

function samePath(a, b) {
  try {
    const ra = fs.realpathSync.native(a);
    const rb = fs.realpathSync.native(b);
    return process.platform === "win32" ? ra.toLowerCase() === rb.toLowerCase() : ra === rb;
  } catch (_) {
    return false;
  }
}

function removeEmptyDirs(absDir) {
  if (!fs.existsSync(absDir)) return;
  for (const ent of fs.readdirSync(absDir, { withFileTypes: true })) {
    if (ent.isDirectory()) {
      const sub = path.join(absDir, ent.name);
      removeEmptyDirs(sub);
      if (!fs.readdirSync(sub).length) fs.rmdirSync(sub);
    }
  }
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const root = process.cwd();
  const ref = args.version || spec.readPin(root);
  if (!ref && !args.from) {
    fail(2, "no spec version: create .agent/SPEC_VERSION (for example v2.0.0) or pass --version <tag> or --from <spec checkout>");
  }
  if (args.version && !spec.isSafeRef(args.version)) fail(2, `--version ${JSON.stringify(args.version)} is not a plain tag`);

  let layout;
  try {
    layout = spec.detectLayout(root, args.lang);
  } catch (e) {
    fail(2, e.message);
  }

  const self = path.resolve(__filename);
  const ownTooling = ["scripts/sync-spec.js", "scripts/spec-files.js"].map((rel) => spec.toAbs(root, rel));
  const runningVendoredCopy = samePath(self, ownTooling[0]);
  const toolingBefore = ownTooling.map((abs) => spec.readOrNull(abs));

  let source;
  let plan;
  let contents;
  try {
    source = await spec.openSource({ from: args.from, ref });
    plan = spec.planSync(source, layout);
    contents = spec.readPlan(source, plan);
    for (const e of plan.entries) spec.assertNoSymlink(root, e.dest);
  } catch (e) {
    fail(1, e.message);
  }

  let changed = 0;
  for (const e of plan.entries) {
    const abs = spec.toAbs(root, e.dest);
    const data = contents.get(e.dest);
    const current = spec.readOrNull(abs);
    if (current && spec.normalizeText(current).equals(data)) continue;
    fs.mkdirSync(path.dirname(abs), { recursive: true });
    fs.writeFileSync(abs, data);
    changed++;
    console.log(`synced ${e.dest}`);
  }

  const stale = spec.findStale(root, plan.managed);
  for (const rel of stale) {
    fs.unlinkSync(spec.toAbs(root, rel));
    console.log(`removed ${rel} (not shipped by this spec version)`);
  }
  removeEmptyDirs(spec.toAbs(root, layout.libDir));

  if (args.version) {
    fs.mkdirSync(path.join(root, ".agent"), { recursive: true });
    fs.writeFileSync(spec.toAbs(root, spec.SPEC_VERSION_FILE), `${args.version.trim()}\n`);
    console.log(`pinned ${spec.SPEC_VERSION_FILE} -> ${args.version.trim()}`);
  }

  for (const n of notes(root, layout, source)) console.error(`sync-spec: note: ${n}`);
  const libCount = Object.keys(plan.manifest.files).length;
  console.log(
    `sync-spec: ${plan.entries.length} files from ${source.label}: ` +
      `${changed} changed, ${stale.length} removed; ${spec.LIBRARIES[layout.lang].label} library ` +
      `${plan.manifest.spec_version} -> ${layout.libDir}/ (${libCount} files)`,
  );

  // If this run replaced the sync tooling itself, the new version may vendor
  // more than the old one knew about: run it once more so a spec upgrade
  // completes in one command.
  const toolingAfter = ownTooling.map((abs) => spec.readOrNull(abs));
  const toolingChanged = toolingBefore.some((b, i) => !b || !toolingAfter[i] || !spec.normalizeText(b).equals(toolingAfter[i]));
  if (runningVendoredCopy && toolingChanged && process.env[REEXEC_ENV] !== "1") {
    console.log("sync-spec: the sync tooling changed; running the new version");
    const r = spawnSync(process.execPath, [self, ...process.argv.slice(2)], {
      stdio: "inherit",
      env: Object.assign({}, process.env, { [REEXEC_ENV]: "1" }),
    });
    process.exit(r.status === null ? 1 : r.status);
  }
}

main().catch((e) => fail(1, (e && e.stack) || String(e)));
