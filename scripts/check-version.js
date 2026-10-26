#!/usr/bin/env node
"use strict";

// Fail-closed version-consistency guard (REPO-031). Asserts every derived
// location equals the source of truth (package.json "version"): the npm
// platform pins and lockfile, each Skill's metadata.version and
// metadata.min_version (SKL-001), the Python __version__, and the CHANGELOG's
// newest released heading. Wired into ci.yml (every push/PR) and release.yml
// (on the tagged commit, before anything is published; VER-008), so any drift,
// even from a hand edit that bypassed `npm version`, turns CI red and cannot be
// merged or published.
//
// Exit 0 = all in sync; exit 1 = drift (prints every offending location).

const path = require("path");
const { sourceVersion, derivedLocations } = require("./version-files");

const root = path.resolve(__dirname, "..");
const version = sourceVersion(root);

const problems = [];
// An absent Skill field is the one problem a resync cannot fix: sync-version
// rewrites values in place and never adds a field it did not find.
let skillFieldMissing = false;
let resyncable = false;
for (const loc of derivedLocations(root)) {
  for (const occ of loc.read()) {
    if (occ.value !== version) {
      if (occ.value === null && loc.label.startsWith("skills/")) skillFieldMissing = true;
      else resyncable = true;
      problems.push(`${loc.label} [${occ.where}]: ${occ.value === null ? "(missing)" : occ.value} != ${version}`);
    }
  }
}

if (problems.length) {
  console.error(`check-version: ${problems.length} location(s) out of sync with package.json (${version}):`);
  for (const p of problems) console.error(`  - ${p}`);
  if (skillFieldMissing) {
    console.error(
      [
        "A Skill field marked (missing) is not where the version tooling reads it, and sync-version will not add it.",
        "SKILL-SPEC 2.0 (SKL-001) keeps both fields in the frontmatter's `metadata` map, double-quoted:",
        "  metadata:",
        `    version: "${version}"`,
        `    min_version: "${version}"`,
        "A 1.x Skill has a top-level `version` and `requires.min_version` in a one-line JSON `metadata`;",
        "rewrite both in the form above (MIGRATION-2.0.md, step 8), then re-run.",
      ].join("\n")
    );
  }
  if (resyncable) {
    console.error("Run `npm version <patch|minor|major>` (or `node scripts/sync-version.js`) to resync.");
  }
  process.exit(1);
}

console.log(`check-version: OK — all derived locations match ${version}`);
