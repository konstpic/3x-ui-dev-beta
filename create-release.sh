#!/bin/bash

# Script to create GitHub release in konstpic/SharX repository
# Usage: ./create-release.sh <tag> [description_file]
# Example: ./create-release.sh v1.0.0

set -e

TAG="${1:-v1.0.0}"
DESC_FILE="${2:-release-description.md}"

if [ -z "$SHARX_REPO_TOKEN" ]; then
    echo "❌ Error: SHARX_REPO_TOKEN environment variable is not set"
    echo "Please set it with: export SHARX_REPO_TOKEN=your_token"
    exit 1
fi

# Check if description file exists
if [ ! -f "$DESC_FILE" ]; then
    echo "⚠️  Description file '$DESC_FILE' not found, using default description"
    RELEASE_BODY=$(cat <<EOF
## Release $TAG

**SharX** is a fork of the original **3XUI** panel with enhanced features and monitoring capabilities.

### ✨ Key Features

- **Node Mode**: One panel manages multiple nodes
- **PostgreSQL**: Full migration from SQLite
- **Redis Integration**: Enhanced performance with caching
- **Grafana Integration**: Advanced monitoring with Prometheus metrics and Loki logs
- **Docker-Based**: Easy deployment with pre-built images
- **HWID Protection**: Device identification (Beta, Happ & V2RayTun)
- **Auto SSL**: Let's Encrypt certificates with auto-renewal
- **Environment-Based Configuration**: Flexible domain, port, and certificate management via environment variables

### 🐳 Docker Images

Images are available in Harbor (configure your Harbor credentials in secrets).

### 📦 Quick Start

For detailed installation instructions, see the [README](https://github.com/konstpic/SharX#quick-start--быстрый-старт).

### 📝 Installation

\`\`\`bash
git clone https://github.com/konstpic/SharX.git
cd SharX
sudo bash ./install_ru.sh
\`\`\`

### 🔄 Changes

See commit history for detailed changes.
EOF
)
else
    echo "📄 Using description from $DESC_FILE"
    RELEASE_BODY=$(cat "$DESC_FILE")
fi

# Determine if it's a beta release
if [[ "$TAG" == *"-beta" ]] || [[ "$TAG" == *"beta" ]]; then
    PRERELEASE=true
else
    PRERELEASE=false
fi

echo "🚀 Creating release $TAG in konstpic/SharX..."
echo "   Prerelease: $PRERELEASE"

# Create release using GitHub API
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST \
  -H "Accept: application/vnd.github+json" \
  -H "Authorization: Bearer $SHARX_REPO_TOKEN" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  https://api.github.com/repos/konstpic/SharX/releases \
  -d "{
    \"tag_name\": \"$TAG\",
    \"name\": \"Release $TAG\",
    \"body\": $(echo "$RELEASE_BODY" | jq -Rs .),
    \"draft\": false,
    \"prerelease\": $PRERELEASE
  }")

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
RESPONSE_BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" -eq 201 ]; then
    echo "✅ Release created successfully!"
    echo "$RESPONSE_BODY" | jq -r '.html_url'
elif [ "$HTTP_CODE" -eq 422 ]; then
    echo "⚠️  Release already exists, attempting to update..."
    # Get release ID
    RELEASE_ID=$(curl -s \
      -H "Accept: application/vnd.github+json" \
      -H "Authorization: Bearer $SHARX_REPO_TOKEN" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "https://api.github.com/repos/konstpic/SharX/releases/tags/$TAG" | jq -r '.id')
    
    if [ "$RELEASE_ID" != "null" ] && [ -n "$RELEASE_ID" ]; then
        # Update existing release
        UPDATE_RESPONSE=$(curl -s -w "\n%{http_code}" -X PATCH \
          -H "Accept: application/vnd.github+json" \
          -H "Authorization: Bearer $SHARX_REPO_TOKEN" \
          -H "X-GitHub-Api-Version: 2022-11-28" \
          "https://api.github.com/repos/konstpic/SharX/releases/$RELEASE_ID" \
          -d "{
            \"name\": \"Release $TAG\",
            \"body\": $(echo "$RELEASE_BODY" | jq -Rs .),
            \"draft\": false,
            \"prerelease\": $PRERELEASE
          }")
        
        UPDATE_CODE=$(echo "$UPDATE_RESPONSE" | tail -n1)
        if [ "$UPDATE_CODE" -eq 200 ]; then
            echo "✅ Release updated successfully!"
            echo "$UPDATE_RESPONSE" | sed '$d' | jq -r '.html_url'
        else
            echo "❌ Failed to update release. HTTP code: $UPDATE_CODE"
            echo "$UPDATE_RESPONSE" | sed '$d'
            exit 1
        fi
    else
        echo "❌ Release exists but could not get release ID"
        exit 1
    fi
else
    echo "❌ Failed to create release. HTTP code: $HTTP_CODE"
    echo "$RESPONSE_BODY"
    exit 1
fi
