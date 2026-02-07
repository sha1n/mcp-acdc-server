---
name: review-code
description: Performs a detailed code review of a commit or all local changes.
arguments:
  - name: commit
    description: A git commit hash to review. If omitted, reviews all uncommitted changes.
    required: false
  - name: focus
    description: An area to focus on (e.g., security, performance, style).
    required: false
---
Please perform a detailed code review.

{{if .commit}}
Review the changes introduced in commit `{{.commit}}`.
{{else}}
Review all currently uncommitted changes in the repository.
{{end}}

{{if .focus}}
Pay special attention to **{{.focus}}**.
{{else}}
Cover code correctness, potential bugs, security concerns, and style consistency.
{{end}}

Provide actionable feedback with code suggestions where appropriate.
