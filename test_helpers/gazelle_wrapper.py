#!/usr/bin/env python3
"""Test wrapper for running Gazelle with proto fixes"""

import os
import subprocess
import sys

def run_command(cmd, cwd=None):
    """Run a command and return its exit code."""
    print(f"Running: {' '.join(cmd)}")
    result = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)
    if result.stdout:
        print(result.stdout)
    if result.stderr:
        print(result.stderr, file=sys.stderr)
    return result.returncode

def main():
    workspace_root = os.environ.get('BUILD_WORKSPACE_DIRECTORY', '.')

    # Step 1: Run standard Gazelle
    print("Step 1: Running standard Gazelle...")
    exit_code = run_command(["bazel", "run", "//:gazelle"], cwd=workspace_root)
    if exit_code != 0:
        return exit_code

    # Step 2: Fix imports
    print("\nStep 2: Fixing proto imports...")
    fix_script = os.path.join(os.path.dirname(__file__), "fix_proto_imports.py")
    exit_code = run_command([sys.executable, fix_script, workspace_root])
    if exit_code != 0:
        return exit_code

    # Step 3: Run Gazelle with protolake extension
    print("\nStep 3: Running Gazelle with protolake extension...")
    exit_code = run_command(["bazel", "run", "//:gazelle", "--", "-lang=protolake"], cwd=workspace_root)

    return exit_code

if __name__ == '__main__':
    sys.exit(main())