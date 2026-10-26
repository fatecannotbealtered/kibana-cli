#!/usr/bin/env node
// check-spec.js — fail-closed CI guard that this tool's vendored spec assets
// and its copy of the shared library have not drifted from the
// ai-native-cli-spec version pinned in .agent/SPEC_VERSION. It is the
// enforcement half of "managed in one place": a tool can re-sync, but it can
// never quietly fork the specs, the contract or the write gate.
//
// Two layers:
//   1) offline (STRICT, always runs): every file in the vendored library
//      directory (internal/aicli/ or <pkg>/_aicli/) matches the sha256 in the
//      vendored MANIFEST.json, no file is missing, no extra file exists, the
//      manifest's spec_version matches the pin and contract/contract.json, and
//      every vendored spec file is present.
//   2) remote (best effort): every vendored file equals the spec at the pinned
//      tag, and no stale file remains. A failed download DEGRADES to a warning
//      (a GitHub hiccup must not flake CI); layer 1 is the offline backstop.
//      With --from the comparison is strict.
//
// Usage (run from the tool's repository root):
//   node scripts/check-spec.js                  # offline + remote (best effort)
//   node scripts/check-spec.js --from <path>    # compare with a local spec checkout or release .tar.gz (strict)
//   node scripts/check-spec.js --local-only     # offline layer only
//   node scripts/check-spec.js --lang go|py     # only needed when the repo has both go.mod and pyproject.toml
//
// Exit 0 = in sync; 1 = drift or a broken vendored copy; 2 = usage error.

"use strict";

const fs = require("fs");
const spec = require("./spec-files.js");

const USAGE = "usage: node scripts/check-spec.js [--from <spec checkout | archive.tar.gz>] [--local-only] [--lang go|py]";
const FIX = "Run: node scripts/sync-spec.js";

function fail(code, msg) {
  console.error(msg);
  process.exit(code);
}

function parseArgs(argv) {
  const out = { from: "", localOnly: false, lang: "" };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const value = () => {
      const v = argv[++i];
      if (v === undefined || v.startsWith("--")) fail(2, `[spec] ${a} needs a value\n${USAGE}`);
      return v;
    };
    if (a === "--from") out.from = value();
    else if (a === "--local-only") out.localOnly = true;
    else if (a === "--lang") out.lang = value();
    else if (a === "-h" || a === "--help") {
      console.log(USAGE);
      process.exit(0);
    } else fail(2, `[spec] unknown argument ${a}\n${USAGE}`);
  }
  return out;
}

// checkOffline verifies the vendored library against its own MANIFEST.json and
// the pin. Returns a list of problems (empty = OK) and the parsed manifest.
function checkOffline(root, layout, pin) {
  const problems = [];
  const libDir = layout.libDir;
  const libAbs = spec.toAbs(root, libDir);
  try {
    spec.assertNoSymlink(root, libDir);
  } catch (e) {
    return { problems: [e.message], manifest: null };
  }
  if (!fs.existsSync(libAbs) || !fs.statSync(libAbs).isDirectory()) {
    return { problems: [`MISSING the vendored library directory ${libDir}/. ${FIX}`], manifest: null };
  }
  const mbuf = spec.readOrNull(spec.toAbs(root, `${libDir}/${spec.MANIFEST}`));
  if (!mbuf) return { problems: [`MISSING ${libDir}/${spec.MANIFEST}. ${FIX}`], manifest: null };
  let manifest;
  try {
    manifest = spec.parseManifest(mbuf, `${libDir}/${spec.MANIFEST}`);
  } catch (e) {
    return { problems: [`${e.message}. ${FIX}`], manifest: null };
  }

  let present;
  try {
    present = spec.listFiles(libAbs).filter((rel) => !spec.isJunk(rel));
  } catch (e) {
    return { problems: [e.message], manifest };
  }
  const presentSet = new Set(present);
  for (const [rel, want] of Object.entries(manifest.files)) {
    if (!presentSet.has(rel)) {
      problems.push(`MISSING ${libDir}/${rel} (listed in ${spec.MANIFEST}). ${FIX}`);
    } else if (spec.sha256(fs.readFileSync(spec.toAbs(libAbs, rel))) !== want) {
      problems.push(`MODIFIED ${libDir}/${rel}: does not match ${spec.MANIFEST}. Vendored code is never edited in a tool; ${FIX}`);
    }
  }
  for (const rel of present) {
    if (rel !== spec.MANIFEST && !(rel in manifest.files)) {
      problems.push(`EXTRA ${libDir}/${rel}: not part of the library. Move tool code out of ${libDir}/, or ${FIX}`);
    }
  }

  const pinned = spec.semverCore(pin);
  const shipped = spec.semverCore(manifest.spec_version);
  if (pinned && shipped && pinned !== shipped) {
    problems.push(`.agent/SPEC_VERSION pins ${pin} but ${libDir}/ is from spec ${manifest.spec_version}. ${FIX}`);
  }
  const contract = spec.readOrNull(spec.toAbs(root, spec.CONTRACT[0]));
  if (contract) {
    let v = null;
    try {
      v = JSON.parse(contract.toString("utf8")).spec_version;
    } catch (_) {
      problems.push(`${spec.CONTRACT[0]} is not valid JSON. ${FIX}`);
    }
    if (v !== null && v !== manifest.spec_version) {
      problems.push(`${spec.CONTRACT[0]} is spec ${v} but ${libDir}/ is spec ${manifest.spec_version}. ${FIX}`);
    }
  }
  for (const rel of [...spec.AGENT_SPECS, ...spec.CONTRACT, ...spec.TOOLING]) {
    if (!fs.existsSync(spec.toAbs(root, rel))) problems.push(`MISSING vendored file ${rel}. ${FIX}`);
  }
  const schemasAbs = spec.toAbs(root, spec.SCHEMAS_DIR);
  const schemas = fs.existsSync(schemasAbs) ? fs.readdirSync(schemasAbs).filter((n) => n.endsWith(".json")) : [];
  if (!schemas.length) problems.push(`MISSING the vendored JSON Schemas in ${spec.SCHEMAS_DIR}/. ${FIX}`);
  return { problems, manifest };
}

// checkRemote compares every vendored file with the spec source and reports
// stale files. Returns a list of drift lines.
function checkRemote(root, layout, source, label) {
  const plan = spec.planSync(source, layout);
  const contents = spec.readPlan(source, plan);
  const drift = [];
  for (const e of plan.entries) {
    const local = spec.readOrNull(spec.toAbs(root, e.dest));
    if (!local) drift.push(`MISSING ${e.dest} (shipped by ${label}). ${FIX}`);
    else if (!spec.normalizeText(local).equals(contents.get(e.dest))) drift.push(`DRIFT: ${e.dest} differs from ${label}. ${FIX}`);
  }
  for (const rel of spec.findStale(root, plan.managed)) {
    drift.push(`STALE ${rel}: not shipped by ${label}. ${FIX}`);
  }
  return drift;
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const root = process.cwd();
  const pin = spec.readPin(root);
  if (!pin && !args.from) fail(1, "[spec] no .agent/SPEC_VERSION pin found");
  if (pin && !spec.isSafeRef(pin)) fail(1, `[spec] .agent/SPEC_VERSION holds ${JSON.stringify(pin)}, which is not a spec tag such as v2.0.0`);

  let layout;
  try {
    layout = spec.detectLayout(root, args.lang);
  } catch (e) {
    fail(1, `[spec] ${e.message}`);
  }

  const offline = checkOffline(root, layout, pin);
  if (offline.problems.length) {
    for (const p of offline.problems) console.error(`[spec] ${p}`);
    fail(1, `[spec] FAILED: the vendored spec copy is broken (${offline.problems.length} problem(s))`);
  }
  const libCount = Object.keys(offline.manifest.files).length;
  console.log(`[spec] OK (offline): ${layout.libDir}/ matches its ${spec.MANIFEST} (${libCount} files, spec ${offline.manifest.spec_version})`);
  if (args.localOnly) return;

  const label = args.from ? `--from ${args.from}` : `template@${pin}`;
  let source;
  try {
    source = await spec.openSource({ from: args.from, ref: pin });
  } catch (e) {
    if (args.from) fail(1, `[spec] ${e.message}`);
    // 404 means the pin names no published version: a wrong pin, not a hiccup.
    if (e.status === 404) fail(1, `[spec] ${label} does not exist (${e.message}); fix .agent/SPEC_VERSION`);
    console.warn(`[spec] WARN: could not fetch ${label} (${e.message}); skipped the remote comparison`);
    return;
  }
  let drift;
  try {
    drift = checkRemote(root, layout, source, label);
  } catch (e) {
    fail(1, `[spec] ${e.message}`);
  }
  if (drift.length) {
    for (const d of drift) console.error(`[spec] ${d}`);
    fail(1, `[spec] FAILED: ${drift.length} vendored file(s) drifted from ${label}`);
  }
  console.log(`[spec] OK: every vendored file matches ${label}`);
}

main().catch((e) => fail(1, `[spec] ${(e && e.stack) || String(e)}`));
