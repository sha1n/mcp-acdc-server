# Docker with Local Content Path

This is the easiest way to try MCP ACDC with your own local markdown resources. It uses a direct Docker volume mount to share local files with the server container.

## ✨ Benefits

- **Rapid Iteration**: Changes to your local markdown files are immediately reflected in the server (after a short discovery delay).
- **Simplicity**: Single service setup with no complex orchestration.
- **Easy Customization**: Just drop your markdown files into the `sample-content/` directory (or change the mount path).

## 🚀 Usage

### 1. Start the server
```bash
docker-compose up -d
```

### 2. Verify
Test the health endpoint:
```bash
curl http://localhost:8080/health
```

### 3. Connect Your Agent
Configure your MCP client:
- **URL**: `http://localhost:8080/sse`
- **Transport**: SSE (Server-Sent Events)

## 📂 Content Structure

This example mounts the shared `examples/sample-content/` directory. You can iterate on the resources there or point the `volumes` section in `docker-compose.yml` to your own documentation path.

## Cleanup

```bash
docker-compose down
```
