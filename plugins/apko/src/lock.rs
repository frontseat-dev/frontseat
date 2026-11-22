//! The lock apko writes of an image's configuration (`apko lock`): each
//! package of each architecture by its address, with the checksum of each
//! of its sections and where the section lies in the file, and each
//! signing key with its content. There is no checksum of a whole package,
//! so a package is fetched whole and its sections cut out and checked
//! (`Download.parts`), each kept where apko's cache looks for it.

use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::join;
use serde::Deserialize;

/// The lock's name, beside the configuration it was written of.
pub(super) const FILE: &str = "apko.lock.json";

/// The architectures apko writes in a lock, as apk names them (apko
/// 26953696, `pkg/build/types/types.go`, `ToAPK`). apko builds for the one
/// it is told by these names, and installs nothing for a name it does not
/// know.
const ARCHITECTURES: [&str; 9] = [
    "aarch64",
    "armhf",
    "armv7",
    "loongarch64",
    "ppc64le",
    "riscv64",
    "s390x",
    "x86",
    "x86_64",
];

/// Where apko keeps what it fetched when it is told no other place: in
/// the cache directory of the action's home. An offline build reads what
/// the fetches left there.
const CACHE: &str = "~/.cache/dev.chainguard.go-apk";

#[derive(Deserialize)]
struct Document {
    version: String,
    config: Option<Written>,
    contents: Contents,
}

#[derive(Deserialize)]
struct Written {
    name: String,
}

#[derive(Deserialize)]
struct Contents {
    #[serde(default)]
    keyring: Vec<KeyEntry>,
    #[serde(default)]
    packages: Vec<PackageEntry>,
}

#[derive(Deserialize)]
struct KeyEntry {
    url: String,
    content: Option<String>,
}

#[derive(Deserialize)]
struct PackageEntry {
    url: String,
    architecture: String,
    control: SectionEntry,
    data: SectionEntry,
    checksum: String,
}

#[derive(Deserialize)]
struct SectionEntry {
    range: String,
    checksum: String,
}

/// A lock, as far as an image is built from it.
pub(super) struct Lock {
    /// The configuration it was written of, a file beside it.
    pub config: String,
    pub keys: Vec<Key>,
    pub packages: Vec<Package>,
}

/// A signing key the lock holds whole.
pub(super) struct Key {
    /// The name of its fetch.
    pub fetch: String,
    /// The directory of apko's cache it is read from, in the home.
    pub directory: String,
    pub content: String,
}

/// A package of one architecture.
#[derive(PartialEq)]
pub(super) struct Package {
    /// The name of its fetch.
    pub fetch: String,
    pub url: String,
    pub architecture: String,
    /// The directory of apko's cache its sections are read from, in the
    /// home.
    pub directory: String,
    pub control: Section,
    pub data: Section,
}

/// A section of a package: where it lies in the file, and its checksum
/// in hex, a sha1 for the control section and a sha256 for the data.
#[derive(PartialEq)]
pub(super) struct Section {
    pub offset: u64,
    pub length: u64,
    pub hex: String,
}

fn invalid(path: &str, message: impl Into<String>) -> Error {
    Error::invalid(
        "invalid-lock",
        path,
        message,
        "Run `apko lock` on the configuration and commit the lock it writes.",
    )
}

/// Where an address is kept in apko's cache, and what its fetch is named.
struct Place {
    /// The address without its scheme: a host and a path.
    plain: String,
    /// The cache directory of the address's repository and the directory
    /// the file is in: for `https://host/os/aarch64/x.apk`, the escaped
    /// `https://host/os`, then `aarch64`.
    within: String,
    /// The file's name.
    file: String,
}

/// Reads an address as apko's cache does (`cachePathFromURL`): the last
/// two steps of its path are a directory and a file, and what is before
/// them, with the scheme and the host, is escaped into one directory's
/// name. Only an address that is written one way is taken: https, a
/// lower-case host with no port, and steps of letters, digits and
/// `._+-@` (the keys apko finds for Alpine are named with an `@`), so
/// that apko and this agree on where it is kept whatever either would
/// otherwise normalize.
fn place(address: &str) -> Option<Place> {
    let plain = address.strip_prefix("https://")?;
    let (host, path) = plain.split_once('/')?;
    let named = |step: &str, more: &str| {
        !step.is_empty()
            && step != "."
            && step != ".."
            && step
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || more.as_bytes().contains(&b))
    };
    if !named(host, ".-") || host.bytes().any(|b| b.is_ascii_uppercase()) {
        return None;
    }
    let steps: Vec<&str> = path.split('/').collect();
    if steps.len() < 2 || !steps.iter().all(|step| named(step, "._+-@")) {
        return None;
    }
    let (file, directory) = (steps[steps.len() - 1], steps[steps.len() - 2]);
    let mut repository = format!("https://{host}/");
    repository.push_str(&steps[..steps.len() - 2].join("/"));
    Some(Place {
        plain: plain.into(),
        within: format!("{}/{directory}", escaped(&repository)),
        file: file.into(),
    })
}

/// Escapes text as Go's `url.QueryEscape` does, which is how apko names a
/// repository's directory in its cache.
fn escaped(text: &str) -> String {
    let mut out = String::with_capacity(text.len() * 3);
    for b in text.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            b' ' => out.push('+'),
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

/// Reads a checksum written as the lock writes one, `sha1-` or `sha256-`
/// and its base64, into hex.
fn hex(checksum: &str, kind: &str, bytes: usize) -> Option<String> {
    let raw = frontseat_plugin::checksum::decode(checksum, kind, bytes)?;
    Some(frontseat_plugin::checksum::hex(&raw))
}

/// Reads a section: its range, written `bytes=first-last`, and its
/// checksum.
fn section(entry: &SectionEntry, kind: &str, bytes: usize) -> Option<Section> {
    let (first, last) = entry.range.strip_prefix("bytes=")?.split_once('-')?;
    let plain = |number: &str| {
        (!number.is_empty() && number.bytes().all(|b| b.is_ascii_digit()))
            .then(|| number.parse::<u64>().ok())
            .flatten()
    };
    let (first, last) = (plain(first)?, plain(last)?);
    let length = last.checked_sub(first)?.checked_add(1)?;
    Some(Section {
        offset: first,
        length,
        hex: hex(&entry.checksum, kind, bytes)?,
    })
}

/// Reads the lock of an image in a directory; none where there is no lock.
pub(super) fn read(repo: &Repository, root: &str) -> Result<Option<Lock>, Error> {
    let path = join(root, FILE);
    let Some(document) = repo
        .json::<Document>(&path)
        .map(|read| read.map(|document| document.value))?
    else {
        return Ok(None);
    };
    if document.version != "v1" {
        return Err(invalid(
            &path,
            format!(
                "the lock is of version {:?}, and only v1 is read",
                document.version
            ),
        ));
    }
    // apko holds the configuration to the checksum the lock has of it.
    // A lock that names none is not held to anything.
    let Some(config) = document.config.map(|config| config.name) else {
        return Err(invalid(
            &path,
            "the lock does not say which configuration it was written of",
        ));
    };
    // apko writes the configuration as it was named to `apko lock`, with
    // whatever directories led to it from where apko ran. The image is
    // found by its lock, so the configuration is the file of that name
    // beside the lock; apko holds it to the lock's checksum of it. Its
    // name is a name: it is handed to apko as an argument and to the
    // build as an input, and is read as neither an option nor a pattern.
    let config = config.rsplit('/').next().unwrap_or_default().to_string();
    if config.is_empty()
        || config == "."
        || config == ".."
        || config.starts_with('-')
        || !config
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"._+-".contains(&b))
    {
        return Err(invalid(
            &path,
            format!("the lock was written of a configuration named {config:?}, which is not a file's name in letters, digits and \"._+-\""),
        ));
    }
    let mut keys = Vec::new();
    for entry in document.contents.keyring {
        // apko reads a key from a directory named for the key's file
        // only where the file is named as a key is.
        let place = place(&entry.url)
            .filter(|place| place.file.ends_with(".rsa.pub"))
            .ok_or_else(|| {
                invalid(
                    &path,
                    format!(
                        "the key {:?} is not a plain https address of a .rsa.pub file",
                        entry.url
                    ),
                )
            })?;
        let Some(content) = entry.content.filter(|content| !content.is_empty()) else {
            return Err(invalid(
                &path,
                format!("the lock does not hold the key {}", entry.url),
            ));
        };
        let key = Key {
            fetch: format!("fetch/{}", place.plain),
            directory: format!("{CACHE}/{}/{}/", place.within, place.file),
            content,
        };
        match keys.iter().find(|held: &&Key| held.fetch == key.fetch) {
            Some(held) if held.content != key.content => {
                return Err(invalid(
                    &path,
                    format!("the lock holds the key {} twice, differently", entry.url),
                ))
            }
            Some(_) => {}
            None => keys.push(key),
        }
    }
    let mut packages: Vec<Package> = Vec::new();
    for entry in document.contents.packages {
        let wrong = |what: &str| invalid(&path, format!("the package {}: {what}", entry.url));
        let place = place(&entry.url)
            .filter(|place| place.file.len() > ".apk".len() && place.file.ends_with(".apk"))
            .ok_or_else(|| wrong("it is not a plain https address of an .apk file"))?;
        if !ARCHITECTURES.contains(&entry.architecture.as_str()) {
            return Err(wrong("its architecture is not one apko writes in a lock"));
        }
        let control = section(&entry.control, "sha1", 20)
            .ok_or_else(|| wrong("its control section has no range and sha1"))?;
        let data = section(&entry.data, "sha256", 32)
            .ok_or_else(|| wrong("its data section has no range and sha256"))?;
        // apko finds a package in its cache by this checksum, which is
        // the control section's again as a package index spells it.
        let indexed = entry
            .checksum
            .strip_prefix("Q1")
            .and_then(|base64| hex(&format!("sha1-{base64}"), "sha1", 20));
        if indexed.as_ref() != Some(&control.hex) {
            return Err(wrong("its checksum is not the sha1 of its control section"));
        }
        // apko keeps a package in a directory named as its file is,
        // without the ".apk" it ends in, taken off once.
        let name = place.file.strip_suffix(".apk").unwrap_or(&place.file);
        let package = Package {
            fetch: format!("fetch/{}", place.plain),
            url: entry.url.clone(),
            architecture: entry.architecture.clone(),
            directory: format!("{CACHE}/{}/{name}/", place.within),
            control,
            data,
        };
        match packages.iter().find(|held| held.fetch == package.fetch) {
            Some(held) if *held != package => {
                return Err(wrong("the lock holds it twice, differently"))
            }
            Some(_) => {}
            None => packages.push(package),
        }
    }
    if packages.is_empty() {
        return Err(invalid(&path, "the lock holds no package"));
    }
    Ok(Some(Lock {
        config,
        keys,
        packages,
    }))
}

#[cfg(test)]
mod tests {
    use super::*;

    // Where apko looks for each: its cachePathFromURL, cacheDirForPackage
    // and cacheDirFromFile (apko 26953696, pkg/apk/apk/cache.go), and what
    // apko built from that commit says it cannot find when a file is
    // elsewhere. The acceptance spec builds with these paths and the
    // pinned apko.
    #[test]
    fn an_address_is_kept_where_apko_looks_for_it() {
        let package =
            place("https://packages.wolfi.dev/os/aarch64/libstdc++-15.2.0-r6.apk").unwrap();
        assert_eq!(
            package.within,
            "https%3A%2F%2Fpackages.wolfi.dev%2Fos/aarch64"
        );
        assert_eq!(package.file, "libstdc++-15.2.0-r6.apk");
        assert_eq!(
            package.plain,
            "packages.wolfi.dev/os/aarch64/libstdc++-15.2.0-r6.apk"
        );
        let key = place("https://packages.wolfi.dev/os/wolfi-signing.rsa.pub").unwrap();
        assert_eq!(key.within, "https%3A%2F%2Fpackages.wolfi.dev%2F/os");
        let deep = place("https://dl.example.org/a/b/v3.22/main/x86_64/musl-1.2.5-r9.apk").unwrap();
        assert_eq!(
            deep.within,
            "https%3A%2F%2Fdl.example.org%2Fa%2Fb%2Fv3.22%2Fmain/x86_64"
        );
    }

    #[test]
    fn an_address_written_another_way_is_not_taken() {
        for address in [
            "http://packages.wolfi.dev/os/aarch64/x.apk",
            "https://packages.wolfi.dev/x.apk",
            "https://packages.wolfi.dev",
            "https://Packages.wolfi.dev/os/aarch64/x.apk",
            "https://packages.wolfi.dev:8443/os/aarch64/x.apk",
            "https://user@packages.wolfi.dev/os/aarch64/x.apk",
            "https://packages.wolfi.dev/os/../aarch64/x.apk",
            "https://packages.wolfi.dev/os/./aarch64/x.apk",
            "https://packages.wolfi.dev/os//aarch64/x.apk",
            "https://packages.wolfi.dev/os/aarch64/x.apk?a=b",
            "https://packages.wolfi.dev/os/aarch64/x.apk#a",
            "https://packages.wolfi.dev/os/aarch64/x%2e.apk",
            "https://packages.wolfi.dev/os/aarch64/",
            "https:///os/aarch64/x.apk",
            "file:///os/aarch64/x.apk",
            "/os/aarch64/x.apk",
        ] {
            assert!(place(address).is_none(), "{address}");
        }
    }

    #[test]
    fn a_section_is_read_from_its_range_and_checksum() {
        let entry = |range: &str, checksum: &str| SectionEntry {
            range: range.into(),
            checksum: checksum.into(),
        };
        let control = section(
            &entry("bytes=647-1628", "sha1-m6wB0UFePGN8Drz82pC8y2uAiu8="),
            "sha1",
            20,
        )
        .unwrap();
        assert_eq!((control.offset, control.length), (647, 982));
        assert_eq!(control.hex, "9bac01d1415e3c637c0ebcfcda90bccb6b808aef");
        for (range, checksum) in [
            ("", ""),
            ("bytes=5-4", "sha1-m6wB0UFePGN8Drz82pC8y2uAiu8="),
            ("bytes=-4", "sha1-m6wB0UFePGN8Drz82pC8y2uAiu8="),
            ("bytes=+1-4", "sha1-m6wB0UFePGN8Drz82pC8y2uAiu8="),
            (
                "bytes=0-18446744073709551615",
                "sha1-m6wB0UFePGN8Drz82pC8y2uAiu8=",
            ),
            ("0-4", "sha1-m6wB0UFePGN8Drz82pC8y2uAiu8="),
            ("bytes=0-4", "sha256-m6wB0UFePGN8Drz82pC8y2uAiu8="),
            ("bytes=0-4", "m6wB0UFePGN8Drz82pC8y2uAiu8="),
            ("bytes=0-4", "sha1-m6wB0UFePGN8Drz82pC8"),
            ("bytes=0-4", "sha1-not base64"),
        ] {
            assert!(
                section(&entry(range, checksum), "sha1", 20).is_none(),
                "{range} {checksum}"
            );
        }
    }
}
