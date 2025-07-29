# Proto Lake Gazelle Extension

A custom Gazelle language extension that automatically generates service bundle BUILD rules for Proto Lake services.

## Overview

This extension detects `bundle.yaml` files in your Proto Lake and automatically generates the appropriate BUILD rules for creating multi-language proto bundles (Java, Python, JavaScript).

## Features

- **Automatic Service Detection**: Finds `bundle.yaml` files to identify service boundaries
- **Bundle Generation**: Creates aggregated proto_library rules and language-specific bundles
- **Multi-Language Support**: Generates Java, Python, and JavaScript proto packages
- **Publishing Targets**: Creates convenient aliases for publishing to Maven, PyPI, and NPM

## How It Works

1. When Gazelle runs, this extension looks for `bundle.yaml` files
2. For each service found, it:
    - Collects all proto_library targets under that service directory
    - Creates an aggregated `<service>_all_protos` target
    - Generates language-specific proto libraries and bundles
    - Adds publishing aliases for convenience

## Generated Rules

For a service with name `service_a`, the extension generates:

```starlark
# Aggregate all protos
proto_library(
    name = "service_a_all_protos",
    deps = [
        # All proto targets under this service
    ],
    visibility = ["//visibility:public"],
)

# Java support
java_proto_library(
    name = "service_a_java_proto",
    deps = [":service_a_all_protos"],
)

java_proto_bundle(
    name = "service_a_java_bundle",
    proto_deps = [":service_a_all_protos"],
    java_deps = [":service_a_java_proto"],
    group_id = "com.company.platform",
    artifact_id = "service-a-proto",
    version = "1.0.0",
)

# Python support
py_proto_library(
    name = "service_a_py_proto",
    deps = [":service_a_all_protos"],
)

py_proto_bundle(
    name = "service_a_py_bundle",
    proto_deps = [":service_a_all_protos"],
    py_deps = [":service_a_py_proto"],
    package_name = "company-service-a-proto",
    version = "1.0.0",
)

# Publishing aliases
alias(
    name = "publish_to_maven",
    actual = ":service_a_java_bundle",
)

alias(
    name = "publish_to_pypi", 
    actual = ":service_a_py_bundle",
)
```

## Usage

1. The extension is automatically included when you run:
   ```bash
   bazel run //:gazelle
   ```

2. Or with the wrapper that also fixes imports:
   ```bash
   bazel run //tools:gazelle_wrapper
   ```

## Configuration

You can control the extension using Gazelle directives:

```starlark
# Enable/disable protolake extension (enabled by default)
# gazelle:protolake enabled
```

## Service Configuration

The `bundle.yaml` file should have this structure:

```yaml
service:
  name: service_a
  owner: team-platform
  language_targets:
    java:
      group_id: com.company.platform
      artifact_id: service-a-proto
    python:
      package_name: company-service-a-proto
    javascript:
      package_name: "@company/service-a-proto"
```

## Development

To modify this extension:

1. Make changes to the Go source files
2. Test with: `bazel test //...`
3. Build with: `bazel build //...`

## Future Enhancements

- Version management from bundle.yaml
- Better proto target discovery (walk subdirectories)
- JavaScript/TypeScript proto generation support
- Custom Gazelle directives for fine-grained control