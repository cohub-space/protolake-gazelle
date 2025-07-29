# Contributing to protolake-gazelle

Thank you for your interest in contributing to protolake-gazelle!

## Development Process

1. **Fork the repository** on GitHub
2. **Clone your fork** locally:
   ```bash
   git clone https://github.com/YOUR-USERNAME/protolake-gazelle.git
   cd protolake-gazelle
   ```
3. **Create a feature branch**:
   ```bash
   git checkout -b feature/your-feature-name
   ```
4. **Make your changes** and test them thoroughly
5. **Run tests**:
   ```bash
   bazel test //...
   ./test_protolake_gazelle.sh  # For integration tests
   ```
6. **Commit your changes** with a descriptive message:
   ```bash
   git commit -m 'feat: add new feature description'
   ```
7. **Push to your fork**:
   ```bash
   git push origin feature/your-feature-name
   ```
8. **Open a Pull Request** on GitHub

## Pull Request Guidelines

- PRs must be reviewed before merging
- All tests must pass (including CI/CD checks)
- Update documentation as needed
- Follow Go coding standards and conventions
- Keep commits focused and atomic
- Write clear commit messages following conventional commits format
- Use the provided pull request template

## CI/CD Pipeline

Our GitHub Actions workflows automatically run on every pull request:

- **Build and Test**: Builds all targets and runs unit tests
- **Integration Tests**: Runs comprehensive integration tests with Proto Lake scenarios
- **Dependency Updates**: Dependabot automatically creates PRs for dependency updates

All checks must pass before a PR can be merged.

## Commit Message Format

We follow the [Conventional Commits](https://www.conventionalcommits.org/) specification:

```
type(scope): description

[optional body]

[optional footer(s)]
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `test`: Adding or updating tests
- `refactor`: Code refactoring
- `chore`: Maintenance tasks

## Testing

Run all unit tests:
```bash
bazel test //...
```

Run integration tests:
```bash
./test_protolake_gazelle.sh
```

Run specific tests:
```bash
bazel test //language:go_default_test
bazel test //:integration_test
```

Test with a specific Bazel version:
```bash
USE_BAZEL_VERSION=8.3.0 bazel test //...
```

## Code Style

- Follow standard Go formatting (use `gofmt`)
- Use meaningful variable and function names
- Add comments for exported functions and types
- Keep functions small and focused
- Run `go mod tidy` before committing Go module changes

## Local Development Tips

1. **Testing changes locally with Proto Lake**:
   ```bash
   # In your Proto Lake workspace
   local_path_override(
       module_name = "protolake_gazelle",
       path = "/path/to/your/protolake-gazelle",
   )
   ```

2. **Debugging Gazelle extension**:
   ```bash
   # Run with debug output
   bazel run //:gazelle -- -mode=diff -v
   ```

3. **Testing with different configurations**:
   - Create test cases in `testdata/` directory
   - Each test case should have its own subdirectory with `bundle.yaml`

## Reporting Issues

When reporting issues, please include:
- Bazel version (`bazel version`)
- Operating system
- Go version (`go version`)
- Steps to reproduce the issue
- Expected behavior
- Actual behavior
- Relevant `bundle.yaml` and proto file structure

## Release Process

Releases are created through the manual Release workflow:
1. Maintainers trigger the workflow with a version tag
2. CI runs all tests
3. A draft release is created with auto-generated notes
4. Maintainers review and publish the release

## Code of Conduct

Please be respectful and constructive in all interactions. We are committed to providing a welcoming and inclusive environment for all contributors.

## Questions?

If you have questions, feel free to:
- Open an issue with the "question" label
- Start a discussion in the GitHub Discussions tab (if enabled)
- Reach out to the maintainers listed in CODEOWNERS (@khichou)
