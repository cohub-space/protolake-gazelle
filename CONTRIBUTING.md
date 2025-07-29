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
- All tests must pass
- Update documentation as needed
- Follow Go coding standards and conventions
- Keep commits focused and atomic
- Write clear commit messages following conventional commits format

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

Run all tests:
```bash
bazel test //...
```

Run specific tests:
```bash
bazel test //language:go_default_test
bazel test //:integration_test
```

## Code Style

- Follow standard Go formatting (use `gofmt`)
- Use meaningful variable and function names
- Add comments for exported functions and types
- Keep functions small and focused

## Reporting Issues

When reporting issues, please include:
- Bazel version
- Operating system
- Go version
- Steps to reproduce the issue
- Expected behavior
- Actual behavior

## Code of Conduct

Please be respectful and constructive in all interactions. We are committed to providing a welcoming and inclusive environment for all contributors.

## Questions?

If you have questions, feel free to open an issue with the "question" label.
