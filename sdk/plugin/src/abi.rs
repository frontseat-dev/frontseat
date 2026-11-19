//! The guest half of the ABI (`api/plugin-proto/ABI.md`), and the dispatch
//! of a call to a plugin.
//!
//! The dispatch is the same wherever it is compiled, so a plugin is
//! tested on the machine that builds it, against a host the test
//! supplies. The guest half is what a wasm module exports and imports,
//! and is there only in one.

use prost::Message;

use crate::wire;
use crate::{Error, Host, Plugin, Repository, ABI};

#[cfg(target_arch = "wasm32")]
pub use guest::{alloc, call, free};

fn internal(e: impl std::fmt::Display) -> Error {
    Error {
        code: crate::Code::InvalidArgument,
        reason: "invalid-request".into(),
        message: e.to_string(),
        ..Error::default()
    }
}

/// Decode a host reply without turning corruption into an absent file or empty
/// glob. Zero bytes are valid protobuf defaults; malformed bytes are not.
#[cfg(any(target_arch = "wasm32", test))]
pub(crate) fn decode_host_reply<Resp: Message + Default>(bytes: &[u8]) -> Result<Resp, Error> {
    Resp::decode(bytes).map_err(|error| Error {
        code: crate::Code::Internal,
        reason: "invalid-host-response".into(),
        message: error.to_string(),
        help: "Use a host and SDK that implement the same plugin contract.".into(),
        ..Default::default()
    })
}

/// Answers one call of the contract's PluginService: the serialized
/// response, an error, or `None` for a method the plugin does not serve.
pub fn dispatch(
    plugin: &dyn Plugin,
    host: &dyn Host,
    call: &wire::Call,
) -> Option<Result<Vec<u8>, Error>> {
    let request = call.request.as_slice();
    match call.method.as_str() {
        "Describe" => Some(describe(plugin, request)),
        "Analyze" => Some(analyze(plugin, host, request)),
        "Arrange" => Some(arrange(plugin, host, request)),
        "Report" => Some(report(plugin, host, request)),
        "Reconcile" => Some(reconcile(plugin, host, request)),
        "Branch" => Some(branch(plugin, request)),
        "Version" => Some(version(plugin, request)),
        "Service" => Some(service(plugin, request)),
        _ => None,
    }
}

fn describe(plugin: &dyn Plugin, request: &[u8]) -> Result<Vec<u8>, Error> {
    let req = wire::DescribeRequest::decode(request).map_err(internal)?;
    if req.host_abi_max != 0 && (ABI < req.host_abi_min || ABI > req.host_abi_max) {
        return Err(Error {
            reason: "abi-mismatch".into(),
            message: format!(
                "the plugin speaks ABI {ABI} and the host {} to {}",
                req.host_abi_min, req.host_abi_max
            ),
            help: "Build the plugin against an SDK the host supports.".into(),
            ..Error::default()
        });
    }
    let d = plugin.describe();
    let options = d
        .options
        .iter()
        .map(|o| wire::Option {
            name: o.name.into(),
            kind: o.kind() as i32,
            default: Some(o.default.clone().into()),
            entity_settable: o.entity_settable,
        })
        .collect();
    Ok(wire::DescribeResponse {
        name: d.name,
        abi: ABI,
        interests: d.interests,
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

fn analyze(plugin: &dyn Plugin, host: &dyn Host, request: &[u8]) -> Result<Vec<u8>, Error> {
    let req = wire::AnalyzeRequest::decode(request).map_err(internal)?;
    let repo = Repository::new(host, req.context);
    repo.validate_context(&plugin.describe().options)?;
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

fn arrange(plugin: &dyn Plugin, host: &dyn Host, request: &[u8]) -> Result<Vec<u8>, Error> {
    let req = wire::ArrangeRequest::decode(request).map_err(internal)?;
    let repo = Repository::new(host, req.context);
    repo.validate_context(&plugin.describe().options)?;
    let files = plugin.arrange(&repo, &req.root, &req.actions, &req.handed)?;
    Ok(wire::ArrangeResponse { files }.encode_to_vec())
}

fn report(plugin: &dyn Plugin, host: &dyn Host, request: &[u8]) -> Result<Vec<u8>, Error> {
    let req = wire::ReportRequest::decode(request).map_err(internal)?;
    let repo = Repository::new(host, req.context);
    repo.validate_context(&plugin.describe().options)?;
    let printed = crate::Printed {
        exit_code: req.exit_code,
        stdout: req.stdout,
        stderr: req.stderr,
    };
    let diagnostics = plugin.report(&repo, &req.root, &req.action, &printed)?;
    Ok(wire::ReportResponse { diagnostics }.encode_to_vec())
}

fn branch(plugin: &dyn Plugin, request: &[u8]) -> Result<Vec<u8>, Error> {
    let req = wire::BranchRequest::decode(request).map_err(internal)?;
    Ok(plugin.branch(&req)?.encode_to_vec())
}

fn version(plugin: &dyn Plugin, request: &[u8]) -> Result<Vec<u8>, Error> {
    let req = wire::VersionRequest::decode(request).map_err(internal)?;
    Ok(plugin.version(&req)?.encode_to_vec())
}

fn reconcile(plugin: &dyn Plugin, host: &dyn Host, request: &[u8]) -> Result<Vec<u8>, Error> {
    let req = wire::ReconcileRequest::decode(request).map_err(internal)?;
    let repo = Repository::new(host, req.context);
    repo.validate_context(&plugin.describe().options)?;
    let edits = plugin.reconcile(&repo, &req.root, &req.entities)?;
    Ok(wire::ReconcileResponse { edits }.encode_to_vec())
}

/// Service is asked only for a name the plugin claimed, and is told
/// nothing of a repository. A plugin that claims none starts none.
fn service(plugin: &dyn Plugin, request: &[u8]) -> Result<Vec<u8>, Error> {
    let req = wire::ServiceRequest::decode(request).map_err(internal)?;
    let services = plugin.describe().services;
    if services.is_empty() {
        return Err(crate::contract::no_service(&req.name));
    }
    if !services.contains(&req.name) {
        return Err(Error {
            code: crate::Code::InvalidArgument,
            reason: "unclaimed-service".into(),
            message: format!("this plugin starts no service {}", req.name),
            help: "Ask a plugin only for the names its description claims.".into(),
            ..Error::default()
        });
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
    let result = match wire::Call::decode(call) {
        Ok(call) => dispatch(plugin, host, &call),
        Err(e) => Some(Err(internal(e))),
    };
    let failure = |e: Error| wire::reply::Result::Error(e.into_status(&plugin.describe().name));
    wire::Reply {
        result: Some(match result {
            Some(Ok(response)) => wire::reply::Result::Response(response),
            Some(Err(e)) => failure(e),
            None => failure(Error {
                code: crate::Code::Unimplemented,
                reason: "unknown-method".into(),
                message: "The plugin does not serve this method.".into(),
                ..Default::default()
            }),
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
        // SAFETY: the host wrote `size` bytes at `ptr`, into memory alloc gave it.
        let request = unsafe { std::slice::from_raw_parts(ptr as *const u8, size as usize) };
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
