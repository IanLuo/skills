#!/usr/bin/env python3
"""
test_cred_contract.py — behaviour tests for the cred skill's shell↔binary contract.

cred.sh owns the vault/profile contract — where profiles live, where the unlocked
cache is, the relock window, and the `@secret` token — and exports it as CRED_*
variables; cred-run consumes it and refuses to guess. Nothing else checks that the
two halves agree, which is how a broken `cred run` can pass every other check in
this repo: it happened, when cred.sh's exports were lost and validate/audit/tests
all still reported success. These tests exist so that silence is not possible.

Rules under test:

  1. cred run injects a profile's non-secret var and scrubs the secret to ***
  2. the secret's value never survives into the output, in any form
  3. the allow-list is enforced (exit 6)
  4. a profile with no secrets works while the vault is locked
  5. the relock window comes from cred.sh: a stale cache reads as locked (exit 2)
  6. cred-run without the CRED_* contract fails loudly instead of guessing
  7. upsert replaces a value containing & and | (the BSD-sed and & expansion bugs)
  8. cred list shows @secret, never a value

Usage: python3 tests/credentials/test_cred_contract.py     (run from anywhere)
Exit: 0 all pass (or skipped, if cred-run is not built), 1 any fail.
"""

import os
import pathlib
import subprocess
import sys
import tempfile

REPO = pathlib.Path(__file__).resolve().parents[2]
CRED = REPO / "skills/credentials/scripts/cred.sh"
RUN = REPO / "skills/credentials/scripts/cred-run"

SECRET = "v3ry-s3cret-value"
PASS, FAILS = 0, []


def check(name, cond, detail=""):
    global PASS
    if cond:
        PASS += 1
        print(f"  ok   {name}")
    else:
        FAILS.append(name)
        print(f"FAIL {name}: {detail}")


def cred(cred_dir, *args, env=None):
    """Run cred.sh with CRED_DIR pointed at a throwaway directory."""
    e = dict(os.environ, CRED_DIR=str(cred_dir))
    if env:
        e.update(env)
    return subprocess.run(
        ["bash", str(CRED), *args], env=e, capture_output=True, text=True
    )


def profile(cred_dir, name, body):
    d = cred_dir / "profiles"
    d.mkdir(parents=True, exist_ok=True)
    (d / f"{name}.env").write_text(body)


def cache(cred_dir, rows):
    """The plaintext cache `cred unlock` writes: the vault header + TSV rows."""
    (cred_dir / "unlocked").write_text(
        "# cred-vault-v1\n" + "".join(f"{p}\t{v}\t{val}\n" for p, v, val in rows)
    )


def main():
    if not RUN.is_file():
        print(f"  skip cred-run is not built ({RUN}); run ./bin/build-project.sh credentials")
        return 0

    with tempfile.TemporaryDirectory() as td:
        cred_dir = pathlib.Path(td)
        profile(cred_dir, "p", f"SECRET=@secret\nURL=https://api.example.com\nallow=printenv list\n")
        cache(cred_dir, [("p", "SECRET", SECRET)])

        # 1 + 2. Injection, scrubbing, and passthrough of a non-secret var.
        r = cred(cred_dir, "run", "p", "--", "printenv", "SECRET")
        check("run injects the secret and scrubs it", r.stdout.strip() == "***", repr(r.stdout))
        check("the secret value never reaches output", SECRET not in r.stdout + r.stderr)
        r = cred(cred_dir, "run", "p", "--", "printenv", "URL")
        check("run passes a non-secret var through", r.stdout.strip() == "https://api.example.com", repr(r.stdout))

        # 3. Allow-list.
        r = cred(cred_dir, "run", "p", "--", "cat", "/etc/hosts")
        check("allow-list refuses an unlisted command", r.returncode == 6, f"exit={r.returncode}")
        check("the refusal names the allow-list", "allow-list" in r.stderr, r.stderr)

        # 4. No secrets → works while locked (no cache at all).
        locked = cred_dir / "locked"
        profile(locked, "q", "URL=https://api.example.com\nallow=printenv\n")
        r = cred(locked, "run", "q", "--", "printenv", "URL")
        check("a secret-free profile runs while locked", r.returncode == 0, r.stderr.strip())

        # 5. The relock window is cred.sh's to define; cred-run reads it.
        r = subprocess.run(
            [str(RUN), "run", "p", "--", "printenv", "SECRET"],
            env=dict(
                os.environ,
                CRED_PROFILE_DIR=str(cred_dir / "profiles"),
                CRED_UNLOCKED=str(cred_dir / "unlocked"),
                CRED_TTL="0",
                CRED_SECRET_MARKER="@secret",
            ),
            capture_output=True,
            text=True,
        )
        check("a stale cache reads as locked", r.returncode == 2, f"exit={r.returncode}")
        check("locked failure says so", "LOCKED" in r.stderr, r.stderr)

        # 6. The contract is required, not guessed — this is what broke silently.
        r = subprocess.run(
            [str(RUN), "run", "p", "--", "printenv", "SECRET"],
            env={k: v for k, v in os.environ.items() if not k.startswith("CRED_")},
            capture_output=True,
            text=True,
        )
        check("cred-run without the contract fails", r.returncode == 1, f"exit={r.returncode}")
        check("the failure names the missing variable", "CRED_PROFILE_DIR" in r.stderr, r.stderr)

        # 7. upsert: replace, not append; `&` and `|` are data, not syntax.
        cred(cred_dir, "set", "u", "URL", "https://a/b?c=1&d=2|x")
        cred(cred_dir, "set", "u", "URL", "https://a/b?c=9&d=8|x")
        body = (cred_dir / "profiles/u.env").read_text()
        check("upsert replaces instead of appending", body.count("URL=") == 1, body.strip())
        check("a value with & and | survives intact", "URL=https://a/b?c=9&d=8|x" in body, body.strip())
        check("upsert leaves no temp file", not list((cred_dir / "profiles").glob("*.new")))

        # 8. list.
        r = cred(cred_dir, "list", "p")
        check("list shows the @secret reference", "SECRET -> @secret" in r.stdout, r.stdout)
        check("list never prints a value", SECRET not in r.stdout)

    print(f"\n  {PASS} passed, {len(FAILS)} failed")
    return 1 if FAILS else 0


if __name__ == "__main__":
    sys.exit(main())
