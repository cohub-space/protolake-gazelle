# Dockerfile for building and testing protolake-gazelle with Bazel 8.3
FROM ubuntu:24.04

# Install dependencies
RUN apt-get update && apt-get install -y \
    curl \
    git \
    build-essential \
    python3 \
    && rm -rf /var/lib/apt/lists/*

# Install Bazel 8.3.0
RUN curl -fLo /usr/local/bin/bazel https://github.com/bazelbuild/bazel/releases/download/8.3.0/bazel-8.3.0-linux-x86_64 \
    && chmod +x /usr/local/bin/bazel

# Install Go 1.23
RUN curl -fLo /tmp/go.tar.gz https://go.dev/dl/go1.23.3.linux-amd64.tar.gz \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz

# Set up environment
ENV PATH="/usr/local/go/bin:$PATH"
ENV GOPATH="/go"
ENV PATH="$GOPATH/bin:$PATH"

# Create workspace directory
WORKDIR /workspace

# Set up bazelrc for better caching
RUN echo "build --disk_cache=/tmp/bazel-cache" > /etc/bazel.bazelrc

CMD ["/bin/bash"]