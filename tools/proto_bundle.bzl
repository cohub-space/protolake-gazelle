# Proto bundle rules with hybrid publishing approach
# Static configuration is in BUILD files, dynamic configuration comes from environment

def java_proto_bundle(name, proto_deps=[], java_deps=[], java_grpc_deps=[], group_id="", artifact_id="", **kwargs):
    """Java proto bundle that reads version from environment"""
    
    # Create a JAR with both classes and proto sources
    native.genrule(
        name = name,
        srcs = java_deps + java_grpc_deps + proto_deps,
        outs = [name + ".jar"],
        cmd = """
        # Create temporary directory structure
        mkdir -p jar_contents/META-INF
        
        # Copy compiled Java classes (if any)
        for src in $(SRCS); do
            if [[ $$src == *.class ]]; then
                cp $$src jar_contents/
            fi
        done
        
        # Copy proto sources to root of JAR for IDE support
        for src in $(SRCS); do
            if [[ $$src == *.proto ]]; then
                cp $$src jar_contents/
            fi
        done
        
        # Create manifest
        echo "Manifest-Version: 1.0" > jar_contents/META-INF/MANIFEST.MF
        echo "Group-Id: %s" >> jar_contents/META-INF/MANIFEST.MF
        echo "Artifact-Id: %s" >> jar_contents/META-INF/MANIFEST.MF
        echo "Version: $${VERSION:-1.0.0}" >> jar_contents/META-INF/MANIFEST.MF
        
        # Create JAR (use tar if jar command not available)
        if command -v jar >/dev/null 2>&1; then
            (cd jar_contents && jar cf ../$(location %s.jar) .)
        else
            (cd jar_contents && tar cf ../$(location %s.jar) .)
        fi
        """ % (group_id, artifact_id, name, name),
        **kwargs
    )

def py_proto_bundle(name, proto_deps=[], py_deps=[], py_grpc_deps=[], package_name="", **kwargs):
    """Python proto bundle that reads version from environment"""
    
    native.genrule(
        name = name,
        srcs = py_deps + py_grpc_deps + proto_deps,
        outs = [name + ".whl"],
        cmd = """
        # Create wheel structure
        mkdir -p wheel_contents
        
        # Copy Python files
        for src in $(SRCS); do
            if [[ $$src == *.py ]]; then
                cp $$src wheel_contents/
            fi
        done
        
        # Copy proto sources
        for src in $(SRCS); do
            if [[ $$src == *.proto ]]; then
                cp $$src wheel_contents/
            fi
        done
        
        # Create minimal wheel (just touch the file for testing)
        touch $(location %s.whl)
        echo "Created Python wheel for %s version $${VERSION:-1.0.0}" > wheel_contents/info.txt
        """ % (name, package_name),
        **kwargs
    )

def js_proto_bundle(name, proto_deps=[], js_deps=[], js_grpc_web_deps=[], package_name="", **kwargs):
    """JavaScript proto bundle that reads version from environment"""
    
    native.genrule(
        name = name,
        srcs = js_deps + js_grpc_web_deps + proto_deps,
        outs = [name + ".tgz"],
        cmd = """
        # Create package structure
        mkdir -p package_contents
        
        # Copy JavaScript files
        for src in $(SRCS); do
            if [[ $$src == *.js ]]; then
                cp $$src package_contents/
            fi
        done
        
        # Copy proto sources
        for src in $(SRCS); do
            if [[ $$src == *.proto ]]; then
                cp $$src package_contents/
            fi
        done
        
        # Create minimal package (just touch the file for testing)
        touch $(location %s.tgz)
        echo "Created NPM package for %s version $${VERSION:-1.0.0}" > package_contents/info.txt
        """ % (name, package_name),
        **kwargs
    )

def build_validation(name, targets=[], **kwargs):
    """Build validation rule to ensure all targets build successfully"""
    
    native.genrule(
        name = name,
        srcs = targets,
        outs = [name + ".validation"],
        cmd = """
        echo "Build validation passed for targets: %s" > $(location %s.validation)
        """ % (" ".join(targets), name),
        **kwargs
    )
