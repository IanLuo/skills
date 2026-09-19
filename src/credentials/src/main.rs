//! cred-run — the executor behind `cred run`, in two halves that share one wire
//! protocol.
//!
//! `cred-run hold` is the executor. It is started by `cred unlock`, reads the
//! decrypted vault from stdin, and keeps it in this process's memory for the
//! relock window — the vault is never written to disk, which is the whole point
//! of the split. It owns everything that touches a secret: loading a profile,
//! the allow-list, injecting into a child's `envp`, and scrubbing the child's
//! output before it goes anywhere.
//!
//! `cred-run run` is a thin client. It connects, sends {profile, argv, tty},
//! relays the scrubbed frames to its own stdout/stderr, and exits with the
//! holder's code. It never sees a secret value, so a client that is *not*
//! cred-run — anything that can open the socket — still gets scrubbed, allow-
//! listed output. The scrub is not a property of the caller.
//!
//! cred.sh owns the contract — where profiles live, where the holder socket is,
//! how long the window lasts, and which token in a profile means "resolve from
//! the vault" — and passes it in as CRED_* environment variables. There are no
//! defaults on purpose: a missing variable means this binary was reached without
//! going through cred.sh, and that should fail loudly rather than silently fall
//! back to a constant that can drift.
//!
//! Secret values never appear in argv or on stdout. They exist in the child's
//! `envp` (the one safe channel on Unix) and in the holder's memory for redaction.

use std::env;
use std::fs;
use std::io::{self, BufRead, BufReader, Read, Write};
use std::net::Shutdown;
use std::os::unix::fs::PermissionsExt;
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Duration;

const MIN_SCRUB_LEN: usize = 6;
const REDACTED: &str = "***";

/// A failure that belongs to one request, not to the holder: the message goes to
/// the client's stderr and the code becomes its exit status. A `die` here would
/// take the whole relock window down with it.
type RequestError = (String, i32);

type Vault = Vec<(String, String, String)>; // (profile, var, value)
type SecretMap = Vec<(String, String)>; // (VAR, value)

// ── The wire protocol ────────────────────────────────────────────────────
//
// One request per connection. Every field is a u32 big-endian length followed by
// that many bytes, so an argument containing a newline or a NUL-ish byte is not
// a framing problem.
//
//   client → holder:  tty, profile, argc, argv…
//   holder → client:  ('o'|'e', len, bytes)…, then ('x', i32 exit code)
//
// The client's tty path travels as a string rather than as a passed file
// descriptor: reopening it on the holder side keeps stdin interactive, and pure
// std has no way to send an fd.

const TAG_STDOUT: u8 = b'o';
const TAG_STDERR: u8 = b'e';
const TAG_EXIT: u8 = b'x';

fn write_field(w: &mut impl Write, s: &str) -> io::Result<()> {
    w.write_all(&(s.len() as u32).to_be_bytes())?;
    w.write_all(s.as_bytes())
}

fn read_field(r: &mut impl Read) -> io::Result<String> {
    let mut len = [0u8; 4];
    r.read_exact(&mut len)?;
    let mut buf = vec![0u8; u32::from_be_bytes(len) as usize];
    r.read_exact(&mut buf)?;
    Ok(String::from_utf8_lossy(&buf).into_owned())
}

/// Write one frame under the lock, so concurrent stdout/stderr threads can never
/// interleave half a frame.
fn write_frame(out: &Arc<Mutex<UnixStream>>, tag: u8, bytes: &[u8]) {
    let mut guard = match out.lock() {
        Ok(g) => g,
        Err(_) => return,
    };
    let _ = guard.write_all(&[tag]);
    let _ = guard.write_all(&(bytes.len() as u32).to_be_bytes());
    let _ = guard.write_all(bytes);
}

fn write_exit(out: &Arc<Mutex<UnixStream>>, code: i32) {
    let mut guard = match out.lock() {
        Ok(g) => g,
        Err(_) => return,
    };
    let _ = guard.write_all(&[TAG_EXIT]);
    let _ = guard.write_all(&code.to_be_bytes());
    let _ = guard.flush();
}

// ── Config, each half requiring only what it uses ────────────────────────

/// What `hold` needs: everything that touches a profile or the vault.
struct HoldCfg {
    profile_dir: String,
    hold_sock: String,
    ttl: Duration,
    marker: String,
}

impl HoldCfg {
    fn from_env() -> Self {
        Self {
            profile_dir: req_env("CRED_PROFILE_DIR"),
            hold_sock: req_env("CRED_HOLD_SOCK"),
            ttl: Duration::from_secs(
                req_env("CRED_TTL")
                    .parse()
                    .unwrap_or_else(|_| die("CRED_TTL must be a whole number of seconds", 1)),
            ),
            marker: req_env("CRED_SECRET_MARKER"),
        }
    }
}

fn req_env(name: &str) -> String {
    env::var(name).unwrap_or_else(|_| {
        die(
            &format!(
                "{name} is not set — cred-run is the executor behind `cred run` and must be reached through it"
            ),
            1,
        )
    })
}

fn die(msg: &str, code: i32) -> ! {
    eprintln!("cred: {msg}");
    std::process::exit(code);
}

fn usage() {
    eprintln!(
        "usage: cred run <profile> -- <cmd> [args]\n\
         \n\
         This is the client half of `cred run`. cred.sh supplies the holder\n\
         socket as CRED_HOLD_SOCK; running it directly fails rather than\n\
         guessing one.\n\
         \n\
         Injects profile vars + vault secrets into <cmd>'s environment and scrubs\n\
         secret values from its stdout and stderr — done by the holder, not here,\n\
         so the values never reach this process. There is no get: never printed.\n\
         \n\
         Examples:\n\
           cred run github -- gh api user\n\
           cred run github -- sh -c 'curl -H \"Authorization: Bearer $GH_TOKEN\" ...'\n\
         \n\
         The profile must have an 'allow =' line listing permitted commands; the\n\
         first word of <cmd> must match it. A missing or expired holder fails fast\n\
         with 'vault is LOCKED' — ask the human to run 'cred unlock'.\n\
         \n\
         `cred-run hold` is the other half: started by `cred unlock`, it reads the\n\
         vault on stdin and serves requests until the relock window closes. It is\n\
         not a user-facing verb and takes no arguments.\n\
         \n\
         Full guide: skills/credentials/references/usage.md"
    );
}

// ── The vault, in memory ─────────────────────────────────────────────────

/// Parse the decrypted vault into (profile, var, value) triples. The header and
/// any comment lines are cred.sh's; they are skipped here, not interpreted.
fn parse_vault(text: &str) -> Vault {
    let mut out = Vec::new();
    for raw in text.lines() {
        if raw.is_empty() || raw.starts_with('#') {
            continue;
        }
        let mut parts = raw.splitn(3, '\t');
        let (Some(profile), Some(var), Some(val)) = (parts.next(), parts.next(), parts.next())
        else {
            continue;
        };
        out.push((profile.to_string(), var.to_string(), val.to_string()));
    }
    out
}

fn resolve_secret(vault: &Vault, profile: &str, var: &str) -> Result<String, RequestError> {
    for (p, v, val) in vault {
        if p == profile && v == var {
            return Ok(val.clone());
        }
    }
    Err((
        format!("no secret for {profile}/{var} — add it with 'cred add {profile} {var}'"),
        3,
    ))
}

// ── Profiles (cred.sh's plaintext half) ──────────────────────────────────

/// A profile's plaintext half: the vars to pass through, the names of the vars to
/// resolve from the vault, and the command allow-list. Named rather than returned
/// as a tuple, because three adjacent `Vec`s are indistinguishable at the call
/// site and the order is the whole contract.
struct Profile {
    vars: Vec<(String, String)>,
    secret_vars: Vec<String>,
    allow: Vec<String>,
}

fn load_profile(profile_dir: &str, name: &str, marker: &str) -> Result<Profile, RequestError> {
    let path = PathBuf::from(profile_dir).join(format!("{name}.env"));
    let text = fs::read_to_string(&path).map_err(|_| {
        (
            format!("no profile '{name}' at {}", path.display()),
            1,
        )
    })?;

    let mut profile = Profile {
        vars: Vec::new(),
        secret_vars: Vec::new(),
        allow: Vec::new(),
    };

    for raw in text.lines() {
        let line = raw.trim();
        if line.is_empty() || line.starts_with('#') || !line.contains('=') {
            continue;
        }
        let (key, val) = line.split_once('=').unwrap();
        let key = key.trim();
        let val = strip_inline_comment(val.trim()).trim();
        if key == "allow" {
            profile.allow = val.split_whitespace().map(str::to_string).collect();
        } else if val == marker {
            profile.secret_vars.push(key.to_string());
        } else {
            profile.vars.push((key.to_string(), val.to_string()));
        }
    }
    Ok(profile)
}

fn valid_profile(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')
}

/// A `#` preceded by whitespace starts a trailing comment (whole-line `#`
/// comments are skipped above). Values keep any other `#` (e.g. in a URL).
fn strip_inline_comment(v: &str) -> &str {
    let bytes = v.as_bytes();
    for i in 1..bytes.len() {
        if bytes[i] == b'#' && bytes[i - 1].is_ascii_whitespace() {
            return &v[..i];
        }
    }
    v
}

// ── Scrubbing ────────────────────────────────────────────────────────────

fn redact(line: &str, secrets: &[String]) -> String {
    let mut out = line.to_string();
    for v in secrets {
        if v.len() >= MIN_SCRUB_LEN {
            out = out.replace(v, REDACTED);
        }
    }
    out
}

/// Read the child's stream line by line, redact, and ship scrubbed frames. The
/// redaction has to happen here, on the holder's side of the socket: a client
/// that skipped it would be reading the child's raw output.
fn stream_out<R: Read + Send + 'static>(
    reader: R,
    out: Arc<Mutex<UnixStream>>,
    tag: u8,
    secrets: Vec<String>,
) {
    let mut secrets = secrets;
    secrets.sort_by_key(|b| std::cmp::Reverse(b.len()));
    let mut reader = BufReader::new(reader);
    let mut buf: Vec<u8> = Vec::new();
    loop {
        buf.clear();
        if reader.read_until(b'\n', &mut buf).unwrap_or(0) == 0 {
            break;
        }
        // Byte-level read: invalid UTF-8 is lossy-replaced, never truncating
        // the stream (BufReader::lines() dropped the rest on the first
        // non-UTF-8 byte, hiding the child's own error output).
        let line = String::from_utf8_lossy(&buf);
        write_frame(&out, tag, redact(&line, &secrets).as_bytes());
    }
}

// ── Serving one request ──────────────────────────────────────────────────

/// Open the client's tty for the child's stdin. The holder is detached, so its
/// own stdin is not the user's terminal — without this, an interactive child
/// (`gh auth login`) would read from nowhere. A client whose stdin is not a tty
/// sends an empty path and the child gets /dev/null.
fn child_stdin(tty: &str) -> Stdio {
    if tty.is_empty() {
        return Stdio::null();
    }
    match fs::File::open(tty) {
        Ok(f) => Stdio::from(f),
        Err(_) => Stdio::null(),
    }
}

fn serve(stream: UnixStream, cfg: &HoldCfg, vault: &Vault) -> i32 {
    let mut reader = match stream.try_clone() {
        Ok(s) => s,
        Err(e) => {
            eprintln!("cred: cannot clone connection: {e}");
            return 1;
        }
    };
    let out = Arc::new(Mutex::new(stream));

    let (tty, profile, argv) = match read_request(&mut reader) {
        Ok(r) => r,
        Err(e) => {
            write_frame(&out, TAG_STDERR, format!("cred: {e}\n").as_bytes());
            write_exit(&out, 1);
            return 1;
        }
    };

    match execute(&profile, &argv, &tty, cfg, vault, &out) {
        Ok(code) => {
            write_exit(&out, code);
            code
        }
        Err((msg, code)) => {
            write_frame(&out, TAG_STDERR, format!("cred: {msg}\n").as_bytes());
            write_exit(&out, code);
            code
        }
    }
}

fn read_request(reader: &mut impl Read) -> io::Result<(String, String, Vec<String>)> {
    let tty = read_field(reader)?;
    let profile = read_field(reader)?;
    let mut len = [0u8; 4];
    reader.read_exact(&mut len)?;
    let argc = u32::from_be_bytes(len);
    let mut argv = Vec::with_capacity(argc as usize);
    for _ in 0..argc {
        argv.push(read_field(reader)?);
    }
    Ok((tty, profile, argv))
}

/// Do the work for one request. Returns the child's exit code, or the failure
/// that belongs to this request alone.
fn execute(
    profile_name: &str,
    argv: &[String],
    tty: &str,
    cfg: &HoldCfg,
    vault: &Vault,
    out: &Arc<Mutex<UnixStream>>,
) -> Result<i32, RequestError> {
    if !valid_profile(profile_name) {
        return Err((format!("invalid profile name '{profile_name}'"), 1));
    }
    if argv.is_empty() {
        return Err(("no command given after '--'".to_string(), 1));
    }
    let profile = load_profile(&cfg.profile_dir, profile_name, &cfg.marker)?;
    if profile.allow.is_empty() {
        return Err((
            "profile has no 'allow =' line — add one (e.g. 'allow = gh git curl') so credentials are scoped".to_string(),
            5,
        ));
    }
    if !profile.allow.contains(&argv[0]) {
        return Err((
            format!(
                "'{}' not in profile allow-list ({})",
                argv[0],
                profile.allow.join(" ")
            ),
            6,
        ));
    }

    let mut secrets: SecretMap = Vec::new();
    for var in &profile.secret_vars {
        secrets.push((var.clone(), resolve_secret(vault, profile_name, var)?));
    }
    let values: Vec<String> = secrets.iter().map(|(_, v)| v.clone()).collect();

    let mut cmd = Command::new(&argv[0]);
    cmd.args(&argv[1..]);
    for (k, v) in &profile.vars {
        cmd.env(k, v);
    }
    for (k, v) in &secrets {
        cmd.env(k, v);
    }
    cmd.stdin(child_stdin(tty));
    cmd.stdout(Stdio::piped()).stderr(Stdio::piped());

    let mut child = cmd
        .spawn()
        .map_err(|e| (format!("failed to run '{}': {e}", argv[0]), 1))?;

    let stdout = child.stdout.take().unwrap();
    let stderr = child.stderr.take().unwrap();

    let to1 = out.clone();
    let vals1 = values.clone();
    let t1 = thread::spawn(move || stream_out(stdout, to1, TAG_STDOUT, vals1));
    let vals2 = values;
    let to2 = out.clone();
    let t2 = thread::spawn(move || stream_out(stderr, to2, TAG_STDERR, vals2));

    let status = child.wait();
    let _ = t1.join();
    let _ = t2.join();
    Ok(status.map(|s| s.code().unwrap_or(1)).unwrap_or(1))
}

// ── hold: the executor ───────────────────────────────────────────────────

fn hold() -> ! {
    let cfg = HoldCfg::from_env();

    // The vault arrives on stdin from cred.sh, which has already decrypted and
    // checked it. Reading it whole and holding it in memory is the point: no
    // plaintext vault exists on disk for the life of the window.
    let mut text = String::new();
    if let Err(e) = io::stdin().read_to_string(&mut text) {
        die(&format!("cannot read the vault from stdin: {e}"), 1);
    }
    let vault = Arc::new(parse_vault(&text));
    drop(text);

    // A socket left behind by a holder that was killed would make every later
    // unlock fail with "address in use", so it goes first.
    let _ = fs::remove_file(&cfg.hold_sock);
    let listener = match UnixListener::bind(&cfg.hold_sock) {
        Ok(l) => l,
        Err(e) => die(&format!("cannot bind {}: {e}", cfg.hold_sock), 1),
    };
    if let Err(e) = fs::set_permissions(&cfg.hold_sock, fs::Permissions::from_mode(0o600)) {
        die(&format!("cannot restrict {}: {e}", cfg.hold_sock), 1);
    }

    let sock = cfg.hold_sock.clone();
    let ttl = cfg.ttl;
    thread::spawn(move || {
        // The window is fixed, not sliding: a holder that refreshed on use would
        // stay unlocked for as long as anything kept using it.
        thread::sleep(ttl);
        let _ = fs::remove_file(&sock);
        std::process::exit(0);
    });

    // Two clients at once are served by two threads; requests are independent,
    // and the child of one never sees another's secrets.
    let cfg = Arc::new(cfg);
    for conn in listener.incoming() {
        match conn {
            Ok(stream) => {
                let cfg = cfg.clone();
                let vault = vault.clone();
                thread::spawn(move || {
                    serve(stream, &cfg, &vault);
                });
            }
            Err(e) => eprintln!("cred: accept failed: {e}"),
        }
    }
    std::process::exit(0);
}

// ── run: the client ──────────────────────────────────────────────────────

/// The path of the terminal this process is talking to, or "" when stdin is not
/// one. No std API returns this, and on macOS `/dev/fd/0` is a device node rather
/// than a symlink, so `readlink` yields nothing — the C library owns the answer.
/// `ttyname_r` over `ttyname` to stay off libc's shared static buffer.
fn client_tty() -> String {
    extern "C" {
        fn ttyname_r(fd: i32, buf: *mut std::os::raw::c_char, len: usize) -> i32;
    }
    const BUF: usize = 1024;
    let mut buf = [0 as std::os::raw::c_char; BUF];
    // SAFETY: `buf` is BUF bytes, which is what ttyname_r is told; it NUL-terminates
    // on success, and a non-zero return means it wrote nothing to read.
    let rc = unsafe { ttyname_r(0, buf.as_mut_ptr(), BUF) };
    if rc != 0 {
        return String::new();
    }
    let bytes: Vec<u8> = buf.iter().take_while(|&&c| c != 0).map(|&c| c as u8).collect();
    String::from_utf8(bytes).unwrap_or_default()
}

fn run(hold_sock: &str, profile: &str, argv: &[String]) -> i32 {
    // A missing socket, a stale one from a killed holder, and an expired one all
    // land here: all three mean "the window is not open", and none of them may
    // hang or look like success.
    let mut stream = match UnixStream::connect(Path::new(hold_sock)) {
        Ok(s) => s,
        Err(_) => die(
            "vault is LOCKED — run 'cred unlock' first (this is the intended gate)",
            2,
        ),
    };

    let mut req = Vec::new();
    let _ = write_field(&mut req, &client_tty());
    let _ = write_field(&mut req, profile);
    let _ = req.write_all(&(argv.len() as u32).to_be_bytes());
    for a in argv {
        let _ = write_field(&mut req, a);
    }
    if let Err(e) = stream.write_all(&req) {
        die(&format!("cannot send the request to the holder: {e}"), 2);
    }
    let _ = stream.shutdown(Shutdown::Write);

    let mut frame = [0u8; 1];
    let mut stdout = io::stdout();
    let mut stderr = io::stderr();
    loop {
        if let Err(e) = stream.read_exact(&mut frame) {
            die(&format!("the holder stopped mid-answer: {e}"), 2);
        }
        match frame[0] {
            TAG_EXIT => {
                let mut code = [0u8; 4];
                if stream.read_exact(&mut code).is_err() {
                    return 1;
                }
                return i32::from_be_bytes(code);
            }
            tag @ (TAG_STDOUT | TAG_STDERR) => {
                let mut len = [0u8; 4];
                if stream.read_exact(&mut len).is_err() {
                    return 1;
                }
                let mut buf = vec![0u8; u32::from_be_bytes(len) as usize];
                if stream.read_exact(&mut buf).is_err() {
                    return 1;
                }
                let sink: &mut dyn Write = if tag == TAG_STDOUT { &mut stdout } else { &mut stderr };
                let _ = sink.write_all(&buf);
                let _ = sink.flush();
            }
            other => die(&format!("unexpected frame {other:#x} from the holder"), 2),
        }
    }
}

fn main() {
    let args: Vec<String> = env::args().collect();
    if args.len() < 2 || args[1] == "-h" || args[1] == "--help" || args[1] == "help" {
        usage();
        std::process::exit(if args.len() < 2 { 1 } else { 0 });
    }

    match args[1].as_str() {
        "hold" => hold(),
        "run" => {
            let rest = &args[2..];
            let argv: &[String] = match rest.iter().position(|a| a == "--") {
                Some(idx) => &rest[idx + 1..],
                None => die("usage: cred run <profile> -- <cmd> [args]", 1),
            };
            if rest.is_empty() || rest[0] == "--" {
                die("usage: cred run <profile> -- <cmd> [args]", 1);
            }
            let profile = rest[0].clone();
            let code = run(&req_env("CRED_HOLD_SOCK"), &profile, argv);
            std::process::exit(code);
        }
        _ => {
            usage();
            std::process::exit(1);
        }
    }
}
