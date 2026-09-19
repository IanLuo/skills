#!/usr/bin/env python3
"""
jev.py — ask TypeSafe System One for a scored second opinion about a state.

Three things are load-bearing here, each because the alternative bit us:

  * the token comes from `cred`, never this process — so a secret never enters a
    Python program, an argv, or the transcript;
  * the payload travels as a temp *file*, because curl's `-d@path` has to be one
    argv element and JSON re-quoted through a shell is where correctness goes to
    die;
  * the state is read from a file, so it can contain apostrophes and quotes.

Usage:
  jev.py gate <name> --state <file>   evaluate a gate against its stored rubric
  jev.py gate --list                  list the rubrics available
  jev.py raw --payload <file>         send a payload verbatim

Exit: 0 the model answered, 1 nothing was asked or the call failed.
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile

SKILL = pathlib.Path(__file__).resolve().parents[1]
RUBRICS = SKILL / "references" / "rubrics.json"
PROFILE = "typesafe"  # the cred profile holding the endpoint + API key
ENDPOINT_VAR = "TYPESAFE_ENDPOINT"
KEY_VAR = "TYPESAFE_API_KEY"


def cred_cmd() -> list[str]:
    """`cred` is deliberately not on PATH (the skill deploys as a folder), so prefer
    an explicit CRED, then PATH, then the sibling skill's script."""
    if os.environ.get("CRED"):
        return os.environ["CRED"].split()
    found = shutil.which("cred")
    if found:
        return [found]
    sibling = SKILL.parent / "credentials" / "scripts" / "cred.sh"
    if sibling.is_file():
        return ["bash", str(sibling)]
    sys.exit("cannot find cred — set CRED, put `cred` on PATH, or run from the skills repo")


def evaluate(payload: dict) -> dict:
    with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as fh:
        json.dump(payload, fh)
        path = fh.name
    cmd = cred_cmd() + [
        "run", PROFILE, "--", "curl", "-sS", "-X", "POST",
        "--variable", f"%{ENDPOINT_VAR}",
        "--expand-url", f"{{{{{ENDPOINT_VAR}}}}}",
        "--variable", f"%{KEY_VAR}",
        "--expand-header", f"Authorization: Bearer {{{{{KEY_VAR}}}}}",
        "-H", "Content-Type: application/json",
        "--max-time", "120",
        f"-d@{path}",
    ]
    try:
        try:
            r = subprocess.run(cmd, capture_output=True, text=True)
        except OSError as e:
            # A traceback here would be the only thing the caller saw, and the
            # usual cause is a CRED that does not point at anything runnable.
            sys.exit(f"cannot run cred ({' '.join(cmd[:2])}): {e}")
    finally:
        pathlib.Path(path).unlink(missing_ok=True)

    if r.returncode != 0:
        sys.exit(f"the API call failed (exit {r.returncode}): {r.stderr.strip() or r.stdout.strip()}")
    try:
        return json.loads(r.stdout)
    except json.JSONDecodeError:
        sys.exit(f"unexpected response: {r.stdout[:500]}")


def report(ans: dict) -> None:
    print(f"model     {ans['model']}")
    for name, a in ans["answers"].items():
        kind = a["type"]
        print(f"\n{name}   [{kind}]")
        if kind == "score":
            print(f"  score {a['score']:.2f}    confidence {a['confidence']:.2f}")
            print("  levels (score = probability-weighted across them):")
            for level in sorted(a["probabilities"], key=int):
                print(f"    {level}  {a['probabilities'][level]:.2f}  {a['legend'].get(level, '')}")
        elif kind == "choice":
            print(f"  choice {a['choice']!r}    confidence {a['confidence']:.2f}")
            for opt, p in sorted(a["probabilities"].items(), key=lambda kv: -kv[1]):
                print(f"    {opt}: {p:.2f}")
        else:  # noul
            print(f"  {a['noul']:.2f}   (probability the answer is yes)")
    usage = ans.get("usage", {})
    print(f"\ntokens    in {usage.get('input_tokens', '?')} / out {usage.get('output_tokens', '?')}")


def main() -> int:
    ap = argparse.ArgumentParser(prog="jev.py", description=__doc__.strip().splitlines()[1])
    sub = ap.add_subparsers(dest="mode", required=True)

    gate = sub.add_parser("gate", help="evaluate a gate against its stored rubric")
    gate.add_argument("name", nargs="?", help="gate name from references/rubrics.json")
    gate.add_argument("--state", help="file holding the state to evaluate")
    gate.add_argument("--list", action="store_true", help="list available rubrics")
    gate.add_argument("--json", action="store_true", help="print the raw response")

    raw = sub.add_parser("raw", help="send a payload verbatim")
    raw.add_argument("--payload", required=True)
    raw.add_argument("--json", action="store_true")

    args = ap.parse_args()
    rubrics = json.loads(RUBRICS.read_text())

    if args.mode == "gate" and args.list:
        for name, q in sorted(rubrics.items()):
            print(f"{name}: {q['instructions']}")
            for i, level in enumerate(q["criteria"]):
                print(f"    {i}  {level}")
        return 0

    if args.mode == "gate":
        if not args.name or not args.state:
            ap.error("gate needs a name and --state <file>")
        if args.name not in rubrics:
            sys.exit(f"no rubric '{args.name}' — known: {', '.join(sorted(rubrics))}")
        payload = {
            "model": "jev-latest",
            "state": pathlib.Path(args.state).read_text(),
            "questions": {args.name: {"type": "score", **rubrics[args.name]}},
        }
    else:
        payload = json.loads(pathlib.Path(args.payload).read_text())

    ans = evaluate(payload)
    if args.json:
        print(json.dumps(ans, indent=2))
    else:
        report(ans)
        print("\nnow append a row to ~/.fs/jev/observations.md (see SKILL.md)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
