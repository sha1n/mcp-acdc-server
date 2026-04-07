# Semantic Search Research & Proposal

## Executive Summary
This document analyzes the feasibility of adding semantic search (vector search) to the MCP ACDC Server, adhering to strict constraints: **Pure Go implementation**, **MIT/Apache 2.0 license**, **Low Memory Footprint**, and **Agent-provided query embeddings**.

**Conclusion**: The feature is feasible but requires careful management of vector dimensionality and document count to stay within the 1GB memory target. The recommended architectural pattern shifts the embedding generation responsibility to the Agent (Client), simplifying the server significantly.

## 1. Existing Solutions (Pure Go & License Compliant)

The landscape of "Pure Go" vector databases is sparse, as most high-performance solutions wrap C++ libraries (FAISS, HNSWLib). However, a few compliant options exist.

### Candidate Libraries

| Library | License | Pure Go? | Stars | Maintenance | Notes |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`fogfish/hnsw`** | **MIT** | **Yes** | ~16 | Active | Generic HNSW implementation. Best fit for license/tech constraints. |
| **`casibase/go-hnsw`** | **Apache 2.0** | **Yes** | ~6 | Low | Simple implementation, but very low community activity. |
| `philippgille/chromem-go` | MPL 2.0 | Yes | ~830 | Active | **Strong contender**, but MPL 2.0 is a "weak copyleft" license (file-level), which may not meet the strict "MIT/Apache" requirement. Excellent feature set (persistence, filtering). |
| `coder/hnsw` | CC0 (Public Domain) | Yes | ~200 | Active | Excellent quality, but "Public Domain" can sometimes be legally ambiguous in corporate settings compared to explicit MIT/Apache. |

### Recommendation
**`fogfish/hnsw`** is the safest choice regarding the "MIT/Apache Only" constraint. It provides a generic, in-memory HNSW graph implementation without external dependencies.
If **CC0** is acceptable, `coder/hnsw` is likely a more robust engineering choice due to Coder's backing and higher activity.

## 2. Feasibility & Architecture

### The "Agent-Provided Embedding" Pattern
The requirement for the Agent to provide query embeddings is **technically feasible** and highly advantageous for the server's simplicity.

**Workflow:**
1. **Tool Definition**: The server exposes a tool `semantic_search` with an argument `vector` (array of numbers).
2. **Agent Responsibility**: The Agent (LLM) recognizes it needs a vector to call this tool.
3. **Orchestration**: The Agent must call an *external* tool (e.g., `openai.embed` or a local client tool) to generate the vector for the user's query.
4. **Execution**: The Agent passes the vector to `acdc-server`.
5. **Search**: `acdc-server` performs similarity search using the in-memory index.

**Implications:**
- **Zero Runtime Dependencies**: The server does not need API keys (OpenAI, Anthropic) or heavy local inference models (ONNX/Torch).
- **Client Burden**: The user/agent *must* have access to an embedding provider. If the Agent cannot generate embeddings, this feature is inaccessible.

### Content & Metadata
- **Storage**: Embeddings for resources can be stored in the existing YAML frontmatter or as sidecar files.
- **Simplicity**: Storing in frontmatter (`embeddings: [...]`) is simplest but bloats the markdown files (1536 floats is ~10-15KB of text).
- **Recommendation**: Support frontmatter for simplicity, but consider `embeddings.json` sidecars for better developer experience (DX).

## 3. Resource Consumption Estimation

The 1GB memory limit is the primary constraint. Vector data is memory-intensive.

**Formula:**
`Memory ≈ (NumDocs * Dimensions * 4 bytes) + GraphOverhead`

### Scenarios

| Docs | Dimensions | Raw Vector RAM | Estimated Total RAM (w/ Graph) | Feasibility (<1GB) |
| :--- | :--- | :--- | :--- | :--- |
| 10,000 | 768 (e.g. BERT/MiniLM) | ~30 MB | ~100 MB | ✅ **Safe** |
| 10,000 | 1536 (OpenAI v3) | ~60 MB | ~200 MB | ✅ **Safe** |
| 100,000 | 768 | ~307 MB | ~600-800 MB | ⚠️ **Tight** |
| 100,000 | 1536 | ~614 MB | ~1.2 GB - 1.5 GB | ❌ **Over Limit** |

**Conclusion:**
- To support **100,000 documents** within **1GB**, you strictly require **smaller dimensionality** (e.g., 384 or 768 dims) or **Product Quantization (PQ)** (which pure Go libs rarely support).
- With **OpenAI embeddings (1536 dims)**, the realistic limit is around **50,000 - 60,000 documents** before hitting 1GB (assuming the Go runtime and text search index also need RAM).

## 4. Proposed Solution

### Implementation Plan
1.  **Library**: Adopt `fogfish/hnsw` (MIT).
2.  **API**: Add `semantic_search` tool:
    ```json
    {
      "name": "semantic_search",
      "arguments": {
        "vector": { "type": "array", "items": { "type": "number" } },
        "limit": { "type": "integer" }
      }
    }
    ```
3.  **Data Loading**:
    - Extend `ResourceDefinition` to include `Embedding []float32`.
    - Parse `embeddings` field from Frontmatter.
    - On startup, hydrate the in-memory HNSW index.
4.  **Configuration**:
    - `ACDC_MCP_SEARCH_SEMANTIC_ENABLED=true`
    - `ACDC_MCP_SEARCH_EMBEDDING_DIMENSION=768` (Fixed dimension validation).

### Risk Assessment
- **License Compliance**: Confirmed MIT.
- **Memory Pressure**: High. Requires strict monitoring. Go's GC overhead might push usage higher than raw calculations.
- **Agent Capability**: Relies entirely on the Agent's ability to "bring its own vector".

## 5. Decision Matrix
- **Go Ahead** if: 50k document limit (with 1536 dims) or 100k limit (with 768 dims) is acceptable.
- **Reconsider** if: 100k docs @ 1536 dims is a hard requirement (needs disk-backed or quantized solution).
