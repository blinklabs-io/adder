#!/bin/bash
set -e

# Keep track of startup success to handle fault-tolerant cleanup
SUCCESS=false

cleanup() {
    if [ "$SUCCESS" = "false" ]; then
        echo "⚠️  Startup failed. Cleaning up half-started resources..."
        container stop dingo >/dev/null 2>&1 || true
        container rm dingo >/dev/null 2>&1 || true
        rm -f "$HOME/dingo-ipc/node.socket"
        rmdir "$HOME/dingo-ipc" 2>/dev/null || true
    fi
}

# Register the cleanup function to trigger on any premature exit
trap cleanup EXIT

echo "🔍 Initializing Apple native container system..."
# 1. Start system services
container system start

# 2. Clean up any previous dingo container if it exists
if container inspect dingo >/dev/null 2>&1; then
    echo "🧹 Stopping and removing previous dingo container..."
    container stop dingo >/dev/null 2>&1 || true
    container rm dingo >/dev/null 2>&1 || true
fi

# 3. Ensure socket directory exists on host
mkdir -p "$HOME/dingo-ipc"
rm -f "$HOME/dingo-ipc/node.socket"

echo "🚀 Starting Dingo v0.70.9 inside native container with socket publishing..."
# 4. Start Dingo
container run \
  --name dingo \
  --publish-socket "$HOME/dingo-ipc/node.socket:/ipc/node.socket" \
  --detach \
  ghcr.io/blinklabs-io/dingo:0.70.9 \
  serve --socket-path /ipc/node.socket --network preview

echo "⏳ Waiting for Dingo to initialize socket..."
for i in {1..10}; do
    if [ -S "$HOME/dingo-ipc/node.socket" ]; then
        break
    fi
    sleep 1
done

if [ ! -S "$HOME/dingo-ipc/node.socket" ]; then
    echo "❌ Error: Dingo socket was not created in time."
    exit 1
fi

# Disable the cleanup trap since startup succeeded cleanly
SUCCESS=true
trap - EXIT

echo "✅ Dingo is online and listening on ~/dingo-ipc/node.socket!"
echo ""
echo "🎉 You can now run Adder natively on your Mac Host using:"
echo "--------------------------------------------------------"
echo "go run ./cmd/adder --input chainsync \\"
echo "  --input-chainsync-socket-path ~/dingo-ipc/node.socket \\"
echo "  --input-chainsync-network preview \\"
echo "  --input-chainsync-intersect-tip=false \\"
echo "  --output log"
echo "--------------------------------------------------------"
