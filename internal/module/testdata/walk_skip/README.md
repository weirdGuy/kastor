# walk_skip

Fixture for module.Files: the gen/ directory is a codegen target output and
must be skipped, as must dot-directories. Non-ADL files like this one are
still listed; callers filter by extension.
