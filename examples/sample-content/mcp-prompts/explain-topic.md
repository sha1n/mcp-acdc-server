---
name: explain-topic
description: Explains an ACDC topic using the available resources.
arguments:
  - name: topic
    description: The topic to explain (e.g., resources, prompts, search, configuration).
    required: true
  - name: depth
    description: "Level of detail: brief or detailed. Defaults to brief."
    required: false
---
Explain the following ACDC topic: **{{.topic}}**.

Start by reading `acdc://guides/getting-started` for context on key concepts.

{{if .depth}}Provide a **{{.depth}}** explanation.{{else}}Provide a concise overview.{{end}}

Include practical examples where possible and point the user to related resources for further reading.
