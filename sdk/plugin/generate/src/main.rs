//! Writes the plugin SDK's Rust from the contract's protos in `api/`: the
//! plugin contract and the types of the other modules it names, and the
//! canonical RPC status. What it writes is committed in the SDK
//! (`src/gen`), so that building the SDK reads no proto and runs no build
//! script.
//!
//! ```text
//! frontseat-plugin-generate           write src/gen anew
//! frontseat-plugin-generate --check   fail, naming each file, where src/gen
//!                                     is not what it would write now
//! ```

use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

/// The module of the buf workspace that holds the plugin contract: every
/// proto in it is written.
const CONTRACT: &str = "plugin-proto";

/// The modules of the buf workspace the protos are read from.
const MODULES: [&str; 3] = [CONTRACT, "type-proto", "third-party-proto"];

/// The files Rust is written for beyond the plugin contract's own (each
/// `.proto` of `plugin-proto`, found there): what the SDK offers that the
/// contract does not import, the canonical codes and the details of a
/// failure. What a written file imports is written with it.
const ALSO: [&str; 2] = ["google/rpc/code.proto", "google/rpc/error_details.proto"];

fn main() -> ExitCode {
    // The repository it writes for is the one it is told (--root), or the
    // one it runs at: the contracts and the SDK are found there, never by
    // looking up.
    let usage = || {
        eprintln!("usage: frontseat-plugin-generate [--root DIR] [--check]");
        ExitCode::from(2)
    };
    let (mut root, mut check) = (None, false);
    let mut args = std::env::args().skip(1);
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--check" => check = true,
            "--root" => match args.next() {
                Some(dir) => root = Some(std::path::PathBuf::from(dir)),
                None => return usage(),
            },
            _ => return usage(),
        }
    }
    let root = match root.map_or_else(std::env::current_dir, Ok) {
        Ok(root) if root.join("go.work").is_file() => root,
        _ => {
            eprintln!("frontseat-plugin-generate: the repository holds go.work: run this at its root, or name it with --root");
            return ExitCode::from(2);
        }
    };
    let api = root.join("api");
    let committed = root.join("sdk/plugin/src/gen");
    let written = match generate(&api) {
        Ok(written) => written,
        Err(err) => {
            eprintln!("frontseat-plugin-generate: {err}");
            return ExitCode::FAILURE;
        }
    };
    let result = if check {
        compare(&committed, &written)
    } else {
        write(&committed, &written)
    };
    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(err) => {
            eprintln!("frontseat-plugin-generate: {err}");
            ExitCode::FAILURE
        }
    }
}

/// Generates the Rust, and returns each file by its name: the package it
/// holds, as `type` is written plainly and not as Rust's `r#type`.
fn generate(api: &Path) -> Result<BTreeMap<String, Vec<u8>>, Box<dyn std::error::Error>> {
    let includes: Vec<PathBuf> = MODULES.iter().map(|module| api.join(module)).collect();
    let mut files = contract(&api.join(CONTRACT))?;
    files.extend(ALSO.iter().map(|file| file.to_string()));
    let descriptors = protox::compile(files, includes)?;
    // A directory of this run's own: made here, and refused where one of
    // its name is there already, so that nothing another put there is read.
    let made = std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH)?;
    let out = std::env::temp_dir().join(format!(
        "frontseat-plugin-generate-{}-{}",
        std::process::id(),
        made.as_nanos()
    ));
    fs::create_dir(&out)?;
    let generated = prost_build::Config::new()
        .out_dir(&out)
        .btree_map(["."])
        .disable_comments([".google.rpc"])
        .compile_fds(descriptors);
    let read = generated.map_err(Into::into).and_then(|()| {
        let mut files = BTreeMap::new();
        for entry in fs::read_dir(&out)? {
            let path = entry?.path();
            let name = path
                .file_name()
                .and_then(|name| name.to_str())
                .ok_or("a file prost wrote is not named in UTF-8")?
                .replace("r#", "");
            files.insert(name, fs::read(&path)?);
        }
        Ok(files)
    });
    fs::remove_dir_all(&out)?;
    read
}

/// The plugin contract's protos: each `.proto` beneath its module, by its
/// path there, in order.
fn contract(module: &Path) -> Result<Vec<String>, Box<dyn std::error::Error>> {
    let mut found = Vec::new();
    let mut directories = vec![module.to_path_buf()];
    while let Some(directory) = directories.pop() {
        for entry in fs::read_dir(&directory)? {
            let path = entry?.path();
            if path.is_dir() {
                directories.push(path);
            } else if path
                .extension()
                .is_some_and(|extension| extension == "proto")
            {
                let within = path.strip_prefix(module)?;
                let parts: Vec<&str> = within
                    .components()
                    .map(|part| {
                        part.as_os_str()
                            .to_str()
                            .ok_or("a proto is not named in UTF-8")
                    })
                    .collect::<Result<_, _>>()?;
                found.push(parts.join("/"));
            }
        }
    }
    found.sort();
    Ok(found)
}

/// Fails, naming each file, where the committed directory is not what was
/// generated: a file that differs, one missing, and one generated no more.
fn compare(committed: &Path, written: &BTreeMap<String, Vec<u8>>) -> Result<(), String> {
    let mut differ = Vec::new();
    for (name, content) in written {
        if fs::read(committed.join(name)).ok().as_ref() != Some(content) {
            differ.push(name.clone());
        }
    }
    let there = fs::read_dir(committed).map_err(|err| format!("{}: {err}", committed.display()))?;
    for entry in there {
        let name = entry
            .map_err(|err| err.to_string())?
            .file_name()
            .to_string_lossy()
            .into_owned();
        if !written.contains_key(&name) {
            differ.push(name);
        }
    }
    if differ.is_empty() {
        return Ok(());
    }
    differ.sort();
    Err(format!(
        "the SDK's generated Rust is not what the protos give now: {} in {}; run `mise run protos`",
        differ.join(", "),
        committed.display()
    ))
}

/// Writes the generated files in place of what the committed directory
/// held.
fn write(committed: &Path, written: &BTreeMap<String, Vec<u8>>) -> Result<(), String> {
    let failed = |err: std::io::Error| format!("{}: {err}", committed.display());
    // The directory is this generator's own output and nothing else's:
    // what it held is what it wrote before.
    if committed.exists() {
        fs::remove_dir_all(committed).map_err(failed)?;
    }
    fs::create_dir_all(committed).map_err(failed)?;
    for (name, content) in written {
        fs::write(committed.join(name), content).map_err(failed)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn scratch(name: &str) -> PathBuf {
        let made = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let dir = std::env::temp_dir().join(format!(
            "generate-test-{name}-{}-{made}",
            std::process::id()
        ));
        fs::create_dir(&dir).unwrap();
        dir
    }

    // Every proto of the contract is written, a new one too: the list is
    // read from the directory, not written here.
    #[test]
    fn every_proto_of_the_contract_is_found() {
        let module = scratch("contract");
        fs::create_dir_all(module.join("frontseat/plugin/v1alpha1")).unwrap();
        for file in ["plugin.proto", "new.proto", "README.md"] {
            fs::write(module.join("frontseat/plugin/v1alpha1").join(file), "").unwrap();
        }
        let found = contract(&module).unwrap();
        fs::remove_dir_all(&module).unwrap();
        assert_eq!(
            found,
            [
                "frontseat/plugin/v1alpha1/new.proto",
                "frontseat/plugin/v1alpha1/plugin.proto"
            ]
        );
    }

    // What is committed is what is generated, or the check names each
    // file that is not: one changed, one generated no more, one missing.
    // The control is the directory as written.
    #[test]
    fn the_check_names_each_file_not_as_generated() {
        let written: BTreeMap<String, Vec<u8>> = [("a.rs", "a"), ("b.rs", "b")]
            .into_iter()
            .map(|(name, content)| (name.to_string(), content.as_bytes().to_vec()))
            .collect();
        let committed = scratch("committed").join("gen");
        write(&committed, &written).unwrap();
        assert_eq!(compare(&committed, &written), Ok(()));
        fs::write(committed.join("a.rs"), "planted").unwrap();
        fs::write(committed.join("stale.rs"), "old").unwrap();
        fs::remove_file(committed.join("b.rs")).unwrap();
        let refused = compare(&committed, &written).unwrap_err();
        fs::remove_dir_all(committed.parent().unwrap()).unwrap();
        assert!(refused.contains("a.rs, b.rs, stale.rs"), "{refused}");
    }

    // Where the test is told its repository is (FRONTSEAT_TEST_SRCDIR):
    // what it reads beyond its crate is read there, never by looking up
    // from its crate.
    fn repository() -> std::path::PathBuf {
        let told = std::env::var("FRONTSEAT_TEST_SRCDIR").expect(
            "FRONTSEAT_TEST_SRCDIR is not set: a test is told where its repository is by what runs it (frontseat test, or a mise task)",
        );
        std::path::absolute(told).unwrap()
    }

    // The generator writes what the committed SDK holds now: the
    // contracts and the SDK's committed Rust.
    #[test]
    fn the_committed_rust_is_what_is_generated() {
        let root = repository();
        let written = generate(&root.join("api")).unwrap();
        assert_eq!(compare(&root.join("sdk/plugin/src/gen"), &written), Ok(()));
    }

    // The number after `before` in a text, once: none where it is not
    // said, or said more than once.
    fn number_after(text: &str, before: &str) -> Option<u32> {
        let mut said = text.match_indices(before).map(|(at, lead)| {
            let rest = &text[at + lead.len()..];
            rest[..rest
                .find(|c: char| !c.is_ascii_digit())
                .unwrap_or(rest.len())]
                .parse()
        });
        match (said.next(), said.next()) {
            (Some(Ok(number)), None) => Some(number),
            _ => None,
        }
    }

    // The contract's ABI document says which ABI it is, and it is the
    // one the SDK written from the contract speaks.
    #[test]
    fn the_abi_document_is_of_the_abi_the_sdk_speaks() {
        let root = repository();
        let read = |file: &str| fs::read_to_string(root.join(file)).unwrap();
        let document = number_after(&read("api/plugin-proto/ABI.md"), "This document is ABI ");
        let sdk = number_after(&read("sdk/plugin/src/lib.rs"), "pub const ABI: u32 = ");
        assert!(
            document.is_some(),
            "ABI.md does not say once which ABI it is"
        );
        assert_eq!(document, sdk);
        assert_eq!(number_after("is ABI 7. is ABI 8.", "is ABI "), None);
    }
}
