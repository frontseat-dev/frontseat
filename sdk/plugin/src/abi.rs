//! The guest half of the ABI (`api/plugin-proto/ABI.md`), and the dispatch
//! of a call to a plugin.
//!
//! The dispatch is the same wherever it is compiled, so a plugin is
//! tested on the machine that builds it, against a host the test
//! supplies. The guest half is what a wasm module exports and imports,
//! and is there only in one.

use prost::Message;

use crate::repository::NOT_FIXTURES;
use crate::wire;
use crate::{Description, Error, Host, Plugin, Repository};

#[cfg(target_arch = "wasm32")]
pub use guest::{alloc, call, free};

/// A call the plugin cannot read: the host sent what is no request of
/// the contract.
fn invalid_request(e: impl std::fmt::Display) -> Error {
    Error::new(
        crate::Code::InvalidArgument,
        "invalid-request",
        e.to_string(),
    )
}

/// Decode a host reply without turning corruption into an absent file or empty
/// glob. Zero bytes are valid protobuf defaults; malformed bytes are not.
#[cfg(any(target_arch = "wasm32", test))]
pub(crate) fn decode_host_reply<Resp: Message + Default>(bytes: &[u8]) -> Result<Resp, Error> {
    Resp::decode(bytes).map_err(|error| {
        Error::new(
            crate::Code::Internal,
            "invalid-host-response",
            error.to_string(),
        )
        .help("Use a host and SDK that implement the same plugin contract.")
    })
}

/// Answers one call of the contract's PluginService: the serialized
/// response, an error, or `None` for a method the plugin does not serve.
pub fn dispatch(
    plugin: &dyn Plugin,
    host: &dyn Host,
    call: &wire::Call,
) -> Option<Result<Vec<u8>, Error>> {
    dispatched(plugin, plugin.describe(), host, call)
}

/// A call answered by a plugin that has said what it is: it is asked
/// once in a call, and what it said is what the call is held to.
fn dispatched(
    plugin: &dyn Plugin,
    described: Description,
    host: &dyn Host,
    call: &wire::Call,
) -> Option<Result<Vec<u8>, Error>> {
    let request = call.request.as_slice();
    match call.method.as_str() {
        "Describe" => Some(describe(described, request)),
        "Analyze" => Some(analyze(plugin, &described, host, request)),
        "Arrange" => Some(arrange(plugin, &described, host, request)),
        "Report" => Some(report(plugin, &described, host, request)),
        "Reconcile" => Some(reconcile(plugin, &described, host, request)),
        "Branch" => Some(branch(plugin, &described, request)),
        "Version" => Some(version(plugin, &described, request)),
        "Service" => Some(service(plugin, &described, request)),
        _ => None,
    }
}

fn describe(d: Description, request: &[u8]) -> Result<Vec<u8>, Error> {
    // The ABI is said once, by the module's export: the request says
    // nothing, and is read only to refuse one that is not a request.
    wire::DescribeRequest::decode(request).map_err(invalid_request)?;
    let options = d
        .options
        .into_iter()
        .map(|o| wire::Option {
            name: o.name.into(),
            description: o.description.into(),
            kind: o.kind() as i32,
            default: Some(o.default.into()),
            entity_settable: o.entity_settable,
            minimum: o.minimum,
            maximum: o.maximum,
            allowed: o.allowed.iter().map(|word| word.to_string()).collect(),
        })
        .collect();
    let mut interests = d.interests;
    if !interests.is_empty() && !interests.iter().any(|i| i == NOT_FIXTURES) {
        interests.push(NOT_FIXTURES.into());
    }
    Ok(wire::DescribeResponse {
        name: d.name,
        interests,
        options,
        generates_sources: d.generates_sources,
        release: (!d.branchings.is_empty() || !d.versionings.is_empty()).then_some(
            wire::ReleaseRoles {
                branchings: d.branchings,
                versionings: d.versionings,
            },
        ),
        services: d.services,
    }
    .encode_to_vec())
}

fn analyze(
    plugin: &dyn Plugin,
    described: &Description,
    host: &dyn Host,
    request: &[u8],
) -> Result<Vec<u8>, Error> {
    let req = wire::AnalyzeRequest::decode(request).map_err(invalid_request)?;
    let repo = Repository::new(host, req.context);
    repo.validate_context(&described.options)?;
    let roots = if req.roots.is_empty() {
        vec![String::new()]
    } else {
        req.roots
    };
    let mut analyses = Vec::new();
    for root in roots {
        let mut analysis = plugin.analyze(&repo, &root)?;
        analysis.root = root;
        analyses.push(analysis);
    }
    Ok(wire::AnalyzeResponse { analyses }.encode_to_vec())
}

fn arrange(
    plugin: &dyn Plugin,
    described: &Description,
    host: &dyn Host,
    request: &[u8],
) -> Result<Vec<u8>, Error> {
    let req = wire::ArrangeRequest::decode(request).map_err(invalid_request)?;
    let repo = Repository::new(host, req.context);
    repo.validate_context(&described.options)?;
    let files = plugin.arrange(&repo, &req.root, &req.actions, &req.handed)?;
    Ok(wire::ArrangeResponse { files }.encode_to_vec())
}

fn report(
    plugin: &dyn Plugin,
    described: &Description,
    host: &dyn Host,
    request: &[u8],
) -> Result<Vec<u8>, Error> {
    let req = wire::ReportRequest::decode(request).map_err(invalid_request)?;
    let repo = Repository::new(host, req.context);
    repo.validate_context(&described.options)?;
    let printed = crate::Printed {
        exit_code: req.exit_code,
        stdout: req.stdout,
        stderr: req.stderr,
    };
    let diagnostics = plugin.report(&repo, &req.root, &req.action, &printed)?;
    Ok(wire::ReportResponse { diagnostics }.encode_to_vec())
}

/// Branch and Version are asked only of a strategy the plugin claims, as
/// Service is of a service: a plugin that claims none of the kind serves
/// none, and one that claims others is not asked of this one.
fn claimed(claims: &[String], kind: &str, strategy: &str) -> Result<(), Error> {
    if claims.is_empty() {
        return Err(crate::contract::no_release_strategy(kind));
    }
    if !claims.iter().any(|claim| claim == strategy) {
        return Err(Error::new(
            crate::Code::InvalidArgument,
            "unclaimed-strategy",
            format!("this plugin serves no {kind} strategy {strategy:?}"),
        )
        .help("Name a strategy the plugin says it serves."));
    }
    Ok(())
}

fn branch(plugin: &dyn Plugin, described: &Description, request: &[u8]) -> Result<Vec<u8>, Error> {
    let req = wire::BranchRequest::decode(request).map_err(invalid_request)?;
    claimed(&described.branchings, "branching", &req.strategy)?;
    Ok(plugin.branch(&req)?.encode_to_vec())
}

fn version(plugin: &dyn Plugin, described: &Description, request: &[u8]) -> Result<Vec<u8>, Error> {
    let req = wire::VersionRequest::decode(request).map_err(invalid_request)?;
    claimed(&described.versionings, "versioning", &req.strategy)?;
    Ok(plugin.version(&req)?.encode_to_vec())
}

fn reconcile(
    plugin: &dyn Plugin,
    described: &Description,
    host: &dyn Host,
    request: &[u8],
) -> Result<Vec<u8>, Error> {
    let req = wire::ReconcileRequest::decode(request).map_err(invalid_request)?;
    let repo = Repository::new(host, req.context);
    repo.validate_context(&described.options)?;
    let edits = plugin.reconcile(&repo, &req.root, &req.entities)?;
    Ok(wire::ReconcileResponse { edits }.encode_to_vec())
}

/// Service is asked only for a name the plugin claimed, and is told
/// nothing of a repository. A plugin that claims none starts none.
fn service(plugin: &dyn Plugin, described: &Description, request: &[u8]) -> Result<Vec<u8>, Error> {
    let req = wire::ServiceRequest::decode(request).map_err(invalid_request)?;
    let services = &described.services;
    if services.is_empty() {
        return Err(crate::contract::no_service(&req.name));
    }
    if !services.contains(&req.name) {
        return Err(Error::new(
            crate::Code::InvalidArgument,
            "unclaimed-service",
            format!("this plugin starts no service {}", req.name),
        )
        .help("Ask a plugin only for the names its description claims."));
    }
    let service = plugin.service(&req.name, &req.written.unwrap_or_default())?;
    Ok(wire::ServiceResponse {
        service: Some(service),
    }
    .encode_to_vec())
}

/// Answers a serialized `Call` with a serialized `Reply`, as a module
/// answers the host: the response, or the failure as a
/// `google.rpc.Status`, `UNIMPLEMENTED` for a method the plugin does not
/// serve.
pub fn answer(plugin: &dyn Plugin, host: &dyn Host, call: &[u8]) -> Vec<u8> {
    let described = plugin.describe();
    let name = described.name.clone();
    let result = match wire::Call::decode(call) {
        Ok(call) => dispatched(plugin, described, host, &call),
        Err(e) => Some(Err(invalid_request(e))),
    };
    let failure = |e: Error| wire::reply::Result::Error(e.into_status(&name));
    wire::Reply {
        result: Some(match result {
            Some(Ok(response)) => wire::reply::Result::Response(response),
            Some(Err(e)) => failure(e),
            None => failure(Error::new(
                crate::Code::Unimplemented,
                "unknown-method",
                "the plugin does not serve this method",
            )),
        }),
    }
    .encode_to_vec()
}

/// What a wasm module exports to the host and imports from it.
#[cfg(target_arch = "wasm32")]
mod guest {
    use prost::Message;

    use crate::wire;
    use crate::{Error, Host, Plugin};

    /// Returns the address of `size` bytes the host may write to.
    pub fn alloc(size: u32) -> u32 {
        if size == 0 {
            return 0;
        }
        let buffer = vec![0u8; size as usize].into_boxed_slice();
        Box::into_raw(buffer) as *mut u8 as u32
    }

    /// Gives back what `alloc` returned.
    pub fn free(ptr: u32, size: u32) {
        if ptr == 0 || size == 0 {
            return;
        }
        // SAFETY: the host frees only what alloc or a reply handed it, with the
        // size it was given.
        unsafe {
            drop(Box::from_raw(std::ptr::slice_from_raw_parts_mut(
                ptr as *mut u8,
                size as usize,
            )));
        }
    }

    fn pack(bytes: Vec<u8>) -> u64 {
        if bytes.is_empty() {
            return 0;
        }
        let size = bytes.len() as u64;
        let ptr = Box::into_raw(bytes.into_boxed_slice()) as *mut u8 as u64;
        ptr << 32 | size
    }

    /// Answers a serialized `Call` with a serialized `Reply`.
    pub fn call(plugin: &dyn Plugin, ptr: u32, size: u32) -> u64 {
        // A call of no bytes was written nowhere: alloc gives no address
        // for none, and a slice is never made of the null address.
        let request: &[u8] = if size == 0 {
            &[]
        } else {
            // SAFETY: the host wrote `size` bytes at `ptr`, into memory alloc gave it.
            unsafe { std::slice::from_raw_parts(ptr as *const u8, size as usize) }
        };
        pack(super::answer(plugin, &WasmHost, request))
    }

    #[link(wasm_import_module = "frontseat")]
    extern "C" {
        #[link_name = "read"]
        fn host_read(ptr: u32, size: u32) -> u64;
        #[link_name = "glob"]
        fn host_glob(ptr: u32, size: u32) -> u64;
    }

    /// The host, reached through the module's imports.
    struct WasmHost;

    /// Sends a request through an import and decodes what the host wrote back.
    /// A result of zero is an empty response.
    fn ask<Req: Message, Resp: Message + Default>(
        import: unsafe extern "C" fn(u32, u32) -> u64,
        request: &Req,
    ) -> Result<Resp, Error> {
        let bytes = request.encode_to_vec();
        // SAFETY: the import reads `bytes` during the call and keeps nothing.
        let packed = unsafe { import(bytes.as_ptr() as u32, bytes.len() as u32) };
        if packed == 0 {
            return super::decode_host_reply(&[]);
        }
        let (ptr, size) = ((packed >> 32) as u32, packed as u32);
        // SAFETY: the host wrote `size` bytes at `ptr`, into memory alloc gave it.
        let response = super::decode_host_reply(unsafe {
            std::slice::from_raw_parts(ptr as *const u8, size as usize)
        });
        free(ptr, size);
        response
    }

    impl Host for WasmHost {
        fn read(&self, path: &str) -> Result<Option<Vec<u8>>, Error> {
            let response: wire::ReadResponse =
                ask(host_read, &wire::ReadRequest { path: path.into() })?;
            Ok(response.found.then_some(response.content))
        }

        fn glob(&self, directory: &str, patterns: &[&str]) -> Result<Vec<String>, Error> {
            let request = wire::GlobRequest {
                patterns: patterns.iter().map(|p| p.to_string()).collect(),
                directory: directory.into(),
            };
            let response: wire::GlobResponse = ask(host_glob, &request)?;
            Ok(response.paths)
        }
    }
}
