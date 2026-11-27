//! A chart as Helm reads one: its name from Chart.yaml, its
//! dependencies from Chart.yaml or, where it is there, requirements.yaml,
//! and its lock from Chart.lock or, where it is there, requirements.lock.
use frontseat_plugin::prelude::*;
use frontseat_plugin::repository::{beneath, join, literal, resolve};
use serde::Deserialize;
use serde_saphyr::Spanned;

/// The ecosystem a chart is provided and required in, by its directory:
/// a file:// dependency names a directory, and two charts of one name, in
/// two directories, are two charts.
pub(crate) const ECOSYSTEM: &str = "helm";

/// What a chart of a directory is provided and required by: the
/// directory, relative to the repository, or "." for its root.
pub(crate) fn coordinate_of(directory: &str) -> Coordinate {
    Coordinate {
        ecosystem: ECOSYSTEM.into(),
        name: if directory.is_empty() {
            ".".into()
        } else {
            directory.into()
        },
    }
}

/// What a Chart.yaml says that the plugin reads; the rest is Helm's.
#[derive(Deserialize)]
struct Metadata {
    #[serde(default)]
    name: Option<String>,
    #[serde(default)]
    dependencies: Option<Vec<Spanned<Dependency>>>,
}

/// A requirements.yaml, which Helm reads over Chart.yaml's dependencies
/// where it says some, whatever the chart's apiVersion.
#[derive(Deserialize)]
struct Requirements {
    /// Absent, Chart.yaml's dependencies hold; null, there are none.
    #[serde(default, deserialize_with = "said")]
    dependencies: Option<Option<Vec<Spanned<Dependency>>>>,
}

/// A key said, null or not: what Helm's loader takes over what it read
/// before.
fn said<'de, D, T>(deserializer: D) -> Result<Option<T>, D::Error>
where
    D: serde::Deserializer<'de>,
    T: Deserialize<'de>,
{
    T::deserialize(deserializer).map(Some)
}

#[derive(Deserialize)]
struct Dependency {
    #[serde(default)]
    name: Option<String>,
    #[serde(default)]
    repository: Option<String>,
    #[serde(default)]
    alias: Option<String>,
}

/// A Chart.lock or a requirements.lock: what `helm dependency update`
/// resolved each dependency to.
#[derive(Deserialize)]
struct Lock {
    #[serde(default)]
    dependencies: Option<Vec<Locked>>,
}

#[derive(Deserialize)]
struct Locked {
    #[serde(default)]
    name: Option<String>,
    #[serde(default)]
    version: Option<String>,
}

/// The first document of a YAML file Helm reads, as Helm unmarshals one:
/// none where the file is not there, and an empty one where it holds no
/// document, or only comments.
fn first<T: serde::de::DeserializeOwned>(
    repo: &Repository,
    path: &str,
) -> Result<Option<Option<T>>, Error> {
    Ok(repo
        .yaml_stream::<T>(path)?
        .map(|read| read.value.into_iter().next()))
}

/// A dependency of a chart on another chart of the repository, named by
/// `file://` and outside the chart's own directory.
pub(crate) struct Local {
    /// The chart's name, which the dependency names it by.
    pub name: String,
    /// The name Helm gives it in the depending chart, where the dependency
    /// says one: what Helm names its templates by.
    pub alias: Option<String>,
    /// Its directory, relative to the repository.
    pub directory: String,
    /// Where the chart says it depends on it.
    pub origin: Origin,
    /// The version the lock resolved it to, or none where there is no lock
    /// or it names none.
    pub version: String,
}

pub(crate) struct Chart {
    pub name: String,
    /// Its directory, relative to the repository.
    pub directory: String,
    pub manifest: String,
    pub local: Vec<Local>,
}

/// Where a dependency's chart is, as its repository says.
enum Place<'a> {
    /// In the chart's own `charts/` directory, placed there by hand: no
    /// repository.
    Vendored,
    /// A directory, relative to the chart (`file://../common`).
    File(&'a str),
    /// Anything else: a chart repository over HTTP, an OCI registry, or
    /// a repository's alias (`@stable`, `alias:stable`).
    Remote,
}

fn place(repository: Option<&str>) -> Place<'_> {
    let repository = repository.unwrap_or_default();
    if repository.is_empty() {
        Place::Vendored
    } else if let Some(path) = repository.strip_prefix("file://") {
        Place::File(path)
    } else {
        Place::Remote
    }
}

fn invalid(path: &str, line: i32, message: impl Into<String>, help: &str) -> Error {
    Error::invalid("invalid-manifest", path, message, help).at_line(path, line)
}

/// Whether a chart's `charts/` holds a dependency already, as a
/// directory or as the archive Helm names `<name>-<version>.tgz`: Helm
/// reads it there and fetches nothing. Helm matches it by the name inside
/// it; a file named otherwise is not looked into.
fn vendored(repo: &Repository, root: &str, name: &str) -> Result<bool, Error> {
    let name = literal(name);
    let found = repo.glob(
        root,
        &[
            &format!("charts/{name}/Chart.yaml"),
            &format!("charts/{name}-[0-9]*.tgz"),
        ],
    )?;
    Ok(!found.is_empty())
}

/// The dependencies a chart's directory says, each with its line, and the
/// file they were read from: requirements.yaml where it says some, as
/// Helm's loader reads it over Chart.yaml, and Chart.yaml otherwise.
fn dependencies(
    repo: &Repository,
    root: &str,
    metadata: Metadata,
    manifest: &str,
) -> Result<(String, Vec<Spanned<Dependency>>), Error> {
    let requirements = join(root, "requirements.yaml");
    if let Some(Some(read)) = first::<Requirements>(repo, &requirements)? {
        if let Some(listed) = read.dependencies {
            return Ok((requirements, listed.unwrap_or_default()));
        }
    }
    Ok((manifest.into(), metadata.dependencies.unwrap_or_default()))
}

/// The lock Helm reads: requirements.lock where it is there, which Helm's
/// loader reads after Chart.lock and over it, and Chart.lock otherwise.
fn lock(repo: &Repository, root: &str) -> Result<Vec<Locked>, Error> {
    for file in ["requirements.lock", "Chart.lock"] {
        if let Some(read) = first::<Lock>(repo, &join(root, file))? {
            return Ok(read.and_then(|lock| lock.dependencies).unwrap_or_default());
        }
    }
    Ok(Vec::new())
}

/// The chart whose Chart.yaml is in the directory, or none where there is
/// none.
pub(crate) fn read(repo: &Repository, root: &str) -> Result<Option<Chart>, Error> {
    let manifest = join(root, "Chart.yaml");
    let Some(metadata) = repo.yaml::<Metadata>(&manifest)?.map(|read| read.value) else {
        return Ok(None);
    };
    let Some(name) = metadata.name.clone().filter(|name| !name.is_empty()) else {
        return Err(invalid(
            &manifest,
            0,
            "the chart has no name",
            "Name the chart in Chart.yaml, as Helm requires.",
        ));
    };
    let (file, listed) = dependencies(repo, root, metadata, &manifest)?;
    let locked = lock(repo, root)?;
    let mut local = Vec::new();
    for dependency in listed {
        let line = i32::try_from(dependency.referenced.line()).unwrap_or(0);
        let dependency = dependency.value;
        let Some(named) = dependency.name.filter(|name| !name.is_empty()) else {
            return Err(invalid(
                &file,
                line,
                "a dependency has no name",
                "Name each dependency by the name of its chart.",
            ));
        };
        let repository = dependency.repository.unwrap_or_default();
        let path = match place(Some(&repository)) {
            Place::Vendored => continue,
            Place::Remote if vendored(repo, root, &named)? => continue,
            Place::Remote => {
                return Err(Error::not_served(
                    "remote-dependencies",
                    format!(
                        "the dependency {named} is fetched from {repository}, and a chart is not fetched yet"
                    ),
                )
                .at_line(&file, line)
                .help(
                    "Depend on a chart of this repository by file://, or place the chart in charts/ by hand.",
                ))
            }
            Place::File(path) => path,
        };
        let Some(directory) = resolve(root, path) else {
            return Err(invalid(
                &file,
                line,
                format!("the dependency {named} is outside the repository"),
                "Name a chart of this repository, relative to this chart.",
            ));
        };
        // A chart in this one's own charts/ is in its files already, where
        // Helm looks; anywhere else within it, Helm does not look.
        if directory == root || beneath(root, &directory).is_some() {
            if beneath(root, &directory).is_some_and(|within| within.starts_with("charts/")) {
                continue;
            }
            return Err(invalid(
                &file,
                line,
                format!("the dependency {named} is within this chart and not in its charts/, where Helm looks for it"),
                "Name a chart of charts/, or a chart outside this one.",
            ));
        }
        depended(repo, &directory, &named, &file, line)?;
        local.push(Local {
            version: locked
                .iter()
                .find(|entry| entry.name.as_deref() == Some(&named))
                .and_then(|entry| entry.version.clone())
                .unwrap_or_default(),
            alias: dependency.alias.filter(|alias| !alias.is_empty()),
            name: named,
            directory,
            origin: Origin {
                path: file.clone(),
                line,
            },
        });
    }
    Ok(Some(Chart {
        name,
        directory: root.into(),
        manifest,
        local,
    }))
}

/// Holds a chart depended on by file:// to what it is handed as: the chart
/// of the directory named, of the name the dependency says, depending on
/// nothing outside itself. What it depends on would be handed beside it,
/// where Helm does not look: in the depending chart's `charts/`, and not
/// in its own.
fn depended(
    repo: &Repository,
    directory: &str,
    name: &str,
    file: &str,
    line: i32,
) -> Result<(), Error> {
    let manifest = join(directory, "Chart.yaml");
    let Some(metadata) = repo.yaml::<Metadata>(&manifest)?.map(|read| read.value) else {
        return Err(invalid(
            file,
            line,
            format!("the dependency {name} names {directory}, which holds no chart"),
            "Name the directory of a chart, one that holds a Chart.yaml.",
        ));
    };
    if metadata.name.as_deref() != Some(name) {
        return Err(invalid(
            file,
            line,
            format!(
                "the dependency {name} names {directory}, whose chart is {}",
                metadata.name.as_deref().unwrap_or_default()
            ),
            "Name a dependency by the name its chart's Chart.yaml gives it.",
        ));
    }
    let (_, listed) = dependencies(repo, directory, metadata, &manifest)?;
    let mut beyond = false;
    for dependency in &listed {
        let dependency = &dependency.value;
        beyond |= match place(dependency.repository.as_deref()) {
            Place::Vendored => false,
            Place::Remote => !vendored(
                repo,
                directory,
                dependency.name.as_deref().unwrap_or_default(),
            )?,
            Place::File(path) => resolve(directory, path)
                .is_none_or(|at| at != directory && beneath(directory, &at).is_none()),
        };
    }
    if beyond {
        return Err(Error::not_served(
            "transitive-dependencies",
            format!("the dependency {name} depends on a chart outside it, and what a dependency depends on is not handed yet"),
        )
        .at_line(file, line)
        .help("Depend on a chart that depends on nothing outside its own directory."));
    }
    Ok(())
}
