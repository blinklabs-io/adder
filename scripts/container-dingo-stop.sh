#!/bin/bash
set -e

echo "🧹 Stopping Dingo native container..."
if container inspect dingo >/dev/null 2>&1; then
    container stop dingo >/dev/null 2>&1 || true
    container rm dingo >/dev/null 2>&1 || true
    echo "✅ Dingo container stopped and removed."
else
    echo "ℹ️  No active dingo container found."
fi

# Clean up local socket folder
echo "🗑️  Cleaning up socket files..."
rm -f "$HOME/dingo-ipc/node.socket"
rmdir "$HOME/dingo-ipc" 2>/dev/null || true

# Stop system services
echo "🛑 Stopping Apple native container system services..."
container system stop >/dev/null 2>&1 || true
echo "✅ System services stopped cleanly!"
