use frontseat_plugin::prelude::*;
use std::collections::BTreeMap;

use crate::lock::{Key, Lock, Package, FILE};

/// The tool every image is built with: the apko the repository pins.
const APKO: &str = "apko";

/// The fetch of one package of one architecture: shared by every image
/// of the repository that locks it. The package is downloaded whole,
/// there being no checksum of the whole, and its control and data
/// sections are cut out, each checked against the lock and kept under
/// the name apko's cache reads it by: the checksum in hex. The
/// signature section is left; apko reads none when it installs from a
/// lock. It runs no tool.
fn fetch(package: &Package) -> Action {
    Action::shared_fetch(&package.fetch, [&package.directory]).download(Download {
        url: package.url.clone(),
        parts: vec![
            Part {
                offset: package.control.offset,
                length: package.control.length,
                sha1: package.control.hex.clone(),
                path: format!("{}{}.ctl.tar.gz", package.directory, package.control.hex),
                ..Default::default()
            },
            Part {
                offset: package.data.offset,
                length: package.data.length,
                sha256: package.data.hex.clone(),
                path: format!("{}{}.dat.tar.gz", package.directory, package.data.hex),
                ..Default::default()
            },
        ],
        ..Default::default()
    })
}

/// The fetch of one signing key: the lock holds the key whole, so
/// nothing is downloaded, and the key is written where apko's cache
/// reads it from when it starts offline. apko takes the newest file of
/// the directory, whatever it is named.
fn key(key: &Key) -> Action {
    Action::shared_fetch(&key.fetch, [&key.directory]).file(File {
        path: format!("{}locked.etag", key.directory),
        content: key.content.clone().into_bytes(),
        executable: false,
    })
}

/// The build of the image for one architecture, as an OCI layout: from
/// the lock alone, offline, out of what the fetches left in apko's cache.
/// apko checks the configuration against the lock, each control section
/// against the lock and each data section against its control section
/// again. It writes a layout only into a directory that is there, so the
/// layout's own files are what is declared, and the worker makes the
/// directory they are in. Nothing of the checkout's history goes into
/// the image, and no bill of materials is written beside it.
fn image(config: &str, architecture: &str, inputs: Vec<Input>) -> Action {
    let layout = format!("oci/{architecture}");
    let command = Command::new([
        APKO,
        "build",
        "--offline",
        "--lockfile",
        FILE,
        "--vcs=false",
        "--sbom=false",
        "--arch",
        architecture,
        // What follows is named by the lock, which a repository
        // commits: apko reads none of it as an option.
        "--",
        config,
        "image:latest",
        &layout,
    ]);
    ["index.json", "oci-layout", "blobs/"].into_iter().fold(
        Action::new(format!("image-{}", architecture.replace('_', "-")), command)
            .tool(APKO)
            .inputs(inputs),
        |action, file| action.output(Output::required(format!("{layout}/{file}"))),
    )
}

/// The actions of an image: a fetch of each key and of each package the
/// lock holds, and a build for each architecture it holds packages of,
/// which reads the configuration, the lock, the keys and that
/// architecture's packages, and waits for nothing else.
pub(super) fn of(lock: &Lock) -> Vec<Action> {
    let mut actions: Vec<Action> = lock.keys.iter().map(key).collect();
    actions.extend(lock.packages.iter().map(fetch));
    let mut architectures: BTreeMap<&str, Vec<Input>> = BTreeMap::new();
    for package in &lock.packages {
        architectures
            .entry(&package.architecture)
            .or_default()
            .push(Input::output(&package.fetch));
    }
    for (architecture, packages) in architectures {
        let inputs = [Input::source(&lock.config), Input::source(FILE)]
            .into_iter()
            .chain(lock.keys.iter().map(|key| Input::output(&key.fetch)))
            .chain(packages)
            .collect();
        actions.push(image(&lock.config, architecture, inputs));
    }
    actions
}
