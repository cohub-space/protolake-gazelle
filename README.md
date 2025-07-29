# protolake-gazelle

A Gazelle extension for Proto Lake that automatically generates Bazel BUILD files for protocol buffer service bundles.

## Overview

protolake-gazelle extends [Bazel Gazelle](https://github.com/bazelbuild/bazel-gazelle) to understand Proto Lake's bundle structure and automatically generate BUILD files with multi-language support (Java, Python, JavaScript/TypeScript).

## Features

- **Automatic Bundle Detection**: Finds directories containing `bundle.yaml` files
- **Multi-Language Support**: Generates rules for Java, Python, and JavaScript/TypeScript
- **Configuration Inheritance**: Bundle settings inherit from lake-wide defaults
- **gRPC Support**: Automatically generates gRPC libraries when services are detected
- **Hybrid Publishing**: Supports both static configuration and runtime parameters

## Installation

### Using as a Bazel Module

In your `MODULE.bazel`:

```starlark
bazel_dep(name = "protolake_gazelle", version = "0.0.1")

git_override(
    module_name = "protolake_gazelle",
    remote = "https://github.com/cohub-space/protolake-gazelle.git",
    commit = "COMMIT_SHA", # Use a specific commit
)
```

### Using with Proto Lake

Proto Lake automatically includes this extension when creating new lakes. No manual installation needed!

## Usage

### Basic Usage

Create a `bundle.yaml` in your proto directory:

```yaml
bundle:
  name: "user-service"
  owner: "platform-team"
  proto_package: "com.example.user"
  description: "User service proto definitions"
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

Run Gazelle:

```bash
bazel run //:gazelle
```

### Generated BUILD File

The extension will generate a BUILD file with:

```starlark
# Auto-generated proto aggregation
proto_library(
    name = "user_service_all_protos",
    srcs = glob(["**/*.proto"]),
    visibility = ["//visibility:public"],
)

# Java bundle
java_proto_library(
    name = "user_service_java_proto",
    deps = [":user_service_all_protos"],
)

java_proto_bundle(
    name = "user_service_java_bundle",
    proto_deps = [":user_service_all_protos"],
    java_deps = [":user_service_java_proto"],
    group_id = "com.example.proto",
    artifact_id = "user-service-proto",
)

# Python bundle  
py_proto_library(
    name = "user_service_py_proto",
    deps = [":user_service_all_protos"],
)

py_proto_bundle(
    name = "user_service_py_bundle",
    proto_deps = [":user_service_all_protos"],
    py_deps = [":user_service_py_proto"],
    package_name = "example_user_proto",
)

# JavaScript bundle
js_proto_library(
    name = "user_service_js_proto",
    deps = [":user_service_all_protos"],
)

js_proto_bundle(
    name = "user_service_js_bundle",
    proto_deps = [":user_service_all_protos"],
    js_deps = [":user_service_js_proto"],
    package_name = "@example/user-service-proto",
)
```

## Configuration

### Lake-Level Configuration

Create a `lake.yaml` at the root of your Proto Lake:

```yaml
config:
  language_defaults:
    java:
      enabled: true
      group_id: "com.company.proto"
      source_version: "11"
      target_version: "8"
    python:
      enabled: true
      package_name: "company_proto"
      python_version: ">=3.8"
    javascript:
      enabled: true
      package_name: "@company/proto"
```

### Bundle-Level Configuration

Bundle configuration in `bundle.yaml` overrides lake defaults:

```yaml
bundle:
  name: "special-service"
  owner: "special-team"
config:
  languages:
    java:
      enabled: true
      group_id: "com.special.proto" # Overrides lake default
      artifact_id: "special-proto"
```

## Directives

Control the extension behavior with Gazelle directives:

```starlark
# Disable protolake extension for this directory
# gazelle:protolake false
```

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

### Building

```bash
bazel build //...
```

### Testing

```bash
bazel test //...
```

## License

Apache License 2.0 - See [LICENSE](LICENSE) file for details.

## Support

For issues and questions:
- Open an issue on [GitHub](https://github.com/cohub-space/protolake-gazelle/issues)
- Check existing issues for solutions

## Related Projects

- [Proto Lake](https://github.com/Cohub-Space/Protolake) - The main Proto Lake service
- [Bazel Gazelle](https://github.com/bazelbuild/bazel-gazelle) - The extensible BUILD file generator
