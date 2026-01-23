# Tasks

## Documentation & Examples

- [ ] [GUIDE] Improve the content authoring guide
  - [ ] Add comprehensive examples of server instructions
- [ ] [EXAMPLES] Create a Helm Chart or Kustomize manifests for easier deployment (consider Gateway with SSL termination).

## Tooling  

- [ ] [RELEASE] Optimize the Release Drafter configuration
- [ ] [RELEASE] Create a Pull Request template
- [ ] [DEV] Add Dev Container: Create a .devcontainer configuration to standardize the development environment

## Functional

- [ ] [MCP] Add prompts support
- [ ] [MCP] Explore how to implement content based commands
- [ ] [CLI] Implement version flags (`--version` / `-v`)
- [ ] [CONTENT] Support Git repositories as content sources
  - [ ] [CONTENT] Implement scheduled synchronization and re-indexing (Note: Server metadata updates require reconnection)
- [ ] [SEARCH] Support keyword boosting in the search API, so that agents can improve search quality based on context
- [ ] [CONTENT] Support additional content file types (e.g. PDF, DOCX, etc.) as MD resource attachments. MD provides context and metadata, attachments provide content.
- [ ] [AUTH] Add Okta/OAuth2 authentication support
- [ ] [API] Generate OpenAPI Spec: Auto-generate OpenAPI/Swagger documentation for the SSE HTTP endpoints.

## Technical

### Large Content Repository Support

- [ ] Stream File Processing: Refactor ContentProvider to stream files instead of reading them entirely into memory (os.ReadFile), improving large file handling.
- [ ] Define a hard limit on the number of resources that can return from a search query

### Observability

- [ ] [LOGGING] Add more detailed logging
- [ ] [LOGGING] Research stdio MCPs logging best practices
- [ ] [LOGGING] Add log level configuration
- [x] [HEALTH] Consider adding a health check endpoint
- [ ] [METRICS] Consider adding a metrics endpoint
  - [ ] [METRICS] Consider OTel based metrics


