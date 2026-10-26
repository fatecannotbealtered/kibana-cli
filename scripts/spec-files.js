// spec-files.js — the single registry of what a tool vendors from the
// ai-native-cli-spec repository, plus the plumbing its three consumers share:
// sync-spec.js (writer), check-spec.js (verifier) and, in the spec repository
// only, build-manifest.js. Adding a file class here covers sync and check at
// once, so the writer and the verifier can never disagree about what is vendored.
//
// What a tool vendors (source path in the spec repo -> path in the tool repo):
//   template/common/.agent/*.md            -> .agent/*.md              (specs, bilingual)
//   template/common/contract/contract.json -> contract/contract.json
//   template/common/contract/schemas/*.json-> contract/schemas/*.json  (directory is managed)
//   template/common/scripts/{spec-files,sync-spec,check-spec}.js -> scripts/
//   template/go/aicli/<MANIFEST files>     -> internal/aicli/          (Go tools; directory is managed)
//   template/python/aicli/<MANIFEST files> -> <pkg>/_aicli/            (Python tools; directory is managed)
//
// The library directories carry MANIFEST.json ({spec_version, files: {path:
// sha256}}), written by build-manifest.js in the spec repo. The sha256 is taken
// over the file bytes with CRLF normalised to LF, so a Windows checkout with
// autocrlf hashes the same as Linux. A "managed" directory belongs to the spec:
// sync removes files the spec no longer ships and check reports extra files.
//
// Tools never hand-edit vendored files; they bump .agent/SPEC_VERSION and re-run
// scripts/sync-spec.js. Node >= 16, standard library only.

"use strict";

const fs = require("fs");
const path = require("path");
const crypto = require("crypto");
const zlib = require("zlib");
const https = require("https");

const REPO = "fatecannotbealtered/ai-native-cli-spec";
const COMMON_SRC = "template/common";
const SPEC_VERSION_FILE = ".agent/SPEC_VERSION";

// ---------------------------------------------------------------- registry

const AGENT_SPECS = [
  ".agent/AGENT.md",
  ".agent/AGENT_zh.md",
  ".agent/CLI-SPEC.md",
  ".agent/CLI-SPEC_zh.md",
  ".agent/SEC-SPEC.md",
  ".agent/SEC-SPEC_zh.md",
  ".agent/SKILL-SPEC.md",
  ".agent/SKILL-SPEC_zh.md",
  ".agent/VERIFY-SPEC.md",
  ".agent/VERIFY-SPEC_zh.md",
];
const CONTRACT = ["contract/contract.json"];
// Every *.json directly inside this directory is vendored.
const SCHEMAS_DIR = "contract/schemas";
// The tooling that keeps a tool aligned. gen-contract.js and build-manifest.js
// run in the spec repository only and are not vendored.
const TOOLING = ["scripts/spec-files.js", "scripts/sync-spec.js", "scripts/check-spec.js"];

const MANIFEST = "MANIFEST.json";
const LIBRARIES = {
  go: { src: "template/go/aicli", label: "Go" },
  py: { src: "template/python/aicli", label: "Python" },
};
const GO_LIB_DIR = "internal/aicli";
const PY_LIB_NAME = "_aicli";

// Files under a library source directory that are NOT vendored: tests and
// their data, the library's own module/project files, caches, dotfiles.
const LIB_EXCLUDED_DIRS = new Set(["testdata", "tests", "test", "__pycache__", "build", "dist", "node_modules"]);
const LIB_EXCLUDED_FILES = new Set([
  MANIFEST,
  "go.mod",
  "go.sum",
  "go.work",
  "go.work.sum",
  "pyproject.toml",
  "setup.py",
  "setup.cfg",
  "requirements.txt",
  "conftest.py",
]);

function isLibraryFile(rel) {
  const parts = rel.split("/");
  const base = parts[parts.length - 1];
  if (parts.some((p) => p.startsWith("."))) return false;
  if (parts.slice(0, -1).some((p) => LIB_EXCLUDED_DIRS.has(p) || p.endsWith(".egg-info"))) return false;
  if (LIB_EXCLUDED_FILES.has(base)) return false;
  if (base.endsWith("_test.go")) return false;
  if (/^test_.*\.py$/.test(base) || base.endsWith("_test.py")) return false;
  if (/\.py[cod]$/.test(base)) return false;
  return true;
}

// Junk a running tool or OS leaves inside a vendored directory. Ignored by the
// extra-file check and never deleted by sync.
function isJunk(rel) {
  const parts = rel.split("/");
  const base = parts[parts.length - 1];
  return (
    parts.slice(0, -1).includes("__pycache__") || /\.py[cod]$/.test(base) || base === ".DS_Store" || base === "Thumbs.db"
  );
}

// ---------------------------------------------------------------- hashing

function byteOrder(a, b) {
  return a < b ? -1 : a > b ? 1 : 0;
}

// normalizeText rewrites CRLF to LF on the raw bytes (latin1 maps bytes 1:1, so
// nothing else changes). Used for hashing, comparing and writing.
function normalizeText(buf) {
  if (!buf.includes(13)) return buf;
  return Buffer.from(buf.toString("latin1").replace(/\r\n/g, "\n"), "latin1");
}

function sha256(buf) {
  return crypto.createHash("sha256").update(normalizeText(buf)).digest("hex");
}

// A relative path inside a vendored directory: forward slashes, no empty, "."
// or ".." segment, no segment starting with a dot, conservative characters.
// Manifest entries are checked with it so a manifest can never point outside
// the directory it describes.
const SAFE_REL = /^[A-Za-z0-9_][A-Za-z0-9_.+-]*(\/[A-Za-z0-9_][A-Za-z0-9_.+-]*)*$/;
function assertSafeRel(rel, label) {
  if (typeof rel !== "string" || !SAFE_REL.test(rel)) {
    throw new Error(`${label || "path"}: unsafe relative path ${JSON.stringify(rel)}`);
  }
}

function toAbs(root, rel) {
  return path.join(root, ...rel.split("/"));
}

function readOrNull(abs) {
  try {
    return fs.readFileSync(abs);
  } catch (e) {
    if (e.code === "ENOENT" || e.code === "ENOTDIR") return null;
    throw e;
  }
}

// listFiles returns every regular file under absDir as sorted forward-slash
// paths relative to absDir. A symlink is an error: vendored code is copied,
// never linked.
function listFiles(absDir) {
  const out = [];
  function walk(dir, prefix) {
    for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
      const rel = prefix ? `${prefix}/${ent.name}` : ent.name;
      const abs = path.join(dir, ent.name);
      if (ent.isSymbolicLink()) throw new Error(`${abs} is a symlink; vendored files must be regular files`);
      if (ent.isDirectory()) walk(abs, rel);
      else if (ent.isFile()) out.push(rel);
    }
  }
  walk(absDir, "");
  return out.sort(byteOrder);
}

// assertNoSymlink refuses to write through or check a path whose existing
// components (below root) include a symlink, so a vendored directory cannot be
// redirected outside the repository.
function assertNoSymlink(root, rel) {
  let cur = root;
  for (const part of rel.split("/")) {
    cur = path.join(cur, part);
    let st;
    try {
      st = fs.lstatSync(cur);
    } catch (e) {
      if (e.code === "ENOENT") return;
      throw e;
    }
    if (st.isSymbolicLink()) throw new Error(`${cur} is a symlink; vendored paths must be real files and directories`);
  }
}

// ---------------------------------------------------------------- manifests

function goPackageName(text) {
  const m = /^\s*package\s+([A-Za-z_][A-Za-z0-9_]*)/m.exec(text.replace(/\/\*[\s\S]*?\*\//g, ""));
  return m ? m[1] : null;
}

// buildManifest hashes every library file under absLibRoot. It also enforces
// the shape that vendoring relies on: Go is one flat package (it is copied to
// internal/aicli/ without import rewriting); Python is a package with
// __init__.py at its root (it is copied to <pkg>/_aicli/).
function buildManifest(absLibRoot, specVersion, lang) {
  const files = listFiles(absLibRoot).filter(isLibraryFile);
  if (!files.length) throw new Error(`${absLibRoot}: no library files to vendor`);
  for (const f of files) assertSafeRel(f, absLibRoot);
  if (lang === "go") {
    const goFiles = files.filter((f) => f.endsWith(".go"));
    const nested = goFiles.filter((f) => f.includes("/"));
    if (nested.length) {
      throw new Error(`${absLibRoot}: the Go library must be one flat package; nested .go files: ${nested.join(", ")}`);
    }
    const pkgs = new Set(goFiles.map((f) => goPackageName(fs.readFileSync(path.join(absLibRoot, f), "utf8"))));
    if (pkgs.size !== 1 || pkgs.has(null)) {
      throw new Error(`${absLibRoot}: all non-test .go files must declare the same package (found: ${[...pkgs].join(", ")})`);
    }
  } else if (lang === "py" && !files.includes("__init__.py")) {
    throw new Error(`${absLibRoot}: the Python library needs __init__.py at its root`);
  }
  const out = { spec_version: specVersion, files: {} };
  for (const f of files) out.files[f] = sha256(fs.readFileSync(toAbs(absLibRoot, f)));
  return out;
}

// serializeManifest is deterministic (sorted paths, 2-space indent, LF, final
// newline) so --check is a byte compare.
function serializeManifest(m) {
  const files = {};
  for (const k of Object.keys(m.files).sort(byteOrder)) files[k] = m.files[k];
  return JSON.stringify({ spec_version: m.spec_version, files }, null, 2) + "\n";
}

function parseManifest(buf, label) {
  let m;
  try {
    m = JSON.parse(buf.toString("utf8"));
  } catch (e) {
    throw new Error(`${label}: not valid JSON (${e.message})`);
  }
  if (!m || typeof m !== "object" || Array.isArray(m)) throw new Error(`${label}: not a JSON object`);
  const keys = Object.keys(m).sort(byteOrder).join(",");
  if (keys !== "files,spec_version") throw new Error(`${label}: keys must be exactly spec_version and files (found ${keys})`);
  if (typeof m.spec_version !== "string" || !m.spec_version) throw new Error(`${label}: spec_version must be a non-empty string`);
  if (!m.files || typeof m.files !== "object" || Array.isArray(m.files)) throw new Error(`${label}: files must be an object`);
  for (const [rel, hash] of Object.entries(m.files)) {
    assertSafeRel(rel, label);
    if (rel === MANIFEST) throw new Error(`${label}: lists itself`);
    if (typeof hash !== "string" || !/^[0-9a-f]{64}$/.test(hash)) throw new Error(`${label}: ${rel} has no sha256`);
  }
  return m;
}

// ---------------------------------------------------------------- sources
//
// A source is the spec repository at one version: a local checkout (DirSource)
// or a release archive (MapSource, from a .tar.gz file or the GitHub archive of
// the pinned ref). Paths are relative to the spec repository root.

class DirSource {
  constructor(root) {
    this.root = path.resolve(root);
    this.label = this.root;
  }
  read(rel) {
    const abs = toAbs(this.root, rel);
    try {
      if (!fs.statSync(abs).isFile()) return null;
    } catch (e) {
      if (e.code === "ENOENT" || e.code === "ENOTDIR") return null;
      throw e;
    }
    return fs.readFileSync(abs);
  }
  list(relDir) {
    try {
      return fs
        .readdirSync(toAbs(this.root, relDir), { withFileTypes: true })
        .filter((d) => d.isFile())
        .map((d) => d.name)
        .sort(byteOrder);
    } catch (e) {
      if (e.code === "ENOENT" || e.code === "ENOTDIR") return [];
      throw e;
    }
  }
}

class MapSource {
  constructor(files, label) {
    this.files = files;
    this.label = label;
  }
  read(rel) {
    return this.files.has(rel) ? this.files.get(rel) : null;
  }
  list(relDir) {
    const prefix = relDir.replace(/\/+$/, "") + "/";
    const out = [];
    for (const k of this.files.keys()) {
      if (k.startsWith(prefix) && !k.slice(prefix.length).includes("/")) out.push(k.slice(prefix.length));
    }
    return out.sort(byteOrder);
  }
}

// A git ref we are willing to put into a URL: tags such as v2.0.0, branch
// names, commit ids. No "..", no leading dash, no URL metacharacters.
function isSafeRef(ref) {
  return typeof ref === "string" && /^[A-Za-z0-9][A-Za-z0-9._\/-]*$/.test(ref) && !ref.split("/").includes("..");
}

function archiveUrl(ref) {
  if (!isSafeRef(ref)) throw new Error(`refusing spec version ${JSON.stringify(ref)}: not a plain tag, branch or commit`);
  return `https://codeload.github.com/${REPO}/tar.gz/${ref}`;
}

function fetchBuffer(url, opts) {
  const o = Object.assign({ redirects: 5, maxBytes: 64 * 1024 * 1024, timeoutMs: 60000 }, opts || {});
  return new Promise((resolve, reject) => {
    const req = https.get(url, { headers: { "User-Agent": "ai-native-cli-spec-sync" }, timeout: o.timeoutMs }, (res) => {
      const status = res.statusCode || 0;
      if ([301, 302, 303, 307, 308].includes(status) && res.headers.location) {
        res.resume();
        if (o.redirects <= 0) return reject(new Error(`GET ${url}: too many redirects`));
        const next = new URL(res.headers.location, url);
        if (next.protocol !== "https:") return reject(new Error(`GET ${url}: refusing a redirect to ${next.protocol}`));
        return resolve(fetchBuffer(next.href, Object.assign({}, o, { redirects: o.redirects - 1 })));
      }
      if (status !== 200) {
        res.resume();
        return reject(Object.assign(new Error(`GET ${url} -> HTTP ${status}`), { status }));
      }
      const chunks = [];
      let size = 0;
      res.on("data", (c) => {
        size += c.length;
        if (size > o.maxBytes) req.destroy(new Error(`GET ${url}: response larger than ${o.maxBytes} bytes`));
        else chunks.push(c);
      });
      res.on("end", () => resolve(Buffer.concat(chunks)));
      res.on("error", reject);
    });
    req.on("timeout", () => req.destroy(new Error(`GET ${url}: timed out after ${o.timeoutMs} ms`)));
    req.on("error", reject);
  });
}

// ---- minimal tar.gz reader (ustar + pax + GNU long names), regular files only

function cstring(buf) {
  const i = buf.indexOf(0);
  return buf.subarray(0, i < 0 ? buf.length : i).toString("utf8");
}

function parseOctal(buf) {
  if (buf[0] & 0x80) throw new Error("tar: base-256 numbers are not supported");
  const s = cstring(buf).trim();
  if (!/^[0-7]*$/.test(s)) throw new Error(`tar: bad octal field ${JSON.stringify(s)}`);
  return s ? parseInt(s, 8) : 0;
}

function parsePax(body) {
  const out = {};
  let pos = 0;
  while (pos < body.length) {
    const sp = body.indexOf(0x20, pos);
    if (sp < 0) break;
    const len = parseInt(body.subarray(pos, sp).toString("latin1"), 10);
    if (!Number.isFinite(len) || len <= 0 || pos + len > body.length) break;
    const rec = body.subarray(sp + 1, pos + len - 1).toString("utf8");
    const eq = rec.indexOf("=");
    if (eq > 0) out[rec.slice(0, eq)] = rec.slice(eq + 1);
    pos += len;
  }
  return out;
}

function parseTarGz(buf) {
  let tar;
  try {
    tar = zlib.gunzipSync(buf, { maxOutputLength: 512 * 1024 * 1024 });
  } catch (e) {
    throw new Error(`not a .tar.gz archive (${e.message})`);
  }
  const files = new Map();
  let off = 0;
  let paxPath = null;
  let longName = null;
  while (off + 512 <= tar.length) {
    const h = tar.subarray(off, off + 512);
    if (h.every((b) => b === 0)) break;
    let sum = 0;
    for (let i = 0; i < 512; i++) sum += i >= 148 && i < 156 ? 0x20 : h[i];
    if (sum !== parseOctal(h.subarray(148, 156))) throw new Error("tar: header checksum mismatch (corrupt archive)");
    const type = h[156] === 0 ? "0" : String.fromCharCode(h[156]);
    const size = parseOctal(h.subarray(124, 136));
    let name = cstring(h.subarray(0, 100));
    if (h.subarray(257, 262).toString("latin1") === "ustar") {
      const prefix = cstring(h.subarray(345, 500));
      if (prefix) name = `${prefix}/${name}`;
    }
    off += 512;
    if (off + size > tar.length) throw new Error("tar: truncated archive");
    const body = tar.subarray(off, off + size);
    off += Math.ceil(size / 512) * 512;
    if (type === "x") {
      const rec = parsePax(body);
      if (rec.path) paxPath = rec.path;
      continue;
    }
    if (type === "g") continue;
    if (type === "L") {
      longName = cstring(body);
      continue;
    }
    if (paxPath !== null) name = paxPath;
    else if (longName !== null) name = longName;
    paxPath = null;
    longName = null;
    if (type === "0" || type === "7") files.set(name.replace(/^\.\//, ""), Buffer.from(body));
  }
  // GitHub archives (and git archive --prefix) wrap everything in one top
  // directory such as ai-native-cli-spec-2.0.0/; strip it.
  const keys = [...files.keys()];
  if (keys.length) {
    const top = keys[0].split("/")[0];
    if (keys.every((k) => k.startsWith(`${top}/`))) {
      const stripped = new Map();
      for (const [k, v] of files) stripped.set(k.slice(top.length + 1), v);
      return stripped;
    }
  }
  return files;
}

// openSource returns the spec repository at the requested version.
//   from: a local ai-native-cli-spec checkout (directory) or a release archive (.tar.gz)
//   ref:  otherwise, the git ref to download from GitHub (the pin)
async function openSource({ from, ref }) {
  if (from) {
    const abs = path.resolve(from);
    let st;
    try {
      st = fs.statSync(abs);
    } catch (_) {
      throw new Error(`--from ${from}: not found`);
    }
    if (st.isDirectory()) {
      if (!fs.existsSync(path.join(abs, "template", "common"))) {
        throw new Error(`--from ${from}: not an ai-native-cli-spec checkout (no template/common)`);
      }
      return new DirSource(abs);
    }
    const src = new MapSource(parseTarGz(fs.readFileSync(abs)), abs);
    if (!src.read(`${COMMON_SRC}/contract/contract.json`)) {
      throw new Error(`--from ${from}: not an ai-native-cli-spec archive (no ${COMMON_SRC}/contract/contract.json)`);
    }
    return src;
  }
  if (!ref) throw new Error("no spec version to fetch");
  const buf = await fetchBuffer(archiveUrl(ref));
  const src = new MapSource(parseTarGz(buf), `${REPO}@${ref}`);
  if (!src.read(`${COMMON_SRC}/contract/contract.json`)) {
    throw new Error(`${REPO}@${ref} has no ${COMMON_SRC}/contract/contract.json`);
  }
  return src;
}

// ---------------------------------------------------------------- tool layout

function readPin(root) {
  try {
    return fs.readFileSync(toAbs(root, SPEC_VERSION_FILE), "utf8").trim();
  } catch (_) {
    return "";
  }
}

// semverCore("v2.0.0-rc.1") -> "2.0.0"; null when s is not a version.
function semverCore(s) {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$/.exec(String(s || "").trim());
  return m ? `${Number(m[1])}.${Number(m[2])}.${Number(m[3])}` : null;
}

function normalizePyName(n) {
  return String(n).trim().toLowerCase().replace(/[-.]+/g, "_");
}

function tomlTables(text) {
  const tables = new Map();
  let cur = "";
  let lines = [];
  const flush = () => tables.set(cur, (tables.get(cur) || "") + lines.join("\n") + "\n");
  for (const line of text.split(/\r?\n/)) {
    const m = /^\s*\[([^\[\]]+)\]\s*(#.*)?$/.exec(line);
    if (m) {
      flush();
      cur = m[1].trim();
      lines = [];
    } else {
      lines.push(line);
    }
  }
  flush();
  return tables;
}

// Names the repository's own build files give the Python package, most
// specific first: console-script modules, then the project name.
function pythonNameHints(root) {
  const hints = [];
  const py = readOrNull(path.join(root, "pyproject.toml"));
  if (py) {
    const t = tomlTables(py.toString("utf8"));
    for (const tbl of ["project.scripts", "tool.poetry.scripts"]) {
      const body = t.get(tbl) || "";
      for (const m of body.matchAll(/=\s*["']([A-Za-z_][\w.]*)(?::[\w.]+)?["']/g)) hints.push(m[1].split(".")[0]);
    }
    for (const tbl of ["project", "tool.poetry"]) {
      const m = /^\s*name\s*=\s*["']([^"']+)["']/m.exec(t.get(tbl) || "");
      if (m) hints.push(m[1]);
    }
  }
  const setup = readOrNull(path.join(root, "setup.py"));
  if (setup) {
    const m = /\bname\s*=\s*["']([^"']+)["']/.exec(setup.toString("utf8"));
    if (m) hints.push(m[1]);
  }
  return [...new Set(hints.map(normalizePyName))];
}

const NOT_A_TOOL_PACKAGE = new Set([
  "tests",
  "test",
  "docs",
  "scripts",
  "build",
  "dist",
  "node_modules",
  "venv",
  "env",
  "examples",
  "example",
  "e2e",
  "skills",
  "contract",
  "bin",
  "site-packages",
  "npm-platform",
]);

function findPythonPackage(root) {
  const isPkg = (rel) => fs.existsSync(path.join(toAbs(root, rel), "__init__.py"));
  for (const n of pythonNameHints(root)) {
    for (const rel of [n, `src/${n}`]) if (isPkg(rel)) return rel;
  }
  const candidates = [];
  for (const base of ["", "src/"]) {
    let entries = [];
    try {
      entries = fs.readdirSync(path.join(root, base || "."), { withFileTypes: true });
    } catch (_) {
      continue;
    }
    for (const e of entries) {
      if (!e.isDirectory() || e.name.startsWith(".") || e.name.startsWith("_") || NOT_A_TOOL_PACKAGE.has(e.name)) continue;
      if (isPkg(`${base}${e.name}`)) candidates.push(`${base}${e.name}`);
    }
  }
  if (candidates.length === 1) return candidates[0];
  if (!candidates.length) {
    throw new Error("no Python package (a directory with __init__.py) at the repository root or under src/");
  }
  const byRepo = candidates.filter((c) => c.split("/").pop() === normalizePyName(path.basename(root)));
  if (byRepo.length === 1) return byRepo[0];
  throw new Error(
    `cannot tell which Python package receives the library (${candidates.join(", ")}); ` +
      "set [project] name in pyproject.toml to the package name",
  );
}

function findVendoredPython(root) {
  const out = [];
  for (const base of ["", "src/"]) {
    let entries = [];
    try {
      entries = fs.readdirSync(path.join(root, base || "."), { withFileTypes: true });
    } catch (_) {
      continue;
    }
    for (const e of entries) {
      if (e.isDirectory() && fs.existsSync(path.join(root, base, e.name, PY_LIB_NAME, MANIFEST))) {
        out.push(`${base}${e.name}/${PY_LIB_NAME}`);
      }
    }
  }
  return out;
}

// detectLayout decides the tool's language and where its library copy lives.
// Go: internal/aicli. Python: an existing <pkg>/_aicli (re-sync keeps its
// place), else <detected package>/_aicli.
function detectLayout(root, forcedLang) {
  const has = (rel) => fs.existsSync(toAbs(root, rel));
  let lang = forcedLang || "";
  if (lang && !LIBRARIES[lang]) throw new Error(`--lang must be go or py, not ${JSON.stringify(lang)}`);
  if (!lang) {
    const go = has("go.mod");
    const py = has("pyproject.toml") || has("setup.py");
    if (go && !py) lang = "go";
    else if (py && !go) lang = "py";
    else if (go && py) {
      const goVendored = has(`${GO_LIB_DIR}/${MANIFEST}`);
      const pyVendored = findVendoredPython(root).length > 0;
      if (goVendored !== pyVendored) lang = goVendored ? "go" : "py";
      else throw new Error("this repository has both go.mod and pyproject.toml/setup.py; pass --lang go or --lang py");
    } else {
      throw new Error(`no go.mod, pyproject.toml or setup.py in ${root}; run this from the tool's repository root`);
    }
  }
  if (lang === "go") return { lang, libDir: GO_LIB_DIR };
  const vendored = findVendoredPython(root);
  if (vendored.length > 1) throw new Error(`more than one vendored Python library: ${vendored.join(", ")}`);
  if (vendored.length === 1) return { lang, libDir: vendored[0] };
  return { lang, libDir: `${findPythonPackage(root)}/${PY_LIB_NAME}` };
}

// ---------------------------------------------------------------- the plan

// planSync lists every file to vendor from source into a tool with the given
// layout, and the managed directories whose extra files are stale.
function planSync(source, layout) {
  const entries = [];
  for (const rel of [...AGENT_SPECS, ...CONTRACT, ...TOOLING]) {
    entries.push({ src: `${COMMON_SRC}/${rel}`, dest: rel, group: "spec" });
  }
  const schemaNames = source.list(`${COMMON_SRC}/${SCHEMAS_DIR}`).filter((n) => n.endsWith(".json"));
  if (!schemaNames.length) throw new Error(`${source.label}: no JSON Schemas in ${COMMON_SRC}/${SCHEMAS_DIR}`);
  for (const n of schemaNames) {
    entries.push({ src: `${COMMON_SRC}/${SCHEMAS_DIR}/${n}`, dest: `${SCHEMAS_DIR}/${n}`, group: "schemas" });
  }
  const lib = LIBRARIES[layout.lang];
  const manifestBuf = source.read(`${lib.src}/${MANIFEST}`);
  if (!manifestBuf) {
    throw new Error(
      `${source.label}: no ${lib.src}/${MANIFEST}, so there is no ${lib.label} library to vendor at this version ` +
        "(in the spec repository, run: node template/common/scripts/build-manifest.js)",
    );
  }
  const manifest = parseManifest(manifestBuf, `${source.label}: ${lib.src}/${MANIFEST}`);
  for (const rel of Object.keys(manifest.files).sort(byteOrder)) {
    entries.push({ src: `${lib.src}/${rel}`, dest: `${layout.libDir}/${rel}`, group: "library", sha256: manifest.files[rel] });
  }
  entries.push({ src: `${lib.src}/${MANIFEST}`, dest: `${layout.libDir}/${MANIFEST}`, group: "library" });
  const managed = [
    { dir: SCHEMAS_DIR, recursive: false, owns: (rel) => rel.endsWith(".json"), keep: new Set(schemaNames) },
    {
      dir: layout.libDir,
      recursive: true,
      owns: (rel) => !isJunk(rel),
      keep: new Set([...Object.keys(manifest.files), MANIFEST]),
    },
  ];
  return { entries, managed, manifest, schemaCount: schemaNames.length };
}

// readPlan reads every planned file from the source, all or nothing, and
// verifies the library files against the source's own MANIFEST.json, so a
// spec version whose manifest is stale is refused rather than half-vendored.
function readPlan(source, plan) {
  const contents = new Map();
  const missing = [];
  const mismatched = [];
  for (const e of plan.entries) {
    const buf = source.read(e.src);
    if (!buf) {
      missing.push(e.src);
      continue;
    }
    if (e.sha256 && sha256(buf) !== e.sha256) mismatched.push(e.src);
    contents.set(e.dest, normalizeText(buf));
  }
  const problems = [];
  if (missing.length) problems.push(`${source.label} is missing: ${missing.join(", ")}`);
  if (mismatched.length) {
    problems.push(
      `${source.label}: these library files do not match their MANIFEST.json entry: ${mismatched.join(", ")} ` +
        "(in the spec repository, run: node template/common/scripts/build-manifest.js)",
    );
  }
  if (problems.length) throw new Error(problems.join("\n"));
  return contents;
}

// findStale lists files (relative to root) inside managed directories that the
// plan does not ship.
function findStale(root, managed) {
  const stale = [];
  for (const m of managed) {
    const abs = toAbs(root, m.dir);
    if (!fs.existsSync(abs)) continue;
    let rels;
    if (m.recursive) {
      rels = listFiles(abs);
    } else {
      rels = fs
        .readdirSync(abs, { withFileTypes: true })
        .filter((d) => d.isFile())
        .map((d) => d.name);
    }
    for (const rel of rels) if (m.owns(rel) && !m.keep.has(rel)) stale.push(`${m.dir}/${rel}`);
  }
  return stale.sort(byteOrder);
}

module.exports = {
  REPO,
  COMMON_SRC,
  SPEC_VERSION_FILE,
  AGENT_SPECS,
  CONTRACT,
  SCHEMAS_DIR,
  TOOLING,
  MANIFEST,
  LIBRARIES,
  GO_LIB_DIR,
  PY_LIB_NAME,
  isLibraryFile,
  isJunk,
  normalizeText,
  sha256,
  assertSafeRel,
  assertNoSymlink,
  toAbs,
  readOrNull,
  listFiles,
  buildManifest,
  serializeManifest,
  parseManifest,
  isSafeRef,
  archiveUrl,
  fetchBuffer,
  parseTarGz,
  openSource,
  readPin,
  semverCore,
  findPythonPackage,
  detectLayout,
  planSync,
  readPlan,
  findStale,
};
