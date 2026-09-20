#!/usr/bin/env python3
"""
test_cred_contract.py — behaviour tests for the cred skill's shell↔binary contract.

cred.sh owns the vault/profile contract — where profiles live, where the holder
socket is, the relock window, and the `@secret` token — and exports it as CRED_*
variables; cred-run consumes it and refuses to guess. Nothing else checks that the
two halves agree, which is how a broken `cred run` can pass every other check in
this repo: it happened, when cred.sh's exports were lost and validate/audit/tests
all still reported success. These tests exist so that silence is not possible.

The contract has two halves. `cred-run hold` is the executor: it owns the
decrypted vault in process memory and does the profile loading, the allow-list,
the injection, the scrubbing and the streaming. `cred-run run` is a thin client
that connects, relays, and exits with the holder's code. So a client that is not
cred-run — the raw-socket test below — cannot bypass the scrub.

Rules under test:

  1. cred run injects a profile's non-secret var and scrubs the secret to ***
  2. the secret's value never survives into the output, in any form
  3. the allow-list is enforced (exit 6) — by the holder, not the client
  4. a secret-free profile runs while unlocked, and `cred run` never runs
     without the window open (no silent uncredentialed run)
  5. one holder serves many runs (unlock once, reuse until TTL)
  6. the decrypted vault is never on disk while unlocked
  7. a missing or expired holder reads as locked (exit 2), never hangs
  8. cred-run without the CRED_* contract fails loudly instead of guessing
  9. upsert replaces a value containing & and | (the BSD-sed and & expansion bugs)
 10. cred list shows @secret, never a value

Usage: python3 tests/credentials/test_cred_contract.py     (run from anywhere)
Exit: 0 all pass (or skipped, if cred-run is not built), 1 any fail.
"""

import atexit
import os
import pathlib
import pty
import select
import shutil
import socket
import struct
import subprocess
import sys
import tempfile
import time

REPO = pathlib.Path(__file__).resolve().parents[2]
CRED = REPO / "skills/credentials/scripts/cred.sh"
RUN = REPO / "skills/credentials/scripts/cred-run"

SECRET = "v3ry-s3cret-value"
# A passphrase with a hyphen and digits, so the hex check below is unambiguous.
PASSPHRASE = "vault-pass-9"
VAULT_HEADER = "# cred-vault-v1"
# An answer of this instead of a string closes the pty's input (Ctrl-D at the
# start of a line), which is how a test ends a child that is reading its stdin.
EOF_KEY = "<eof>"
PASS, FAILS = 0, []

# The suite never touches the real Keychain: a `security` shim goes first on
# PATH, and every test that exercises the passphrase path drives this instead.
# Its answers come from files, so no test has to thread env through a pty.
SHIM_DIR = None

SHIM = '''#!/usr/bin/env bash
# test double for macOS `security` (see make_shim)
d="${SHIM_DIR:?SHIM_DIR unset}"
printf '%s\\n' "$@" > "$d/argv"
if [ "${1:-}" = "-i" ]; then
  cat > "$d/stdin" && exit 0
fi
case "$(cat "$d/mode" 2>/dev/null || echo value)" in
  absent) exit 44 ;;
  denied) exit 128 ;;
  value)  printf '%s' "$(cat "$d/value" 2>/dev/null)" ;;
esac
'''


def make_shim(d):
    """Create the `security` shim and point the suite at it."""
    global SHIM_DIR
    SHIM_DIR = d
    d.mkdir(parents=True, exist_ok=True)
    (d / "security").write_text(SHIM)
    (d / "security").chmod(0o755)
    set_keychain("absent")  # default: nothing remembered
    return d


def set_keychain(mode, value=""):
    """absent = never remembered, denied = you clicked Deny, value = approved."""
    (SHIM_DIR / "mode").write_text(mode)
    (SHIM_DIR / "value").write_text(value)


def base_env(cred_dir, extra=None):
    e = dict(os.environ, CRED_DIR=str(cred_dir), SHIM_DIR=str(SHIM_DIR),
             PATH=f"{SHIM_DIR}:{os.environ['PATH']}")
    if extra:
        e.update(extra)
    return e


def check(name, cond, detail=""):
    global PASS
    if cond:
        PASS += 1
        print(f"  ok   {name}")
    else:
        FAILS.append(name)
        print(f"FAIL {name}: {detail}")


def cred(cred_dir, *args, env=None):
    """Run cred.sh with CRED_DIR pointed at a throwaway directory.

    stdin is /dev/null on purpose: the suite must never be able to satisfy a
    passphrase prompt, or a test would hang on a terminal that happens to be
    attached to whatever runs it.
    """
    return subprocess.run(
        ["bash", str(CRED), *args],
        env=base_env(cred_dir, env),
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
    )


def profile(cred_dir, name, body):
    d = cred_dir / "profiles"
    d.mkdir(parents=True, exist_ok=True)
    (d / f"{name}.env").write_text(body)


def vault_text(rows):
    return VAULT_HEADER + "\n" + "".join(f"{p}\t{v}\t{val}\n" for p, v, val in rows)


def start_holder(cred_dir, rows, ttl=300):
    """Start `cred-run hold` the way cred.sh does: vault piped in on stdin.

    The holder creates its socket only after it has read and parsed the whole
    vault, so the socket's existence means ready.
    """
    sock = cred_dir / "hold.sock"
    e = dict(
        os.environ,
        CRED_PROFILE_DIR=str(cred_dir / "profiles"),
        CRED_HOLD_SOCK=str(sock),
        CRED_TTL=str(ttl),
        CRED_SECRET_MARKER="@secret",
    )
    proc = subprocess.Popen(
        [str(RUN), "hold"],
        env=e,
        stdin=subprocess.PIPE,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
    )
    proc.stdin.write(vault_text(rows))
    proc.stdin.close()
    for _ in range(100):
        if sock.exists():
            return proc
        time.sleep(0.05)
    raise AssertionError(f"holder never created {sock}: {proc.stderr.read()}")


def stop_holder(cred_dir, proc):
    if proc.poll() is None:
        proc.terminate()
        proc.wait(timeout=5)
    sock = cred_dir / "hold.sock"
    if sock.exists():
        sock.unlink()


def on_a_real_tty(args, cred_dir, answers, timeout=30, extra=None):
    """Run cred.sh on a controlling pty, answering its prompts in order.

    `cred init`, `cred add` and `cred remember` are human-gated — the only way to
    test them is to actually be a terminal. A bare pty slave is not enough:
    bash's `read -s` drives the *controlling* terminal and blocks forever without
    one, so this is pty.fork() rather than Popen(stdin=slave).

    Each answer is sent only once the child has gone quiet, i.e. is blocked on a
    prompt. Writing everything up front would let the terminal driver echo it
    before `read -s` turns echo off, and the echo assertion below is the check
    that the secret never crosses the screen.

    Output comes back on one stream because the child's stdout, stderr and stdin
    are all the pty.
    """
    pid, master = pty.fork()
    if pid == 0:
        os.execvpe("bash", ["bash", str(CRED), *args], base_env(cred_dir, extra))
        os._exit(127)

    chunks, pending, quiet = [], list(answers), 0.0
    deadline = time.time() + timeout
    while time.time() < deadline:
        if select.select([master], [], [], 0.2)[0]:
            try:
                chunk = os.read(master, 4096)
            except OSError:  # EIO — the child closed its side
                break
            if not chunk:
                break
            chunks.append(chunk)
            quiet = 0.0
            continue
        quiet += 0.2
        if pending and quiet >= 0.4:
            answer = pending.pop(0)
            os.write(master, b"\x04" if answer == EOF_KEY else (answer + "\n").encode())
            quiet = 0.0
    os.close(master)

    wpid, status = os.waitpid(pid, os.WNOHANG)
    if wpid != pid:  # still running: timed out, don't hang the suite
        os.kill(pid, 9)
        _, status = os.waitpid(pid, 0)
        return -1, b"".join(chunks).decode(errors="replace")
    return os.WEXITSTATUS(status), b"".join(chunks).decode(errors="replace")


# ── The wire protocol, spoken directly ───────────────────────────────────
# Pins the shell↔binary contract at the byte level, and proves the scrub runs in
# the holder: a client that is not cred-run still gets scrubbed output.

def frame(field):
    b = field.encode()
    return struct.pack(">I", len(b)) + b


def raw_request(sock_path, profile, argv, tty=""):
    """Send a request over the socket; return (stdout, stderr, exit_code)."""
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
        s.connect(str(sock_path))
        req = frame(tty) + frame(profile) + struct.pack(">I", len(argv))
        for a in argv:
            req += frame(a)
        s.sendall(req)

        out, err, buf = [], [], b""
        while True:
            while len(buf) < 1:
                buf += s.recv(4096)
            tag, buf = buf[0], buf[1:]
            if tag == ord("x"):
                while len(buf) < 4:
                    buf += s.recv(4096)
                code = struct.unpack(">i", buf[:4])[0]
                return "".join(out), "".join(err), code
            while len(buf) < 4:
                buf += s.recv(4096)
            n = struct.unpack(">I", buf[:4])[0]
            buf = buf[4:]
            while len(buf) < n:
                buf += s.recv(4096)
            chunk, buf = buf[:n].decode(errors="replace"), buf[n:]
            (out if tag == ord("o") else err).append(chunk)


def main():
    if not RUN.is_file():
        print(f"  skip cred-run is not built ({RUN}); run ./bin/build-project.sh credentials")
        return 0

    # The shim has to outlive every section below, including the ones that open
    # their own temporary directory. If it disappears mid-run then `security`
    # resolves to the real binary again, and the tests start writing items into
    # the real Keychain — which is exactly what happened before this comment.
    make_shim(pathlib.Path(tempfile.mkdtemp(prefix="cred-shim-")))
    atexit.register(shutil.rmtree, SHIM_DIR, ignore_errors=True)

    with tempfile.TemporaryDirectory() as td:
        cred_dir = pathlib.Path(td)
        profile(cred_dir, "p", "SECRET=@secret\nURL=https://api.example.com\nallow=printenv cat\n")
        # A real vault, so that a locked window fails *at the window* — exit 2 with
        # LOCKED — rather than at a missing vault. Those are different failures
        # and the tests below assert the first.
        on_a_real_tty(["init"], cred_dir, ["pw", "pw"])
        holder = start_holder(cred_dir, [("p", "SECRET", SECRET)])
        try:
            # 1 + 2. Injection, scrubbing, and passthrough of a non-secret var.
            r = cred(cred_dir, "run", "p", "--", "printenv", "SECRET")
            check("run injects the secret and scrubs it", r.stdout.strip() == "***", repr(r.stdout))
            check("the secret value never reaches output", SECRET not in r.stdout + r.stderr)
            r = cred(cred_dir, "run", "p", "--", "printenv", "URL")
            check("run passes a non-secret var through", r.stdout.strip() == "https://api.example.com", repr(r.stdout))

            # 3. Allow-list is the holder's, so it holds for any client.
            r = cred(cred_dir, "run", "p", "--", "ls", "/etc/hosts")
            check("allow-list refuses an unlisted command", r.returncode == 6, f"exit={r.returncode}")
            check("the refusal names the allow-list", "allow-list" in r.stderr, r.stderr)

            # 3b. Same over the wire, from a client that is not cred-run.
            out, err, code = raw_request(cred_dir / "hold.sock", "p", ["printenv", "SECRET"])
            check("a foreign client is still scrubbed by the holder", out.strip() == "***", repr(out))
            _, err, code = raw_request(cred_dir / "hold.sock", "p", ["ls", "/etc/hosts"])
            check("a foreign client is still allow-listed", code == 6, f"exit={code}")
            check("the refusal reaches a foreign client", "allow-list" in err, err)

            # 4 + 5. The holder is reused: many runs, one unlock.
            pids = set()
            for _ in range(3):
                r = cred(cred_dir, "run", "p", "--", "printenv", "SECRET")
                check("reuse: each run against one holder succeeds", r.stdout.strip() == "***", repr(r.stdout))
            check("reuse: the holder is still the same process", holder.poll() is None, "holder exited")
            pids.add(holder.pid)

            # 6. THE PROPERTY: nothing decrypted on disk while unlocked.
            on_disk = [p for p in cred_dir.rglob("*") if p.is_file() and SECRET in p.read_text(errors="ignore")]
            check("no plaintext vault anywhere under CRED_DIR while unlocked", not on_disk, str(on_disk))
            check("the old plaintext cache file is gone", not (cred_dir / "unlocked").exists())

            # 4. A profile with no secrets needs no vault entry — but it still
            # needs the window: running here means running *with* credentials.
            profile(cred_dir, "n", "URL=https://api.example.com\nallow=printenv\n")
            r = cred(cred_dir, "run", "n", "--", "printenv", "URL")
            check("a secret-free profile runs while unlocked", r.returncode == 0, r.stderr.strip())

            # 7. A non-tty stdin must not hang the child (documented /dev/null fallback).
            r = subprocess.run(
                ["bash", str(CRED), "run", "p", "--", "cat"],
                env=dict(os.environ, CRED_DIR=str(cred_dir)),
                capture_output=True, text=True, timeout=10,
            )
            check("a non-tty stdin does not hang the child", r.returncode == 0, f"exit={r.returncode}")

            # 7b. On a tty the child gets that tty back: the holder is detached,
            # so without the passthrough an interactive child would read nowhere.
            # Twice over — the terminal echoes the line, and `cat` writes what it
            # read. Once means cat never read it.
            typed = "typed-into-the-child"
            code, out = on_a_real_tty(["run", "p", "--", "cat"], cred_dir, [typed, EOF_KEY])
            check("the child's stdin is the client's tty", out.count(typed) >= 2, repr(out))

            # 8. lock wipes it.
            r = cred(cred_dir, "lock")
            check("lock reports it relocked", "locked" in r.stdout, r.stdout)
            check("lock removed the socket", not (cred_dir / "hold.sock").exists())
            r = cred(cred_dir, "run", "p", "--", "printenv", "SECRET")
            check("run after lock reads as locked", r.returncode == 2, f"exit={r.returncode}")
            check("locked failure says so", "LOCKED" in r.stderr, r.stderr)
        finally:
            stop_holder(cred_dir, holder)

        # 4b. Locked means locked, even with nothing to inject: a run that would
        # silently proceed without credentials is worse than a refusal.
        locked = cred_dir / "lockeddir"
        profile(locked, "q", "URL=https://api.example.com\nallow=printenv\n")
        shutil.copy(cred_dir / "vault", locked / "vault")
        r = cred(locked, "run", "q", "--", "printenv", "URL")
        check("even a secret-free profile is refused while locked", r.returncode == 2, f"exit={r.returncode}")
        check("the locked refusal prints no env", "api.example.com" not in r.stdout, repr(r.stdout))

        # 7b. An expired holder reads as locked.
        ttl_dir = cred_dir / "ttldir"
        profile(ttl_dir, "p", "SECRET=@secret\nallow=printenv\n")
        shutil.copy(cred_dir / "vault", ttl_dir / "vault")
        expired = start_holder(ttl_dir, [("p", "SECRET", SECRET)], ttl=1)
        time.sleep(2)
        r = cred(ttl_dir, "run", "p", "--", "printenv", "SECRET")
        check("an expired holder reads as locked", r.returncode == 2, f"exit={r.returncode}")
        check("expiry failure says LOCKED", "LOCKED" in r.stderr, r.stderr)
        stop_holder(ttl_dir, expired)

        # 12. Closing the window must not cut a command that is already running:
        # the holder stops accepting, then waits for what is in flight.
        slow = cred_dir / "slowdir"
        profile(slow, "p", "SECRET=@secret\nallow=sleep\n")
        slow_holder = start_holder(slow, [("p", "SECRET", SECRET)], ttl=2)
        r = cred(slow, "run", "p", "--", "sleep", "4")
        check(
            "a run in flight survives the window closing",
            r.returncode == 0,
            f"exit={r.returncode} err={r.stderr!r}",
        )
        stop_holder(slow, slow_holder)

        # 13. A pidfile outlives its holder, and a recycled PID must not be killed.
        recyc = cred_dir / "recycdir"
        recyc.mkdir(exist_ok=True)
        bystander = subprocess.Popen(["sleep", "30"])
        try:
            (recyc / "hold.pid").write_text(str(bystander.pid))
            cred(recyc, "lock")
            check(
                "lock does not kill whatever inherited a stale PID",
                bystander.poll() is None,
                "bystander was killed",
            )
        finally:
            bystander.terminate()
            bystander.wait(timeout=5)

        # 8. The contract is required, not guessed — this is what broke silently.
        r = subprocess.run(
            [str(RUN), "run", "p", "--", "printenv", "SECRET"],
            env={k: v for k, v in os.environ.items() if not k.startswith("CRED_")},
            capture_output=True,
            text=True,
        )
        check("cred-run without the contract fails", r.returncode == 1, f"exit={r.returncode}")
        check("the failure names the missing variable", "CRED_HOLD_SOCK" in r.stderr, r.stderr)

        # 9. upsert: replace, not append; `&` and `|` are data, not syntax.
        cred(cred_dir, "set", "u", "URL", "https://a/b?c=1&d=2|x")
        cred(cred_dir, "set", "u", "URL", "https://a/b?c=9&d=8|x")
        body = (cred_dir / "profiles/u.env").read_text()
        check("upsert replaces instead of appending", body.count("URL=") == 1, body.strip())
        check("a value with & and | survives intact", "URL=https://a/b?c=9&d=8|x" in body, body.strip())
        check("upsert leaves no temp file", not list((cred_dir / "profiles").glob("*.new")))

        # 10. list.
        r = cred(cred_dir, "list", "p")
        check("list shows the @secret reference", "SECRET -> @secret" in r.stdout, r.stdout)
        check("list never prints a value", SECRET not in r.stdout)

    # 11. End to end through the real verbs, on a real pty: the half that no
    # other test reaches, because init and unlock are human-gated.
    with tempfile.TemporaryDirectory() as td:
        e2e = pathlib.Path(td)
        code, out = on_a_real_tty(["init"], e2e, ["e2e-passphrase", "e2e-passphrase"])
        check("init creates a vault on a tty", code == 0 and (e2e / "vault").exists(), out)

        code, out = on_a_real_tty(["add", "typesafe", "API"], e2e, [SECRET, "e2e-passphrase"])
        check("add stores a secret on a tty", code == 0, out)
        check("add never echoes the secret", SECRET not in out, out)
        profile(e2e, "typesafe", "API=@secret\nURL=https://api.example.com\nallow=printenv\n")

        code, out = on_a_real_tty(["unlock"], e2e, ["e2e-passphrase"])
        check("unlock opens the window", code == 0 and (e2e / "hold.sock").exists(), out)
        check("unlock records the holder pid", (e2e / "hold.pid").exists())

        r = cred(e2e, "run", "typesafe", "--", "printenv", "API")
        check(
            "end to end: the stored secret is injected and scrubbed",
            r.stdout.strip() == "***",
            f"exit={r.returncode} out={r.stdout!r} err={r.stderr!r}",
        )
        on_disk = [p for p in e2e.rglob("*") if p.is_file() and SECRET in p.read_text(errors="ignore")]
        check("end to end: nothing decrypted on disk while unlocked", not on_disk, str(on_disk))

        r = cred(e2e, "lock")
        check("lock kills the holder", r.returncode == 0 and not (e2e / "hold.sock").exists(), r.stdout + r.stderr)
        r = cred(e2e, "run", "typesafe", "--", "printenv", "API")
        check("end to end: locked after lock", r.returncode == 2, f"exit={r.returncode}")

    # 14. Agent-initiated window. No terminal anywhere in this section: `cred run`
    # is run by a client with stdin on /dev/null, and the shim stands in for a
    # human approving the dialog macOS would draw.
    with tempfile.TemporaryDirectory() as td:
        kc = pathlib.Path(td)
        profile(kc, "svc", "TOKEN=@secret\nURL=https://api.example.com\nallow=printenv\n")
        on_a_real_tty(["init"], kc, [PASSPHRASE, PASSPHRASE])
        on_a_real_tty(["add", "svc", "TOKEN"], kc, [SECRET, PASSPHRASE])

        # remember: the value must reach `security` on stdin. Interactive mode's
        # argv is just "-i", so the passphrase appearing anywhere at all would be
        # the bug — only its hex is allowed to cross.
        code, out = on_a_real_tty(["remember"], kc, [PASSPHRASE])
        check("remember completes", code == 0, out)
        # Guards the accident above: if the shim were absent, the real `security`
        # would have been called and this file would not exist.
        check("the security shim was used, not the real Keychain", (SHIM_DIR / "argv").exists(), str(SHIM_DIR))
        argv = (SHIM_DIR / "argv").read_text() if (SHIM_DIR / "argv").exists() else ""
        stdin = (SHIM_DIR / "stdin").read_text() if (SHIM_DIR / "stdin").exists() else ""
        check("remember never puts the passphrase in argv", PASSPHRASE not in argv, argv)
        check("the raw passphrase does not cross on stdin either", PASSPHRASE not in stdin, stdin)
        check("it travels hex-encoded", PASSPHRASE.encode().hex() in stdin, stdin)
        check("the item is created asking for approval (-T \"\")", '-T' in stdin and '""' in stdin, stdin)

        set_keychain("value", PASSPHRASE)
        r = cred(kc, "run", "svc", "--", "printenv", "TOKEN")
        check(
            "a locked run opens the window itself, with no terminal",
            r.stdout.strip() == "***",
            f"exit={r.returncode} out={r.stdout!r} err={r.stderr!r}",
        )
        check("the window opened", (kc / "hold.sock").exists())
        log = (kc / "unlock.log").read_text()
        check("the audit trail names what opened it", "run svc" in log, log)

        # Denied must mean denied: no window, and no silent fallback to a prompt.
        cred(kc, "lock")
        set_keychain("denied")
        r = cred(kc, "run", "svc", "--", "printenv", "TOKEN")
        check("a denied approval fails as LOCKED", r.returncode == 2 and "LOCKED" in r.stderr, f"exit={r.returncode} err={r.stderr!r}")
        check("a denial opens no window", not (kc / "hold.sock").exists())

        # Nothing remembered and no terminal: LOCKED, never a run without credentials.
        set_keychain("absent")
        r = cred(kc, "run", "svc", "--", "printenv", "TOKEN")
        check(
            "no remembered passphrase and no terminal fails as LOCKED",
            r.returncode == 2 and "LOCKED" in r.stderr,
            f"exit={r.returncode} err={r.stderr!r}",
        )

        # 15. `cred unlock --for` — the window length is asked for, not assumed.
        long = pathlib.Path(td) / "longdir"
        profile(long, "q", "URL=https://api.example.com\nallow=printenv\n")
        on_a_real_tty(["init"], long, ["pw", "pw"])
        code, out = on_a_real_tty(["unlock", "--for", "3600"], long, ["pw"])
        check("unlock --for opens a window", code == 0 and (long / "hold.sock").exists(), out)
        check("it reports the longer window", "relocks 3600s" in out, out)
        holder_pid = int((long / "hold.pid").read_text().strip())
        holder_env = subprocess.run(["ps", "-Eww", "-p", str(holder_pid)],
                                    capture_output=True, text=True).stdout
        check("the holder was actually given the long window", "CRED_TTL=3600" in holder_env,
              holder_env[:300])
        check("the audit trail records the requested length", "--for 3600" in (long / "unlock.log").read_text(),
              (long / "unlock.log").read_text())
        cred(long, "lock")

        for bad, want in ((["--for"], "usage"),
                          (["--for", "0"], "whole number"),
                          (["--for", "abc"], "whole number"),
                          (["--for", "10", "extra"], "usage"),
                          (["--nope"], "usage")):
            r = cred(long, "unlock", *bad)
            check(
                f"unlock {' '.join(bad)} is rejected",
                r.returncode != 0 and want in r.stderr,
                f"{bad} exit={r.returncode} err={r.stderr!r}",
            )

    # 16. The cleanup trap has to survive a space in $TMPDIR/$CRED_DIR. It used
    # to hold its paths in an unquoted string, so `rm -f $list` word-split on the
    # space and failed silently: `cred add` exited 0 with the whole decrypted
    # vault still on disk, forever. Every other section here uses a space-free
    # tempfile path, which is why nothing caught it.
    with tempfile.TemporaryDirectory() as td:
        spaced = pathlib.Path(td) / "space dir"
        spaced_tmp = spaced / "tmp"
        spaced_tmp.mkdir(parents=True)
        spaced_cred = spaced / "cred dir"
        extra = {"TMPDIR": str(spaced_tmp)}
        code, out = on_a_real_tty(["init"], spaced_cred, ["pw", "pw"], extra=extra)
        check("a spaced CRED_DIR still inits", code == 0, out)
        code, out = on_a_real_tty(["add", "spaced", "TOK"], spaced_cred, [SECRET, "pw"], extra=extra)
        check("add succeeds with a space in CRED_DIR and TMPDIR", code == 0, out)
        left = sorted(p.name for p in spaced_tmp.glob("cred.*"))
        check("no temp file survives a spaced add", not left, str(left))
        on_disk = [p for p in spaced.rglob("*") if p.is_file() and SECRET in p.read_text(errors="ignore")]
        check("no plaintext vault survives a spaced add", not on_disk, str(on_disk))

    # 17. The cleanup trap must be safe on bash 3.2 as well: macOS's /bin/bash
    # (what `#!/usr/bin/env bash` resolves to on a stock machine) treats
    # `"${TMPFILES[@]}"` on an empty array as an unbound variable under `set -u`.
    # The trap runs on every exit, so that broke *every* invocation that
    # registered no temp file — list, lock, --help — with exit 1.
    stock_bash = pathlib.Path("/bin/bash")
    if not stock_bash.is_file():
        print("  skip /bin/bash is not present")
    else:
        with tempfile.TemporaryDirectory() as td:
            bare = pathlib.Path(td) / "bare"
            bare.mkdir()
            for args in (["--help"], ["list"]):
                r = subprocess.run(
                    [str(stock_bash), str(CRED), *args],
                    env=base_env(bare),
                    stdin=subprocess.DEVNULL,
                    capture_output=True,
                    text=True,
                )
                check(
                    f"/bin/bash: cred {' '.join(args)} exits 0 with an empty temp list",
                    r.returncode == 0,
                    f"exit={r.returncode} err={r.stderr!r}",
                )
                check(
                    f"/bin/bash: cred {' '.join(args)} hits no unbound variable",
                    "unbound variable" not in r.stderr + r.stdout,
                    r.stderr,
                )

    print(f"\n  {PASS} passed, {len(FAILS)} failed")
    return 1 if FAILS else 0


if __name__ == "__main__":
    sys.exit(main())
