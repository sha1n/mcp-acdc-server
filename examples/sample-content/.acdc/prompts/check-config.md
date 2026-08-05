---
name: check-config
description: Looks up a specific ACDC configuration setting and explains how to use it.
arguments:
  - name: setting
    description: The configuration setting to look up (e.g., transport, auth-type, uri-scheme).
    required: true
  - name: format
    description: "Output format: summary or detailed. Defaults to summary."
    required: false
---
Look up the ACDC configuration setting **{{.setting}}**.

Read `acdc://reference/configuration` to find the relevant details.

{{if .format}}Respond in **{{.format}}** format.{{else}}Respond with a brief summary including the flag name, environment variable, default value, and a usage example.{{end}}
