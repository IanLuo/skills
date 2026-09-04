//! cred-run — resolve a profile's secrets, inject them into a child process
//! environment, and scrub their values from the child's stdout/stderr.
//!
//! Secrets are read from the transient "unlocked" cache (`$CRED_DIR/unlocked`)
//! that `cred unlock` writes. It is absent when locked, and its mtime enforces
//! the 300s relock window — a missing or stale cache fails fast as "locked",
//! never hangs. Secret values never appear in this process's argv or stdout —
//! they exist only in the child's `envp` (the one safe channel on Unix) and in
//! this process's memory for redaction.

use std::env;
use std::fs;
use std::io::{BufRead, BufReader, Read, Write};
use std::path::PathBuf;
use std::process::{Command, Stdio};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, SystemTime};

const TTL: Duration = Duration::from_secs(300);
const MIN_SCRUB_LEN: usize = 6;
const REDACTED: &str = "***";

type SecretMap = Vec<(String, String)>; // (VAR, value)

fn die(msg: &str, code: i32) -> ! {
    eprintln!("cred: {msg}");
    std::process::exit(code);
}

fn usage() {
    eprintln!(
        "usage: cred run <profile> -- <cmd> [args]\n\
         \n\
         Injects profile vars + vault secrets into <cmd>'s environment and scrubs\n\
         secret values from its stdout and stderr. There is no get — values are\n\
         never printed.\n\
         \n\
         Examples:\n\
           cred run github -- gh api user\n\
           cred run github -- sh -c 'curl -H \"Authorization: Bearer $GH_TOKEN\" ...'\n\
         \n\
         The profile must have an 'allow =' line listing permitted commands; the\n\
         first word of <cmd> must match it. A locked vault fails fast with\n\
         'vault is LOCKED' — ask the human to run 'cred unlock'.\n\
         \n\
         Full guide: skills/credentials/references/usage.md"
    );
}

fn load_profile(profile_dir: &str, name: &str) -> (Vec<(String, String)>, Vec<String>, Vec<String>) {
    let path = PathBuf::from(profile_dir).join(format!("{name}.env"));
    let text = fs::read_to_string(&path).unwrap_or_else(|_| {
        die(&format!("no profile '{name}' at {}", path.display()), 1)
    });

    let mut vars: Vec<(String, String)> = Vec::new();
    let mut secrets: Vec<String> = Vec::new();
    let mut allow: Vec<String> = Vec::new();

    for raw in text.lines() {
        let line = raw.trim();
        if line.is_empty() || line.starts_with('#') || !line.contains('=') {
            continue;
        }
        let (key, val) = line.split_once('=').unwrap();
        let key = key.trim();
        let val = strip_inline_comment(val.trim()).trim();
        if key == "allow" {
            allow = val.split_whitespace().map(str::to_string).collect();
        } else if val == "@secret" {
            secrets.push(key.to_string());
        } else {
            vars.push((key.to_string(), val.to_string()));
        }
    }
    (vars, secrets, allow)
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

/// Read the unlocked cache into (profile, var, value) triples. Returns None when
/// the cache is missing or its mtime is older than TTL (deleting the stale cache
/// so a later run fails clean rather than using expired secrets).
fn read_cache(unlocked: &str) -> Option<Vec<(String, String, String)>> {
    let meta = fs::metadata(unlocked).ok()?;
    let mtime = meta.modified().ok()?;
    if SystemTime::now().duration_since(mtime).map(|d| d > TTL).unwrap_or(true) {
        let _ = fs::remove_file(unlocked);
        return None;
    }
    let text = fs::read_to_string(unlocked).ok()?;
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
    Some(out)
}

fn resolve_secret(cache: &[(String, String, String)], profile: &str, var: &str) -> String {
    for (p, v, val) in cache {
        if p == profile && v == var {
            return val.clone();
        }
    }
    die(
        &format!("no secret for {profile}/{var} — add it with 'cred add {profile} {var}'"),
        3,
    )
}

fn redact(line: &str, secrets: &[String]) -> String {
    let mut out = line.to_string();
    for v in secrets {
        if v.len() >= MIN_SCRUB_LEN {
            out = out.replace(v, REDACTED);
        }
    }
    out
}

fn stream_out<R: Read + Send + 'static>(
    reader: R,
    out: Arc<Mutex<dyn Write + Send>>,
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
        let scrubbed = redact(&line, &secrets);
        let mut guard = out.lock().unwrap();
        let _ = guard.write_all(scrubbed.as_bytes());
        let _ = guard.flush();
    }
}

fn run(profile: &str, argv: &[String], profile_dir: &str, unlocked: &str) -> i32 {
    if !valid_profile(profile) {
        die(&format!("invalid profile name '{profile}'"), 1);
    }
    let (vars, secret_vars, allow) = load_profile(profile_dir, profile);
    if argv.is_empty() {
        die("no command given after '--'", 1);
    }
    if allow.is_empty() {
        die("profile has no 'allow =' line — add one (e.g. 'allow = gh git curl') so credentials are scoped", 5);
    }
    if !allow.contains(&argv[0]) {
        die(
            &format!("'{}' not in profile allow-list ({})", argv[0], allow.join(" ")),
            6,
        );
    }

    let mut secrets: SecretMap = Vec::new();
    if !secret_vars.is_empty() {
        // A profile with no secrets must work while the vault is locked.
        let cache = read_cache(unlocked).unwrap_or_else(|| {
            die(
                "vault is LOCKED — run 'cred unlock' first (this is the intended gate)",
                2,
            )
        });
        for var in &secret_vars {
            secrets.push((var.clone(), resolve_secret(&cache, profile, var)));
        }
    }
    let values: Vec<String> = secrets.iter().map(|(_, v)| v.clone()).collect();

    let mut cmd = Command::new(&argv[0]);
    cmd.args(&argv[1..]);
    for (k, v) in &vars {
        cmd.env(k, v);
    }
    for (k, v) in &secrets {
        cmd.env(k, v);
    }
    cmd.stdout(Stdio::piped()).stderr(Stdio::piped());
    // stdin inherits the terminal so the child can prompt/read if it needs to.

    let mut child = match cmd.spawn() {
        Ok(c) => c,
        Err(e) => die(&format!("failed to run '{}': {e}", argv[0]), 1),
    };

    let stdout = child.stdout.take().unwrap();
    let stderr = child.stderr.take().unwrap();
    let out_writer: Arc<Mutex<dyn Write + Send>> = Arc::new(Mutex::new(std::io::stdout()));
    let err_writer: Arc<Mutex<dyn Write + Send>> = Arc::new(Mutex::new(std::io::stderr()));

    let to1 = out_writer.clone();
    let vals1 = values.clone();
    let t1 = thread::spawn(move || stream_out(stdout, to1, vals1));
    let vals2 = values;
    let t2 = thread::spawn(move || stream_out(stderr, err_writer, vals2));

    let status = child.wait();
    let _ = t1.join();
    let _ = t2.join();
    status.map(|s| s.code().unwrap_or(1)).unwrap_or(1)
}

fn main() {
    let args: Vec<String> = env::args().collect();
    if args.len() < 2 || args[1] == "-h" || args[1] == "--help" || args[1] == "help" {
        usage();
        std::process::exit(if args.len() < 2 { 1 } else { 0 });
    }
    if args[1] != "run" {
        usage();
        std::process::exit(1);
    }

    let rest = &args[2..];
    let argv: &[String] = match rest.iter().position(|a| a == "--") {
        Some(idx) => &rest[idx + 1..],
        None => die("usage: cred run <profile> -- <cmd> [args]", 1),
    };
    let profile = rest[0].clone();

    let cred_dir = env::var("CRED_DIR")
        .unwrap_or_else(|_| format!("{}/.config/cred", env::var("HOME").unwrap_or_default()));
    let profile_dir = format!("{cred_dir}/profiles");
    let unlocked = format!("{cred_dir}/unlocked");

    let code = run(&profile, argv, &profile_dir, &unlocked);
    std::process::exit(code);
}
