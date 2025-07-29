#!/usr/bin/env python3
"""Fix proto imports for Bazel 8 compatibility"""

import os
import re
import sys

def fix_build_file(build_path):
    """Fix proto imports in a single BUILD file."""
    if not os.path.exists(build_path):
        return False

    with open(build_path, 'r') as f:
        content = f.read()

    original_content = content

    # Remove load statements for proto_library from rules_proto
    pattern = r'load\s*\(\s*"@rules_proto//proto:defs\.bzl"\s*,\s*[^)]+\)\s*\n?'
    content = re.sub(pattern, '', content)

    # Clean up empty lines
    content = re.sub(r'\n\s*\n\s*\n', '\n\n', content)

    if content != original_content:
        with open(build_path, 'w') as f:
            f.write(content)
        return True
    return False

def main():
    workspace_root = sys.argv[1] if len(sys.argv) > 1 else "."

    for root, dirs, files in os.walk(workspace_root):
        dirs[:] = [d for d in dirs if not d.startswith('bazel-')]

        for file in files:
            if file in ('BUILD', 'BUILD.bazel'):
                build_path = os.path.join(root, file)
                if fix_build_file(build_path):
                    print(f"Fixed: {build_path}")

    return 0

if __name__ == '__main__':
    sys.exit(main())