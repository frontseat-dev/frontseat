// A Backstage backend's app, for one platform, as Backstage's own image
// recipe makes one: the workspace its build packed (dist/bundle.tar.gz),
// unpacked, and `yarn workspaces focus --all --production` run in it,
// with no network, from the cache the install was fetched into; then
// written, the same bytes wherever and whenever it is made, to one file.
//
// node -e <this> <the install's root> <os> <cpu> <the file to write>
//
// Run in the backend's directory, by the Node mise.toml pins, with the
// archive library the install laid out (tar, which the CLI packs with).
const {spawnSync} = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const [root, os, cpu, file] = process.argv.slice(1);
const install = path.resolve(root);
const out = path.resolve(file);
const tar = require(path.join(install, "node_modules/tar"));
const work = path.join(path.dirname(out), `app-${os}-${cpu}`);
fs.rmSync(work, {recursive: true, force: true});
fs.mkdirSync(work, {recursive: true});
tar.extract({file: "dist/bundle.tar.gz", cwd: work, sync: true});

// A native module is built for the machine the action runs on alone, so an
// app for another platform runs no script, and fails where one would run,
// rather than ship a module built for the wrong machine.
const native = os === process.platform && cpu === process.arch;
fs.writeFileSync(path.join(work, ".yarnrc.yml"), [
  "nodeLinker: node-modules",
  "enableGlobalCache: false",
  `enableScripts: ${native}`,
  "supportedArchitectures:",
  `  os: [${os}]`,
  `  cpu: [${cpu}]`,
  "  libc: [glibc, musl]",
  "",
].join("\n"));
const run = spawnSync("yarn", ["workspaces", "focus", "--all", "--production"], {
  cwd: work,
  encoding: "utf8",
  env: {
    ...process.env,
    YARN_IGNORE_PATH: "1",
    YARN_CACHE_FOLDER: path.join(install, ".yarn/cache"),
    YARN_ENABLE_NETWORK: "false",
    YARN_ENABLE_IMMUTABLE_INSTALLS: "true",
    YARN_CHECKSUM_BEHAVIOR: "throw",
    YARN_ENABLE_TELEMETRY: "false",
    // A native module is built from its source, against this Node's own
    // headers, and node-gyp's Python writes no bytecode of its own.
    npm_config_build_from_source: "true",
    npm_config_nodedir: path.dirname(path.dirname(process.execPath)),
    PYTHONDONTWRITEBYTECODE: "1",
  },
});
process.stdout.write(run.stdout ?? "");
process.stderr.write(run.stderr ?? "");
if (run.error) {
  console.error(`yarn could not be started: ${run.error.message}`);
  process.exit(127);
}
if (run.status !== 0) {
  process.exit(run.status ?? 1);
}
const disabled = run.stdout.split("\n").filter((line) => line.includes("YN0004"));
if (disabled.length > 0) {
  console.error(`the app for ${os}/${cpu} needs these packages built for it, which only a worker of that platform builds, or which dependenciesMeta.<package>.built: false leaves unbuilt:\n${disabled.join("\n")}`);
  process.exit(1);
}

// What node-gyp built names the directory it was built in: of its build
// tree only what it linked is kept, the module a package loads.
const trim = (directory) => {
  for (const entry of fs.readdirSync(directory, {withFileTypes: true})) {
    const at = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      trim(at);
      if (fs.readdirSync(at).length === 0) fs.rmdirSync(at);
    } else if (!entry.name.endsWith(".node")) {
      fs.rmSync(at);
    }
  }
};
const packages = (directory) => {
  for (const entry of fs.readdirSync(directory, {withFileTypes: true})) {
    if (!entry.isDirectory()) continue;
    const at = path.join(directory, entry.name);
    if (entry.name.startsWith("@")) {
      packages(at);
      continue;
    }
    const build = path.join(at, "build");
    if (fs.existsSync(path.join(at, "binding.gyp")) && fs.existsSync(build)) {
      trim(build);
      if (fs.existsSync(build) && fs.readdirSync(build).length === 0) fs.rmdirSync(build);
    }
    const nested = path.join(at, "node_modules");
    if (fs.existsSync(nested)) packages(nested);
  }
};
packages(path.join(work, "node_modules"));

// The app: what is beneath its directory but Yarn's own, in order, with
// no time and no owner of this machine's.
const entries = fs.readdirSync(work).filter((entry) => entry !== ".yarn" && entry !== ".yarnrc.yml").sort();
tar.create({gzip: {mtime: 0}, file: out, cwd: work, portable: true, noMtime: true, sync: true}, entries);
fs.rmSync(work, {recursive: true, force: true});
