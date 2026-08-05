# Changelog

All notable changes to the Kastor VS Code extension are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-08-05

### Added

- Syntax highlighting for `.agent`, `.tool`, `.prompt`, `.kastor`, and
  `kastor.hcl`, using HashiCorp's TextMate scope vocabulary so Kastor inherits
  each theme's Terraform colors.
- Highlighting for Kastor-specific constructs: block references
  (`model.fast`, `agent.forecast.output.summary`), bare type keywords, the
  `source` kind and `target` type enums, and `{{variable}}` prompt templates.
- Distinct file icons per file type, contributed as language icons so they
  appear under the default Seti icon theme with no configuration.
