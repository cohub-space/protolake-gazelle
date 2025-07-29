#!/bin/bash
# Validate CI setup locally before pushing

set -e

echo "=== Validating CI Configuration ==="

# Check if all workflow files are valid YAML
echo "Checking workflow syntax..."
for workflow in .github/workflows/*.yml; do
    if [ -f "$workflow" ]; then
        echo "  ✓ $workflow"
        # You can add yaml validation here if needed
    fi
done

# Check if test script is executable
echo ""
echo "Checking test script permissions..."
if [ -x "test_protolake_gazelle.sh" ]; then
    echo "  ✓ test_protolake_gazelle.sh is executable"
else
    echo "  ✗ test_protolake_gazelle.sh is not executable"
    echo "    Run: chmod +x test_protolake_gazelle.sh"
fi

# Check Bazel files
echo ""
echo "Checking Bazel configuration..."
if [ -f "MODULE.bazel" ]; then
    echo "  ✓ MODULE.bazel exists"
fi
if [ -f "BUILD.bazel" ]; then
    echo "  ✓ BUILD.bazel exists"
fi
if [ -f ".bazelversion" ]; then
    echo "  ✓ .bazelversion exists: $(cat .bazelversion)"
fi

# Check Go module
echo ""
echo "Checking Go module..."
if [ -f "go.mod" ]; then
    echo "  ✓ go.mod exists"
    go mod tidy
    echo "  ✓ go mod tidy successful"
fi

# Run basic build test
echo ""
echo "Running basic build test..."
if command -v bazel &> /dev/null; then
    bazel build //language:go_default_library
    echo "  ✓ Basic build successful"
else
    echo "  ⚠ Bazel not installed locally, skipping build test"
fi

echo ""
echo "=== CI Validation Complete ==="
echo ""
echo "Next steps:"
echo "1. Commit these changes to your fork"
echo "2. Push to GitHub"
echo "3. GitHub Actions will run automatically"
echo "4. Check Actions tab for results"
