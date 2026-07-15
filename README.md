# protolake-gazelle

A [Bazel Gazelle](https://github.com/bazelbuild/bazel-gazelle) language
extension that generates BUILD files for proto bundles in
[Proto Lake](https://github.com/cohub-space/protolake) workspaces.

For most users, you don't install this directly — Proto Lake bundles it
into the lake's `MODULE.bazel`. This README is for: standalone use
(running gazelle without protolake), local development of the extension
itself, and reference for the rules it emits.

For deep architecture (configuration loading, transitive dependency
collection, version handling) see the
[cohub-knowledge protolake-gazelle doc](https://github.com/cohub-space/cohub-knowledge/blob/main/docs/knowledge/protolake/protolake-gazelle.md).

## What it generates

For each directory containing a `bundle.yaml`, the extension emits the
full set of bundle rules: an aggregated `proto_library`, per-language
gRPC + bundle rules, and per-language publish targets. Package
coordinates are baked at gazelle time from the bundle's configuration;
the version resolves from `bundle.yaml` at build/run time via a
`bundle_yaml` label on the bundle rules and `--bundle-yaml` args on the
POM genrule and publishers. The only gazelle-time version literal is
the `maven_publish` `coordinates` string (rules_jvm_external has no
runtime placeholder for it).

For a `bundle.yaml` like:

```yaml
name: user-service
display_name: User Service
description: User service proto definitions
bundle_prefix: com.example
version: "1.0.0"
config:
  languages:
    java:
      enabled: true
      group_id: "com.example.proto"
      artifact_id: "user-service-proto"
    python:
      enabled: true
      package_name: "example_user_proto"
    javascript:
      enabled: true
      package_name: "@example/user-service-proto"
```

the extension emits (abbreviated):

```starlark
load("@rules_jvm_external//private/rules:maven_publish.bzl", "maven_publish")
load("@rules_python//python:defs.bzl", "py_binary")
load("@rules_proto//proto:defs.bzl", "proto_library")
load("@rules_proto_grpc_java//:defs.bzl", "java_grpc_library")
load("@rules_proto_grpc_python//:defs.bzl", "python_grpc_library")
load("//tools:proto_bundle.bzl", "java_proto_bundle", "py_proto_bundle", "js_proto_bundle")
load("//tools:es_proto.bzl", "es_proto_compile")

# Aggregated proto target — direct + recursive subdirectories
proto_library(name = "user-service_all_protos", deps = [...])

# Java path: bundle JAR + POM + maven_publish
java_grpc_library(name = "user-service_java_grpc", protos = [...])
java_proto_bundle(
    name = "user-service_java_bundle",
    group_id = "com.example.proto",
    artifact_id = "user-service-proto",
    bundle_yaml = ":bundle.yaml",
    proto_deps = [":user-service_all_protos"],
    java_deps = [":user-service_java_grpc"],
    java_grpc_deps = [":user-service_java_grpc"],
)
genrule(
    name = "user-service_pom",
    srcs = ["bundle.yaml"],
    outs = ["user-service.pom.xml"],
    cmd = "$(location //tools:pom_generator) --group-id ... --bundle-yaml $(location bundle.yaml) ... --out $@",
    tools = ["//tools:pom_generator"],
)
maven_publish(
    name = "publish_user-service_to_maven",
    coordinates = "com.example.proto:user-service-proto:1.0.0",
    pom = ":user-service_pom",
    artifact = ":user-service_java_bundle",
)

# Python path: wheel + py_binary publish
python_grpc_library(name = "user-service_python_grpc", protos = [...])
py_proto_bundle(
    name = "user-service_py_bundle",
    package_name = "example_user_proto",
    bundle_yaml = ":bundle.yaml",
    proto_deps = [":user-service_all_protos"],
    py_grpc_deps = [":user-service_python_grpc"],
)
py_binary(
    name = "publish_user-service_to_pypi",
    srcs = ["//tools:publish/pypi_publisher_generated.py"],
    main = "publish/pypi_publisher_generated.py",
    args = [
        "$(location :user-service_py_bundle)",
        "--package-name=example_user_proto",
        "--bundle-yaml=$(location bundle.yaml)",
    ],
    data = [
        ":user-service_py_bundle",
        "bundle.yaml",
    ],
    deps = ["//tools:publisher_utils"],
)

# JS / TS path: tarball + py_binary publish (Connect-ES v2 codegen)
es_proto_compile(name = "user-service_es_proto", protos = [...])
js_proto_bundle(
    name = "user-service_js_bundle",
    package_name = "@example/user-service-proto",
    bundle_yaml = ":bundle.yaml",
    proto_deps = [":user-service_all_protos"],
    es_deps = [":user-service_es_proto"],
)
py_binary(name = "publish_user-service_to_npm", ...)  # similar to pypi
```

The publish targets are executable rules invoked via `bazel run` — see
[publisher-execution-model.md](https://github.com/cohub-space/cohub-knowledge/blob/main/docs/designs/protolake/publisher-execution-model.md)
for the rationale (bazel-native side-effecting model, fail-fast,
tag-driven release flow via release-please).

## Installation

### Via Proto Lake (typical)

If you're using a Proto Lake workspace, the extension is wired in
automatically by `protolakew init` — `MODULE.bazel` and `BUILD.bazel`
are generated for you. No manual setup needed.

### Standalone

In your `MODULE.bazel`:

```starlark
bazel_dep(name = "protolake_gazelle", version = "0.0.1")
git_override(
    module_name = "protolake_gazelle",
    remote = "https://github.com/cohub-space/protolake-gazelle.git",
    tag = "v0.3.0",   # or `commit = "<sha>"` for exact pinning
)
```

Then add a `lake.yaml` at the workspace root, a `bundle.yaml` in each
proto directory, and run `bazel run //:gazelle`. The extension also
expects supporting tools (`//tools:pom_generator`, `//tools:publish/*`,
`//tools:proto_bundle.bzl`, etc.) — these are provided by Proto Lake
templates. Standalone use without Proto Lake is possible but you'll
need to set up the tools yourself.

## Configuration

The extension reads `lake.yaml` (walked up from the bundle directory)
and `bundle.yaml` (the current directory). Bundle config overrides
lake defaults via tri-state pointer semantics — `*bool` fields
distinguish *unset* (inherit) from *explicitly false* (disable).

**`lake.yaml`** at the lake root:

```yaml
config:
  language_defaults:
    java:
      enabled: true
      group_id: "com.example.proto"
    python:
      enabled: true
      package_prefix: "example_proto"
    javascript:
      enabled: true
      package_scope: "@example"
```

**`bundle.yaml`** in each bundle directory (overrides lake defaults):

```yaml
name: special-service
version: "1.0.0"
config:
  languages:
    java:
      group_id: "com.special.proto"   # overrides lake default
      artifact_id: "special-proto"
    javascript:
      enabled: false                  # explicitly disabled
```

## Gazelle directives

```starlark
# In a BUILD.bazel: disable protolake-gazelle for this directory only
# gazelle:protolake false
```

## Development

```bash
bazel build //...        # build the extension + integration test fixtures
bazel test //...         # run unit + integration tests
go test ./language/...   # run Go unit tests directly (faster than bazel)
```

The integration test (`//:integration_test`) generates BUILD files for
a fixture two-bundle workspace and asserts the contents — fastest
signal for rule-emission changes.

When iterating on the extension while testing against a real lake, set
`PROTOLAKE_GAZELLE_SOURCE_PATH` on `protolakew` and Proto Lake will use
your local checkout instead of the published tag. See
[dev-workflow.md](https://github.com/cohub-space/cohub-knowledge/blob/main/docs/knowledge/protolake/dev-workflow.md).

## License

Apache License 2.0 — see [LICENSE](LICENSE).

## Related projects

- [Proto Lake](https://github.com/cohub-space/protolake) — the main service that consumes this extension
- [Bazel Gazelle](https://github.com/bazelbuild/bazel-gazelle) — the underlying BUILD file generator
