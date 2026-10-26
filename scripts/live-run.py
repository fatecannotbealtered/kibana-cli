#!/usr/bin/env python3
"""Run every read command of kibana-cli against a real Kibana and record the result.

The record is a VER-005 live record (.agent/VERIFY-SPEC.md): the tool version and
commit, the Kibana it ran against, the time, and per call the argv, the exit code
and the checks made on the output. It is written to docs/evidence/ as JSON and
Markdown; commands whose every call passed may then cite it as `live:` evidence.

Only read commands run. No `auth login`, `auth logout`, `field-map init` or
`field-map add-profile`, nothing that writes the OS keyring, Kibana or
Elasticsearch: the scope file points at a path that does not exist, so even a
write that slipped in would be refused before anything happened.

Two ways to give it a Kibana:

  Environment mode: KIBANA_CLI_HOST, KIBANA_CLI_USER and KIBANA_CLI_PASSWORD are
  set in the environment that runs this script.

      python scripts/live-run.py --index 'logs-*'

  Profile mode: --host and --user name a Kibana user whose password kibana-cli
  1.x (or `auth login`) saved in the OS keyring. The run writes a scratch
  settings.json with one profile (host and user, no secret), and the binary reads
  the password from the keyring entry kibana-cli / <host>|basic|<user>. This
  script never reads, prints or stores the password.

      python scripts/live-run.py --host https://kibana.example.com --user alice [--index 'app-log-*']

Every run uses a fresh home folder (HOME, USERPROFILE, APPDATA, LOCALAPPDATA,
TEMP, XDG_*) and state directory, deleted afterwards unless --keep-run is given,
so the user's own settings, scope, journal and field map are never touched. The
OS keyring is per user, not per home folder: profile mode reads the real entry.
On macOS the Keychain may not be found under a scratch HOME; use environment mode
there if profile mode reports E_AUTH.

Each stdout is checked against the vendored contract (contract/schemas/*.json,
contract/contract.json): the envelope schema, CLI-007 names, ids and times, the
size budgets, the meta.untrusted path syntax and the exit code of the error code.
With --spec <ai-native-cli-spec checkout> the spec's own Python validator runs
too. The record keeps no log content by default (log messages can hold anything
the logged systems wrote): per call it keeps a SHA-256 of stdout and a summary of
its shape; --include-output keeps stdout itself. The Kibana host and user are
replaced with <host> and <user> unless --keep-host is given.

Standard library only; Python 3.9 or later. Exit 0 when every call passed, 1 when
one did not, 2 when the run could not start.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any, Callable, Optional

ROOT = Path(__file__).resolve().parent.parent
TOOL = "kibana-cli"

# Commands that write; the run refuses to start one (belt and braces: the scope
# file does not exist either).
WRITE_COMMANDS = ("auth login", "auth logout", "field-map init", "field-map add-profile", "update", "operation resolve")
BUILTINS = ("reference", "context", "doctor", "changelog", "update", "operation", "help", "version")

SNAKE = re.compile(r"^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$")
UTC_TIME = re.compile(r"^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](?:\.[0-9]{1,9})?Z$")
DATE_ONLY = re.compile(r"^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])$")
UNTRUSTED_PATH = re.compile(r"^(?:data(?:\[\])*|error\.details)(?:\.[a-z][a-z0-9]*(?:_[a-z0-9]+)*(?:\[\])*)*$")
CODE_KEYED = {"error_codes", "exit_codes"}


# ------------------------------------------------------------------ contract


class Contract:
    """The vendored contract: JSON schemas by $id, error codes, budgets."""

    def __init__(self, root: Path):
        self.schemas: dict[str, Any] = {}
        for path in sorted((root / "contract" / "schemas").glob("*.json")):
            doc = json.loads(path.read_text(encoding="utf-8"))
            self.schemas[doc["$id"]] = doc
        contract = json.loads((root / "contract" / "contract.json").read_text(encoding="utf-8"))
        self.exits: dict[str, int] = {}
        codes = contract["error_codes"]
        for table in (codes.get("core", {}), codes.get("extension", {})):
            if isinstance(table, dict):
                for code, entry in table.items():
                    if isinstance(entry, dict) and "exit" in entry:
                        self.exits[code] = int(entry["exit"])
        budgets = contract["size_budgets"]
        self.success_budget = int(budgets["default_success_chars"])
        self.error_budget = int(budgets["error_chars"])
        self.index_budget = int(budgets["reference_index_chars"])
        self.command_budget = int(budgets["reference_command_chars"])
        self.spec_version = contract.get("spec_version", "")

    def schema(self, urn: str) -> Any:
        return self.schemas[urn]


class SchemaValidator:
    """The subset of JSON Schema 2020-12 the vendored schemas use."""

    def __init__(self, contract: Contract):
        self.contract = contract
        self._patterns: dict[str, re.Pattern] = {}

    def validate(self, instance: Any, urn: str) -> list[str]:
        problems: list[str] = []
        root = self.contract.schema(urn)
        self._check(instance, root, root, "$", problems)
        return problems

    def _resolve(self, ref: str, base: Any) -> tuple[Any, Any]:
        urn, _, pointer = ref.partition("#")
        doc = self.contract.schema(urn) if urn else base
        node = doc
        for part in [p for p in pointer.split("/") if p]:
            node = node[part.replace("~1", "/").replace("~0", "~")]
        return node, doc

    def _ok(self, instance: Any, schema: Any, base: Any) -> bool:
        probe: list[str] = []
        self._check(instance, schema, base, "$", probe)
        return not probe

    def _check(self, x: Any, s: Any, base: Any, path: str, out: list[str]) -> None:
        if s is True or s is None:
            return
        if s is False:
            out.append(f"{path}: not allowed")
            return
        if "$ref" in s:
            target, doc = self._resolve(s["$ref"], base)
            self._check(x, target, doc, path, out)
        t = s.get("type")
        if t is not None and not any(_is_type(x, one) for one in (t if isinstance(t, list) else [t])):
            out.append(f"{path}: is not {t}")
            return
        if "const" in s and not _same(x, s["const"]):
            out.append(f"{path}: must be {json.dumps(s['const'])}")
        if "enum" in s and not any(_same(x, e) for e in s["enum"]):
            out.append(f"{path}: must be one of {s['enum']}")
        for sub in s.get("allOf", []):
            self._check(x, sub, base, path, out)
        if "anyOf" in s and not any(self._ok(x, sub, base) for sub in s["anyOf"]):
            out.append(f"{path}: matches none of anyOf")
        if "oneOf" in s and sum(1 for sub in s["oneOf"] if self._ok(x, sub, base)) != 1:
            out.append(f"{path}: does not match exactly one of oneOf")
        if "not" in s and self._ok(x, s["not"], base):
            out.append(f"{path}: matches a forbidden schema")
        if "if" in s:
            branch = s.get("then") if self._ok(x, s["if"], base) else s.get("else")
            if branch is not None:
                self._check(x, branch, base, path, out)
        if isinstance(x, str):
            if "minLength" in s and len(x) < s["minLength"]:
                out.append(f"{path}: shorter than {s['minLength']}")
            if "maxLength" in s and len(x) > s["maxLength"]:
                out.append(f"{path}: longer than {s['maxLength']}")
            if "pattern" in s and not self._pattern(s["pattern"]).search(x):
                out.append(f"{path}: does not match {s['pattern']}")
            fmt = s.get("format")
            if fmt == "date-time" and not UTC_TIME.match(x):
                out.append(f"{path}: is not an ISO 8601 UTC time")
            if fmt == "date" and not DATE_ONLY.match(x):
                out.append(f"{path}: is not a date")
        if _is_number(x):
            if "minimum" in s and x < s["minimum"]:
                out.append(f"{path}: below {s['minimum']}")
            if "maximum" in s and x > s["maximum"]:
                out.append(f"{path}: above {s['maximum']}")
        if isinstance(x, list):
            if "minItems" in s and len(x) < s["minItems"]:
                out.append(f"{path}: fewer than {s['minItems']} items")
            if "maxItems" in s and len(x) > s["maxItems"]:
                out.append(f"{path}: more than {s['maxItems']} items")
            if s.get("uniqueItems"):
                seen = [json.dumps(i, sort_keys=True) for i in x]
                if len(seen) != len(set(seen)):
                    out.append(f"{path}: items are not unique")
            if "items" in s:
                for i, item in enumerate(x):
                    self._check(item, s["items"], base, f"{path}[{i}]", out)
            if "contains" in s:
                n = sum(1 for item in x if self._ok(item, s["contains"], base))
                if n < s.get("minContains", 1):
                    out.append(f"{path}: too few items match contains")
                if "maxContains" in s and n > s["maxContains"]:
                    out.append(f"{path}: too many items match contains")
        if isinstance(x, dict):
            for key in s.get("required", []):
                if key not in x:
                    out.append(f"{path}: lacks {key}")
            if "minProperties" in s and len(x) < s["minProperties"]:
                out.append(f"{path}: fewer than {s['minProperties']} keys")
            if "maxProperties" in s and len(x) > s["maxProperties"]:
                out.append(f"{path}: more than {s['maxProperties']} keys")
            for key, needs in s.get("dependentRequired", {}).items():
                if key in x:
                    for other in needs:
                        if other not in x:
                            out.append(f"{path}: {key} needs {other}")
            props = s.get("properties", {})
            for key, sub in props.items():
                if key in x:
                    self._check(x[key], sub, base, f"{path}.{key}", out)
            if "propertyNames" in s:
                for key in x:
                    self._check(key, s["propertyNames"], base, f"{path}.{key} (name)", out)
            extra = s.get("additionalProperties")
            if extra is not None:
                for key in x:
                    if key in props:
                        continue
                    if extra is False:
                        out.append(f"{path}: unexpected key {key}")
                    elif isinstance(extra, dict):
                        self._check(x[key], extra, base, f"{path}.{key}", out)

    def _pattern(self, p: str) -> re.Pattern:
        if p not in self._patterns:
            self._patterns[p] = re.compile(p)
        return self._patterns[p]


def _is_number(x: Any) -> bool:
    return isinstance(x, (int, float)) and not isinstance(x, bool)


def _is_type(x: Any, t: str) -> bool:
    return {
        "object": isinstance(x, dict),
        "array": isinstance(x, list),
        "string": isinstance(x, str),
        "boolean": isinstance(x, bool),
        "null": x is None,
        "number": _is_number(x),
        "integer": (isinstance(x, int) and not isinstance(x, bool)) or (isinstance(x, float) and x.is_integer()),
    }.get(t, True)


def _same(a: Any, b: Any) -> bool:
    if isinstance(a, bool) or isinstance(b, bool):
        return type(a) is type(b) and a == b
    return a == b


def _no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for key, value in pairs:
        if key in out:
            raise ValueError(f"duplicate key {key!r}")
        out[key] = value
    return out


def generic_checks(value: Any, path: str, out: list[str], code_keyed: bool = False) -> None:
    """CLI-007: snake_case keys, string ids, UTC times (as aicli's genericChecks)."""
    if isinstance(value, dict):
        for key, item in value.items():
            at = f"{path}.{key}"
            if not code_keyed:
                if not SNAKE.match(key):
                    out.append(f"{at}: key is not lowercase snake_case (CLI-007)")
                if (key in ("id", "iid") or key.endswith("_id")) and item is not None and not isinstance(item, str):
                    out.append(f"{at}: ids are strings (CLI-007)")
                if (key == "ids" or key.endswith("_ids")) and not (isinstance(item, list) and all(isinstance(i, str) for i in item)):
                    out.append(f"{at}: ids are strings (CLI-007)")
                if key.endswith("_at") and item is not None and not (isinstance(item, str) and UTC_TIME.match(item)):
                    out.append(f"{at}: times are ISO 8601 UTC with Z (CLI-007)")
            generic_checks(item, at, out, key in CODE_KEYED and isinstance(item, dict) and not code_keyed)
    elif isinstance(value, list):
        for i, item in enumerate(value):
            generic_checks(item, f"{path}[{i}]", out)


# ------------------------------------------------------------------ calls


class Call:
    def __init__(self, cid: str, area: str, argv: list[str], expect: str, *, code: Optional[str] = None,
                 raw: bool = False, budget: Optional[int] = None, data_schema: Optional[str] = None,
                 check: Optional[Callable[[Any], list[str]]] = None):
        self.id, self.area, self.argv, self.expect = cid, area, argv, expect
        self.code, self.raw, self.budget, self.data_schema, self.check = code, raw, budget, data_schema, check

    @property
    def command(self) -> str:
        """The command path: the first two words before a flag, one for the
        single-word built-ins ("" for --version)."""
        words = []
        for a in self.argv:
            if a.startswith("-") or len(words) == 2:
                break
            words.append(a)
        if words and words[0] in ("reference", "context", "doctor", "changelog"):
            return words[0]
        return " ".join(words)


class Runner:
    def __init__(self, args: argparse.Namespace, binary: Path, run_dir: Path, env: dict[str, str],
                 contract: Contract, spec_validator: Any, secrets: list[str], redactions: list[tuple[str, str]]):
        self.args, self.binary, self.run_dir, self.env = args, binary, run_dir, env
        self.contract, self.schema = contract, SchemaValidator(contract)
        self.spec_validator, self.secrets, self.redactions = spec_validator, secrets, redactions
        self.records: list[dict[str, Any]] = []
        self.secret_hits = 0

    def redact(self, text: str) -> str:
        for secret in self.secrets:
            if secret and secret in text:
                self.secret_hits += 1
                text = text.replace(secret, "<secret>")
        for old, new in self.redactions:
            if old:
                text = text.replace(old, new)
        return text

    def run(self, call: Call) -> Any:
        """Runs one call, records it and returns the parsed stdout (or None)."""
        joined = " ".join(call.argv)
        if any(joined.startswith(w) for w in WRITE_COMMANDS):
            raise SystemExit(f"refusing to run a write command: {joined}")
        start = time.monotonic()
        proc = subprocess.run([str(self.binary), *call.argv], env=self.env, cwd=str(self.run_dir),
                              stdin=subprocess.DEVNULL, capture_output=True, timeout=self.args.call_timeout)
        duration = int((time.monotonic() - start) * 1000)
        stdout, stderr = proc.stdout, proc.stderr
        problems: list[str] = []
        doc: Any = None
        summary: dict[str, Any] = {}
        if call.raw:
            try:
                doc = json.loads(stdout.decode("utf-8"), object_pairs_hook=_no_duplicates)
            except ValueError as e:
                problems.append(f"raw output is not one JSON document: {e}")
            if proc.returncode != 0:
                problems.append(f"exit {proc.returncode}, expected 0")
        else:
            doc = self._check_envelope(call, stdout, proc.returncode, problems)
            summary = shape(doc)
        if doc is not None and call.check is not None and not problems:
            try:
                problems.extend(call.check(doc))
            except Exception as e:  # a check that cannot read the output is a failure, not a crash
                problems.append(f"check failed: {type(e).__name__}: {e}")
        if stderr.strip() and not call.raw:
            # Tests are not a terminal: stderr stays quiet (CLI-047). A notice line is fine.
            summary["stderr_bytes"] = len(stderr)
        record: dict[str, Any] = {
            "id": call.id, "area": call.area, "command": call.command,
            "argv": [TOOL, *[self.redact(a) for a in call.argv]],
            "exit": proc.returncode, "duration_ms": duration, "expect": call.expect,
            "envelope_valid": None if call.raw else not any(p.startswith("contract:") for p in problems),
            "pass": not problems, "problems": [self.redact(p) for p in problems],
            "stdout_sha256": hashlib.sha256(stdout).hexdigest(), "stdout_chars": len(stdout.decode("utf-8", "replace")),
            "summary": json.loads(self.redact(json.dumps(summary))),
        }
        if self.args.include_output:
            record["stdout"] = self.redact(stdout.decode("utf-8", "replace"))
            record["stderr"] = self.redact(stderr.decode("utf-8", "replace"))[:4000]
        else:
            # Scan even what is not kept: a secret in output is a defect either way.
            self.redact(stdout.decode("utf-8", "replace") + stderr.decode("utf-8", "replace"))
        self.records.append(record)
        mark = "pass" if record["pass"] else "FAIL"
        print(f"{call.id:4} {mark}  exit {proc.returncode:<3} {duration:>6} ms  {' '.join(record['argv'][1:])}", flush=True)
        for p in record["problems"]:
            print(f"        {p}", flush=True)
        return doc

    def _check_envelope(self, call: Call, raw: bytes, exit_code: int, problems: list[str]) -> Any:
        text = raw.decode("utf-8", "replace")
        if raw.startswith(b"\xef\xbb\xbf"):
            problems.append("contract: stdout starts with a byte order mark")
        if not raw.endswith(b"\n") or raw.endswith(b"\r\n"):
            problems.append("contract: stdout does not end with a single \\n")
        try:
            doc = json.loads(text, object_pairs_hook=_no_duplicates)
        except ValueError as e:
            problems.append(f"contract: stdout is not one JSON document: {e}")
            return None
        for p in self.schema.validate(doc, "urn:ai-native-cli-spec:envelope:2.0"):
            problems.append(f"contract: envelope: {p}")
        generic: list[str] = []
        generic_checks(doc, "$", generic)
        problems.extend(f"contract: {p}" for p in generic)
        if isinstance(doc, dict):
            meta = doc.get("meta") if isinstance(doc.get("meta"), dict) else {}
            for path in meta.get("untrusted", []) or []:
                if not (isinstance(path, str) and UNTRUSTED_PATH.match(path)):
                    problems.append(f"contract: meta.untrusted path {path!r} is not a data or error.details path")
            size = len(text.strip())
            ok = doc.get("ok")
            if ok is True:
                if exit_code != 0:
                    problems.append(f"contract: ok is true but the exit code is {exit_code}")
                budget = call.budget if call.budget is not None else self.contract.success_budget
                if budget and size > budget:
                    problems.append(f"contract: {size} characters, over the {budget} budget")
                if call.data_schema:
                    for p in self.schema.validate(doc.get("data"), call.data_schema):
                        problems.append(f"contract: data: {p}")
            elif ok is False:
                code = (doc.get("error") or {}).get("code")
                want = self.contract.exits.get(code)
                if want is not None and exit_code != want:
                    problems.append(f"contract: {code} exits {want}, the tool exited {exit_code}")
                if size > self.contract.error_budget:
                    problems.append(f"contract: error envelope is {size} characters, over the {self.contract.error_budget} budget")
            if self.spec_validator is not None:
                try:
                    self.spec_validator(raw)
                except Exception as e:
                    problems.append(f"contract: spec validator: {e}")
            if call.code is None and ok is not True:
                err = doc.get("error") or {}
                problems.append(f"expected ok, got {err.get('code')}: {err.get('message')}")
            if call.code is not None and (ok is not False or (doc.get("error") or {}).get("code") != call.code):
                got = (doc.get("error") or {}).get("code") if ok is False else "ok"
                problems.append(f"expected {call.code}, got {got}")
        return doc


def shape(doc: Any) -> dict[str, Any]:
    """What a call returned, without its content: codes, counts, keys."""
    if not isinstance(doc, dict):
        return {}
    out: dict[str, Any] = {"ok": doc.get("ok")}
    if doc.get("ok") is False:
        err = doc.get("error") or {}
        out["code"] = err.get("code")
        details = err.get("details") or {}
        out["dispatch_state"] = details.get("dispatch_state")
        return out
    data = doc.get("data")
    if isinstance(data, dict):
        out["data_keys"] = sorted(data.keys())[:40]
        for key in ("count", "total", "has_more", "next_offset", "sort"):
            if key in data:
                out[key] = data[key]
        scope = data.get("scope")
        if isinstance(scope, dict):
            out["scope_keys"] = sorted(scope.keys())
            if isinstance(scope.get("zero_hits"), dict):
                out["zero_hits_reason"] = scope["zero_hits"].get("reason")
    meta = doc.get("meta") or {}
    notices = meta.get("notices") or []
    if notices:
        out["notices"] = sorted({n.get("type") for n in notices if isinstance(n, dict)})
    if meta.get("untrusted"):
        out["untrusted"] = meta["untrusted"]
    return out


# ------------------------------------------------------------------ helpers for checks


def data(doc: Any) -> dict[str, Any]:
    return doc.get("data") if isinstance(doc, dict) and isinstance(doc.get("data"), dict) else {}


def items(doc: Any) -> list[dict[str, Any]]:
    return [i for i in data(doc).get("items", []) if isinstance(i, dict)]


def need(cond: bool, problem: str) -> list[str]:
    return [] if cond else [problem]


def page_checks(doc: Any) -> list[str]:
    d = data(doc)
    out = need(isinstance(d.get("items"), list), "no items list")
    out += need(d.get("count") == len(d.get("items", [])), "count is not the number of items")
    if d.get("has_more"):
        out += need(any(k in d for k in ("next_offset", "next_cursor", "continuation_reason")), "has_more without a continuation")
    return out


def hit_checks(doc: Any) -> list[str]:
    out = page_checks(doc)
    for h in items(doc):
        ref = h.get("ref", "")
        out += need(isinstance(ref, str) and "/" in ref, f"hit ref {ref!r} is not <index>/<_id>")
        ts = h.get("timestamp")
        out += need(ts is None or UTC_TIME.match(str(ts)) is not None, f"timestamp {ts!r} is not UTC")
    scope = data(doc).get("scope") or {}
    out += need(UTC_TIME.match(str(scope.get("since", ""))) is not None, "scope.since is not a UTC time")
    out += need(UTC_TIME.match(str(scope.get("until", ""))) is not None, "scope.until is not a UTC time")
    return out


# ------------------------------------------------------------------ setup


def build_binary(run_dir: Path) -> Path:
    name = TOOL + (".exe" if os.name == "nt" else "")
    out = run_dir / "bin" / name
    subprocess.run(["go", "build", "-o", str(out), "./cmd/kibana-cli"], cwd=str(ROOT), check=True)
    return out


def git(*args: str) -> str:
    try:
        return subprocess.run(["git", *args], cwd=str(ROOT), capture_output=True, text=True, check=True).stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return ""


def scratch_env(home: Path, state: Path, run_dir: Path) -> dict[str, str]:
    keep_prefixes = ("KIBANA_CLI_CA_FILE",)
    env = {k: v for k, v in os.environ.items()
           if not k.startswith("KIBANA_CLI_") or k in keep_prefixes}
    for key in ("HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "TMPDIR",
                "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"):
        env.pop(key, None)
    tmp = home / "tmp"
    for d in (home, state, tmp, home / "AppData" / "Roaming", home / "AppData" / "Local",
              home / ".config", home / ".cache", home / ".local" / "share", home / ".local" / "state"):
        d.mkdir(parents=True, exist_ok=True)
    env.update({
        "HOME": str(home), "USERPROFILE": str(home),
        "APPDATA": str(home / "AppData" / "Roaming"), "LOCALAPPDATA": str(home / "AppData" / "Local"),
        "TEMP": str(tmp), "TMP": str(tmp), "TMPDIR": str(tmp),
        "XDG_CONFIG_HOME": str(home / ".config"), "XDG_CACHE_HOME": str(home / ".cache"),
        "XDG_DATA_HOME": str(home / ".local" / "share"), "XDG_STATE_HOME": str(home / ".local" / "state"),
        "KIBANA_CLI_STATE_DIR": str(state),
        # No scope: any write would be refused before it happened.
        "KIBANA_CLI_SCOPE_FILE": str(run_dir / "no-scope.json"),
    })
    return env


def load_spec_validator(spec: Optional[str]) -> Any:
    if not spec:
        return None
    sys.path.insert(0, str(Path(spec) / "template" / "python"))
    from aicli.validate import validate_envelope  # type: ignore  # noqa: E402

    return validate_envelope


def parse_minutes(since: str) -> int:
    m = re.fullmatch(r"(\d+)([mhdw])", since.strip())
    if not m:
        return 0
    return int(m.group(1)) * {"m": 1, "h": 60, "d": 1440, "w": 10080}[m.group(2)]


# ------------------------------------------------------------------ the run


def run_all(r: Runner, args: argparse.Namespace, field_map_path: Path) -> dict[str, Any]:
    """Every read command, in an order where later calls use what earlier ones found."""
    facts: dict[str, Any] = {}
    since = args.since

    # Built-ins and sign-in.
    r.run(Call("B01", "builtins", ["--version"], "prints the version"))
    r.run(Call("B02", "builtins", ["context"], "local state, no request", data_schema="urn:ai-native-cli-spec:context:2.0"))
    doc = r.run(Call("B03", "builtins", ["doctor"], "connectivity, credentials and console_proxy pass",
                     data_schema="urn:ai-native-cli-spec:doctor:2.0", check=doctor_checks))
    facts["doctor"] = {c.get("check"): c.get("status") for c in data(doc).get("checks", []) if isinstance(c, dict)}
    for c in data(doc).get("checks", []):
        if isinstance(c, dict) and c.get("check") == "kibana_version":
            facts["kibana_version_message"] = c.get("message")
    doc = r.run(Call("B04", "builtins", ["reference", "--index"], "every command, within 8,000 characters",
                     budget=r.contract.index_budget, data_schema="urn:ai-native-cli-spec:reference-index:2.0"))
    facts["commands"] = sorted(c.get("path") for c in data(doc).get("commands", []) if isinstance(c, dict) and c.get("path"))
    r.run(Call("B05", "builtins", ["reference", "--command", "log search"], "one command, within 6,000 characters",
               budget=r.contract.command_budget, data_schema="urn:ai-native-cli-spec:reference-command:2.0"))
    r.run(Call("B06", "builtins", ["changelog", "--since", "1.1.17"], "the 2.0 entries",
               data_schema="urn:ai-native-cli-spec:changelog:2.0"))
    r.run(Call("B07", "builtins", ["operation", "list"], "no operations (nothing was written)",
               check=lambda d: need(len(items(d)) == 0, "operations exist in a fresh state directory")))
    doc = r.run(Call("A01", "auth", ["auth", "status"], "the account, its roles and the Kibana version source",
                     check=lambda d: need(bool(data(d).get("account")), "no account")
                     + need(data(d).get("kibana_version_source") in ("setting", "kibana_status", "elasticsearch", "unknown"),
                            "kibana_version_source is not one of the four")))
    facts["kibana_version"] = data(doc).get("kibana_version")
    facts["kibana_version_source"] = data(doc).get("kibana_version_source")
    facts["roles"] = len(data(doc).get("roles") or [])
    r.run(Call("A02", "profile", ["profile", "list"], "the profiles of the scratch settings.json, no secret",
               check=lambda d: page_checks(d) + need("password" not in json.dumps(d), "a password key in the list")))

    # Data views and fields.
    doc = r.run(Call("P01", "pattern", ["pattern", "list", "--limit", "100"], "every data view with its ref", check=page_checks))
    views = items(doc)
    facts["data_views"] = data(doc).get("total", len(views))
    index = args.index
    if not index:
        timed = [v for v in views if v.get("time_field")] or views
        index = timed[0].get("title") if timed else None
    if not index:
        print("No --index and no data view to take one from; the log calls are skipped.", flush=True)
        facts["index"] = None
        return facts
    facts["index"] = index
    view = next((v for v in views if v.get("title") == index), views[0] if views else None)
    r.run(Call("P02", "pattern", ["pattern", "fields", "--index", index, "--limit", "200"], "the fields of the index",
               check=lambda d: page_checks(d) + need(len(items(d)) > 0, "no fields")))
    if view:
        r.run(Call("P03", "pattern", ["pattern", "fields", "--data-view", view["ref"], "--limit", "50"],
                   "the fields of a data view, by its ref", check=page_checks))
        r.run(Call("O01", "object", ["object", "get", view["ref"]], "the data view as a saved object"))
    doc = r.run(Call("P04", "pattern", ["pattern", "infer", "--index", index, "--name", "live"],
                     "a field-map profile and its add-profile line",
                     check=lambda d: need("next_command" in data(d), "no next_command")))
    profile_yaml = data(doc).get("yaml")

    # Field map: written by this script into the scratch state directory (never by
    # the tool), then read by the tool.
    r.run(Call("F01", "field-map", ["field-map", "get"], "no field map yet", code="E_NOT_FOUND"))
    if isinstance(profile_yaml, str) and profile_yaml.strip():
        field_map_path.write_text(profile_yaml, encoding="utf-8")  # pattern infer's yaml is a whole profiles: document
        r.run(Call("F02", "field-map", ["field-map", "get"], "the inferred profile",
                   check=lambda d: need(any(p.get("name") == "live" for p in data(d).get("profiles", []) if isinstance(p, dict)),
                                        "the profile live is not listed")))

    # Logs.
    doc = r.run(Call("L01", "log", ["log", "search", "--index", index, "--since", since, "--limit", "5"],
                     "the newest hits with refs, total and scope", check=hit_checks))
    hits = items(doc)
    facts["total"] = data(doc).get("total")
    facts["data_stream_hits"] = sum(1 for h in hits if str(h.get("ref", "")).startswith(".ds-"))
    if not hits:
        print(f"No hits in {index} over --since {since}; widen --since for a fuller run.", flush=True)
    r.run(Call("L02", "log", ["log", "search", "--index", index, "--since", since, "--limit", "2", "--offset", "2"],
               "the second page", check=hit_checks))
    if hits:
        ref = hits[0]["ref"]
        r.run(Call("L03", "log", ["log", "get", ref], "the whole document",
                   check=lambda d: need(data(d).get("ref") == ref, "a different ref came back")
                   + need(isinstance(data(d).get("source"), list), "no source list")))
        r.run(Call("L04", "log", ["log", "get", ref, "--format", "raw"], "Kibana's JSON unchanged", raw=True,
                   check=lambda d: need(isinstance(d, dict) and "_source" in d, "no _source in the raw document")))
        bad = ref.split("/", 1)[0] + "/kibana-cli-live-run-no-such-id"
        r.run(Call("E01", "errors", ["log", "get", bad], "a missing document", code="E_NOT_FOUND"))
    r.run(Call("L05", "log", ["log", "search", "--index", index, "--since", since, "--level", "ERROR", "--limit", "1"],
               "the error count in total", check=hit_checks))
    r.run(Call("L06", "log", ["log", "search", "--index", index, "--since", since, "--query", "error", "--limit", "1"],
               "a Lucene query", check=hit_checks))
    r.run(Call("L07", "log", ["log", "search", "--index", index, "--since", since, "--query-language", "kql",
                              "--query", "not kibana_cli_live_run_field:x", "--limit", "1"], "a KQL query", check=hit_checks))
    r.run(Call("L08", "log", ["log", "search", "--index", index, "--since", since, "--limit", "1", "--explain"],
               "the request in scope.request",
               check=lambda d: need(isinstance((data(d).get("scope") or {}).get("request"), dict), "no scope.request")))
    r.run(Call("L09", "log", ["log", "search", "--index", index, "--dsl", '{"query":{"match_all":{}},"size":1}'],
               "a whole _search body", check=page_checks))
    r.run(Call("L10", "log", ["log", "search", "--index", index, "--since", since, "--query",
                              "kibana_cli_live_run_no_such_token_7f3a", "--limit", "1"], "zero hits, explained",
               check=lambda d: need(len(items(d)) == 0, "hits for a token that should not exist")
               + need(isinstance((data(d).get("scope") or {}).get("zero_hits"), dict), "no scope.zero_hits")))
    if field_map_path.exists():
        r.run(Call("L11", "log", ["log", "search", "--index", index, "--since", since, "--field-profile", "live", "--limit", "1"],
                   "the inferred field profile", check=hit_checks))
    # A real trace id: not an unexpanded pipeline placeholder such as %{[log][traceId]}
    # (seen in production data), which every document shares.
    trace = args.trace_id or next((h.get("trace_id") for h in hits if isinstance(h.get("trace_id"), str)
                                   and re.fullmatch(r"[\w.:-]{4,128}", h["trace_id"])), None)
    if trace:
        r.run(Call("L12", "log", ["log", "search", "--index", index, "--since", since, "--trace-id", trace],
                   "the hits of one trace", check=lambda d: hit_checks(d) + need(len(items(d)) > 0, "no hit for the trace")))
    # level and service map through the field map: use the inferred profile when there
    # is one, as a user with a field map would (an index whose level field is not one
    # of the usual names, like log_level, needs it).
    profile = ["--field-profile", "live"] if field_map_path.exists() else []
    r.run(Call("L13", "log", ["log", "aggregate", "--index", index, "--since", since, "--terms", "level", *profile],
               "counts per level", check=page_checks))
    r.run(Call("L14", "log", ["log", "aggregate", "--index", index, "--since", since, "--terms", "service", "--limit", "5", *profile],
               "the top services", check=page_checks))
    interval = args.interval
    if parse_minutes(since) and parse_minutes(interval) and parse_minutes(since) / parse_minutes(interval) > 120:
        interval = f"{-(-parse_minutes(since) // 120)}m"
    r.run(Call("L15", "log", ["log", "aggregate", "--index", index, "--since", since, "--agg-type", "date_histogram",
                              "--interval", interval], "counts per time bucket", check=page_checks))

    # Saved objects.
    first_object = None
    for i, typ in enumerate(("dashboard", "visualization", "lens", "search", "index-pattern")):
        doc = r.run(Call(f"O1{i}", "object", ["object", "list", "--type", typ, "--limit", "5"], f"{typ} objects", check=page_checks))
        if first_object is None and items(doc) and typ != "index-pattern":
            first_object = items(doc)[0].get("ref")
    if first_object:
        r.run(Call("O02", "object", ["object", "get", first_object, "--fields", "+attributes"], "one saved object with its attributes"))
        r.run(Call("O03", "object", ["object", "get", first_object, "--format", "raw"], "Kibana's JSON unchanged", raw=True,
                   check=lambda d: need(isinstance(d, dict) and "id" in d, "no id in the raw object")))

    # Refusals that need no request.
    r.run(Call("E02", "errors", ["log", "search", "--index", "_kibana_cli_live_system"], "a system index is refused", code="E_VALIDATION"))
    r.run(Call("E03", "errors", ["pattern", "fields", "--index", "kibana-cli-live-run-no-such-*"], "a pattern with no index",
               code="E_NOT_FOUND"))
    return facts


def doctor_checks(doc: Any) -> list[str]:
    checks = {c.get("check"): c for c in data(doc).get("checks", []) if isinstance(c, dict)}
    out = []
    for name in ("connectivity", "credentials", "console_proxy", "kibana_version"):
        c = checks.get(name)
        out += need(c is not None and c.get("status") == "pass", f"doctor {name}: {c.get('status') if c else 'missing'}")
    return out


# ------------------------------------------------------------------ record


def write_record(r: Runner, args: argparse.Namespace, facts: dict[str, Any], meta: dict[str, Any]) -> tuple[Path, Path]:
    tool_commands = [c for c in facts.get("commands", []) if c.split(" ")[0] not in BUILTINS]
    by_command: dict[str, list[dict[str, Any]]] = {}
    for rec in r.records:
        by_command.setdefault(rec["command"], []).append(rec)
    verified = sorted(c for c in tool_commands if by_command.get(c) and all(x["pass"] for x in by_command[c]))
    failed = sorted(c for c in by_command if not all(x["pass"] for x in by_command[c]))
    not_run = sorted(c for c in tool_commands if c not in by_command)
    calls = len(r.records)
    passed = sum(1 for x in r.records if x["pass"])
    envelopes = [x for x in r.records if x["envelope_valid"] is not None]
    version = facts.get("kibana_version") or "unknown"
    record = {
        "record": "live", "schema": "VER-005 live record", "tool": TOOL,
        "version": meta["tool_version"], "commit": meta["commit"], "branch": meta["branch"],
        "spec": {"version": r.contract.spec_version,
                 "validator": "scripts/live-run.py: contract/schemas/*.json (JSON Schema 2020-12 subset), CLI-007 names, "
                              "size budgets and exit codes from contract/contract.json"
                              + ("; plus template/python/aicli/validate.py of the spec checkout" if r.spec_validator else "")},
        "upstream": {"product": "Kibana", "version": facts.get("kibana_version"),
                     "version_source": facts.get("kibana_version_source"),
                     "url": "<host>" if not args.keep_host else meta["host"],
                     "data_views": facts.get("data_views"), "index": facts.get("index"),
                     "data_stream_hits": facts.get("data_stream_hits")},
        "mode": meta["mode"],
        "ran_at": meta["ran_at"], "finished_at": meta["finished_at"],
        "isolation": {
            "home": "HOME, USERPROFILE, APPDATA, LOCALAPPDATA, TEMP, TMP and XDG_* pointed into a fresh run folder; "
                    "KIBANA_CLI_STATE_DIR inside it; deleted after the run" + (" (kept: --keep-run)" if args.keep_run else ""),
            "scope": "KIBANA_CLI_SCOPE_FILE named a file that does not exist, so no write could be granted",
            "credentials": meta["credentials"],
            "writes": "none: only read commands ran; the field map read by F02 and L11 was written by this script into the run folder",
        },
        "secret_scan": {"secrets_checked": len([s for s in r.secrets if s]), "hits": r.secret_hits},
        "summary": {"calls": calls, "passed": passed, "failed": [x["id"] for x in r.records if not x["pass"]],
                    "envelopes_checked": len(envelopes), "envelopes_valid": sum(1 for x in envelopes if x["envelope_valid"]),
                    "tool_commands": len(tool_commands), "verified": len(verified), "not_run": len(not_run)},
        "verified_commands": verified, "commands_failed": failed, "commands_not_run": not_run,
        "doctor": facts.get("doctor"),
        "calls": r.records,
    }
    out_dir = Path(args.out).resolve()
    out_dir.mkdir(parents=True, exist_ok=True)
    stem = f"{meta['ran_at'][:10]}-kibana-{re.sub(r'[^0-9A-Za-z.]+', '-', str(version))}"
    path = out_dir / f"{stem}.json"
    n = 2
    while path.exists() or path.with_suffix(".md").exists():
        path = out_dir / f"{stem}-{n}.json"
        n += 1
    path.write_text(json.dumps(record, indent=1, ensure_ascii=False) + "\n", encoding="utf-8", newline="\n")
    md = path.with_suffix(".md")
    md.write_text(markdown(record, path.name), encoding="utf-8", newline="\n")
    return path, md


def markdown(rec: dict[str, Any], json_name: str) -> str:
    s = rec["summary"]
    up = rec["upstream"]
    lines = [
        f"# Live run: kibana-cli on Kibana {up['version'] or '(version unknown)'}",
        "",
        f"The VER-005 live record of kibana-cli's read commands. Its machine-readable twin is [`{json_name}`]({json_name}): "
        "every call with its argv, exit code, duration, the checks made on its output and a SHA-256 of stdout.",
        "",
        "| | |",
        "|---|---|",
        f"| Tool | kibana-cli `{rec['version']}` (commit `{rec['commit'][:12] or 'unknown'}`, branch `{rec['branch'] or 'unknown'}`) |",
        f"| Spec | ai-native-cli-spec contract `{rec['spec']['version']}`; {rec['spec']['validator']} |",
        f"| Upstream | Kibana {up['version'] or 'version unknown'} (version from: {up['version_source']}) at {up['url']}; "
        f"{up['data_views']} data views; index `{up['index']}`; {up['data_stream_hits']} of the first hits from data stream backing indices |",
        f"| Mode | {rec['mode']} |",
        f"| Ran | {rec['ran_at']} to {rec['finished_at']} |",
        "",
        "## Result",
        "",
        f"- {s['calls']} calls, {s['passed']} as expected" + (f"; not as expected: {', '.join(s['failed'])}." if s["failed"] else "."),
        f"- {s['envelopes_valid']} of {s['envelopes_checked']} stdout envelopes are valid, with every exit code checked against `contract.json`.",
        f"- {s['verified']} of the {s['tool_commands']} tool commands passed every call: "
        + (", ".join(f"`{c}`" for c in rec["verified_commands"]) or "none") + ".",
        f"- Not run (writes, or nothing to run them on): " + (", ".join(f"`{c}`" for c in rec["commands_not_run"]) or "none") + ".",
        f"- Secret scan: {rec['secret_scan']['secrets_checked']} secrets checked, {rec['secret_scan']['hits']} hits.",
        "",
        "## How it ran",
        "",
        f"- **Isolation.** {rec['isolation']['home']}. {rec['isolation']['scope']}.",
        f"- **Credentials.** {rec['isolation']['credentials']}.",
        f"- **Writes.** {rec['isolation']['writes']}.",
        "",
        "## Calls",
        "",
        "| Call | Command line | Exit | Result |",
        "|---|---|---|---|",
    ]
    for c in rec["calls"]:
        argv = " ".join(c["argv"]).replace("|", "\\|")
        result = "as expected" if c["pass"] else "NOT as expected: " + "; ".join(c["problems"]).replace("|", "\\|")
        lines.append(f"| {c['id']} | `{argv}` | {c['exit']} | {c['expect']}: {result} |")
    lines.append("")
    return "\n".join(lines)


# ------------------------------------------------------------------ main


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0], formatter_class=argparse.RawDescriptionHelpFormatter,
                                epilog="See the module docstring (the top of this file) for the two modes.")
    p.add_argument("--index", help="index pattern to search, like 'logs-*' (required in environment mode)")
    p.add_argument("--host", help="profile mode: Kibana base URL whose password is in the OS keyring")
    p.add_argument("--user", help="profile mode: the Kibana user of that keyring entry")
    p.add_argument("--since", default="24h", help="time range of the log calls (default 24h)")
    p.add_argument("--interval", default="1h", help="date histogram bucket (default 1h; widened to keep 120 buckets at most)")
    p.add_argument("--trace-id", help="a trace id to follow (default: the first hit's)")
    p.add_argument("--binary", help="kibana-cli binary to run (default: go build from this checkout)")
    p.add_argument("--out", default=str(ROOT / "docs" / "evidence"), help="where the record goes (default docs/evidence)")
    p.add_argument("--spec", help="an ai-native-cli-spec checkout whose Python validator also checks every stdout")
    p.add_argument("--include-output", action="store_true", help="keep stdout and stderr in the record (they may hold log content)")
    p.add_argument("--keep-host", action="store_true", help="keep the Kibana host and user in the record instead of <host> and <user>")
    p.add_argument("--keep-run", action="store_true", help="keep the run folder (scratch home and state) afterwards")
    p.add_argument("--call-timeout", type=int, default=120, help="seconds one call may take (default 120)")
    args = p.parse_args()

    profile_mode = bool(args.host or args.user)
    if profile_mode and not (args.host and args.user):
        p.error("profile mode needs both --host and --user")
    if not profile_mode:
        missing = [k for k in ("KIBANA_CLI_HOST", "KIBANA_CLI_USER", "KIBANA_CLI_PASSWORD") if not os.environ.get(k)]
        if missing:
            p.error("environment mode needs " + ", ".join(missing) + " (or use --host and --user for profile mode)")
        if not args.index:
            p.error("environment mode needs --index")

    run_dir = Path(tempfile.mkdtemp(prefix="kibana-live-"))
    try:
        contract = Contract(ROOT)
        spec_validator = load_spec_validator(args.spec)
        binary = Path(args.binary).resolve() if args.binary else build_binary(run_dir)
        home, state = run_dir / "home", run_dir / "home" / ".kibana-cli"
        env = scratch_env(home, state, run_dir)
        secrets: list[str] = []
        if profile_mode:
            host, user = args.host.rstrip("/"), args.user
            settings = {"default_profile": "live", "profiles": {"live": {"host": host, "user": user}}}
            (state / "settings.json").write_text(json.dumps(settings, indent=1) + "\n", encoding="utf-8")
            credentials = ("profile mode: a scratch settings.json with one profile (host and user, no secret); the binary "
                           "read the password from the OS keyring entry kibana-cli / <host>|basic|<user>, which this run "
                           "neither read, printed, stored nor changed")
        else:
            host, user = os.environ["KIBANA_CLI_HOST"].rstrip("/"), os.environ["KIBANA_CLI_USER"]
            env["KIBANA_CLI_HOST"], env["KIBANA_CLI_USER"] = os.environ["KIBANA_CLI_HOST"], user
            env["KIBANA_CLI_PASSWORD"] = os.environ["KIBANA_CLI_PASSWORD"]
            secrets.append(os.environ["KIBANA_CLI_PASSWORD"])
            credentials = ("environment mode: KIBANA_CLI_HOST, KIBANA_CLI_USER and KIBANA_CLI_PASSWORD from the environment; "
                           "the OS keyring was not used; the password was scanned for in every output and never recorded")
        redactions: list[tuple[str, str]] = [(str(run_dir), "<run>"), (str(run_dir).replace("\\", "\\\\"), "<run>")]
        if not args.keep_host:
            bare = re.sub(r"^https?://", "", host)
            redactions += [(host, "<host>"), (bare, "<host>")]
            if len(user) >= 3:
                redactions += [(f'"{user}"', '"<user>"'), (f" {user} ", " <user> ")]
        runner = Runner(args, binary, run_dir, env, contract, spec_validator, secrets, redactions)
        version = subprocess.run([str(binary), "--version"], env=env, capture_output=True, text=True).stdout
        try:
            tool_version = json.loads(version)["data"]["version"]
        except (ValueError, KeyError, TypeError):
            tool_version = "unknown"
        ran_at = dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        print(f"kibana-cli {tool_version}, {'profile' if profile_mode else 'environment'} mode, run folder {run_dir}", flush=True)
        facts = run_all(runner, args, state / "field-map.yaml")
        meta = {"tool_version": tool_version, "commit": git("rev-parse", "HEAD"), "branch": git("rev-parse", "--abbrev-ref", "HEAD"),
                "host": host, "mode": "profile" if profile_mode else "environment", "credentials": credentials,
                "ran_at": ran_at, "finished_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")}
        json_path, md_path = write_record(runner, args, facts, meta)
        failed = [x["id"] for x in runner.records if not x["pass"]]
        print(f"\n{len(runner.records) - len(failed)} of {len(runner.records)} calls as expected; record: {json_path} and {md_path.name}")
        if runner.secret_hits:
            print(f"WARNING: the password appeared {runner.secret_hits} times in output (redacted in the record).")
        return 0 if not failed and not runner.secret_hits else 1
    except subprocess.CalledProcessError as e:
        print(f"setup failed: {e}", file=sys.stderr)
        return 2
    finally:
        if not args.keep_run:
            shutil.rmtree(run_dir, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
