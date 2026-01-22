# Docker with Pre-packaged Content Image

This example demonstrates a production-like pattern where content is packaged into a dedicated Docker image (`sha1n/mcp-acdc-content`) and mounted into the server via an init container.

## How it works

1.  **Init Container**: Starts first, copies content from `sha1n/mcp-acdc-content` into a shared volume, and then exits.
2.  **Server Container**: Starts after the init container succeeds, mounting the shared volume as read-only.

## ✨ Benefits

- **Immutability**: Content is versioned and tied to a specific image tag.
- **Security**: The server doesn't need write access to its content directory.
- **Portability**: No local files are required; the entire setup is pull-and-run.

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

## Cleanup

```bash
docker-compose down -v
```
