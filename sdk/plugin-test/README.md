# frontseat-plugin-test

What a Frontseat plugin's unit tests are written with: a repository held
in memory, a host that reads and globs it as Frontseat does, and the
plugin asked as Frontseat asks it.

Add it as a dev-dependency beside the SDK:

```toml
[dev-dependencies]
frontseat-plugin-test = "0.0.0"
```

A test of the plugin of the SDK's example (`sdk/plugin/examples/make.rs`):

```rust
use frontseat_plugin::prelude::*;
use frontseat_plugin_test::{Files, TestHost, TestPlugin};

#[test]
fn a_makefile_is_a_project() {
    let plugin = TestPlugin::new(crate::served());
    let host = TestHost::new(Files::new().with("app/Makefile", "test:\n"));
    let context = plugin.context(Profile::Development);
    let analyzed = plugin.analyze(&host, &context, &["app"]).answered();
    assert_eq!(analyzed.analyses[0].projects[0].kind, "make-project");
}
```

- `Files` is the repository: files added one by one, or read from a
  directory such as `testdata/valid` (`Files::from_dir`).
- `TestHost` serves them as Frontseat does: it globs as Frontseat's host
  globs, in the pattern language `host.proto` says, records what the
  plugin read (`reads`), and fails a read or a glob when told to. A
  call in which a read failed fails with that failure, as Frontseat
  fails it, whatever the plugin made of the read.
- `TestPlugin` asks the plugin through the SDK's own dispatch, each
  call encoded and its answer decoded, so the SDK checks options and
  profiles as it does in a module. `answered()` is the answer, and
  `refused(reason)` the refusal, an `Error` whose reason is as the
  plugin wrote it. An answer that is not looked at is a warning: a test
  that asks and checks nothing proves nothing.

## What it cannot check

It runs the plugin natively, and so checks the plugin's own logic and
the SDK's half of the contract. It cannot check what only Frontseat's
host shows of the built module: its 32-bit word, the memory it is given,
the shapes of its exports, the host's own checks of an answer, and
cancellation. Those are checked by running the built `.wasm` in the
host.
