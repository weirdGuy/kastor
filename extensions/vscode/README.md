# Kastor for VS Code

Syntax highlighting and file icons for [Kastor](https://github.com/getkastordev/kastor)
agent specs.

Kastor is a declarative HCL language for defining AI agents, tools, prompts, and
models — compiled to framework code with `kastor build`, or reconciled against
hosted platforms with `kastor plan` / `kastor apply`.

This extension is presentation only. It adds no commands, no settings, and no
language server. Install it and open a Kastor file.

## File types

| Extension | Language | Contents |
| --- | --- | --- |
| `.agent` | Kastor Agent | model, prompt, tools, inputs, outputs, dependencies |
| `.tool` | Kastor Tool | tool interface plus implementation source |
| `.prompt` | Kastor Prompt | HCL frontmatter plus a `{{variable}}` template body |
| `.kastor`, `kastor.hcl` | Kastor Project | models, targets, defaults |

## What gets highlighted

Block types, labels, attributes, strings, numbers, booleans, and comments, using
the same TextMate scopes HashiCorp's own grammar uses — so Kastor picks up your
theme's Terraform colors without any configuration.

On top of that, three Kastor-specific rules:

**References** — `model.fast`, `prompt.weather_system`,
`agent.forecast.output.summary`. The root is scoped distinctly from the members,
because references are what build Kastor's dependency graph.

**Closed enums** — `type = string | number | bool`,
`kind = "mcp" | "http" | "builtin" | "runtime" | "script"`, and
`type = "codegen" | "platform"`. A value outside its enum stays an ordinary
string, so a typo is visible before you run `kastor validate`.

These rules are anchored to their attribute name, so ordinary prose is never
mistaken for syntax — `description = "returns a string"` stays a string.

**Prompt variables** — `{{location}}` in a `.prompt` body, matched with exactly
the regex the Kastor parser uses. Anything that regex rejects is literal body
text and stays uncolored, which is the language's actual rule: `{{1bad}}` and
`{{not-a-var}}` are prose, not broken variables. A `---` line in the body is
prose too — only a `---` on the very first line of the file opens frontmatter.

### Not highlighted, on purpose

`${...}` interpolation, heredocs, function calls, `for` expressions, and
ternaries are all valid HCL that Kastor rejects. Coloring them would suggest they
work. If the language gains them, the grammar follows.

## File icons

The extension contributes a distinct icon per file type through VS Code's
language-icon contribution point. These appear under **Seti**, the default file
icon theme, with no configuration.

VS Code provides no way to add icons to a third-party icon theme, so if you use
Material Icon Theme, vscode-icons, or similar, Kastor files keep that theme's
generic file icon. That is a limitation of those themes, not something this
extension can work around.

## Development

From `extensions/vscode/`:

```sh
npm install
npm test          # grammar assertions + prompt snapshot
npm run package   # produces kastor-<version>.vsix
```

To try it in a live editor, open this folder in VS Code and press <kbd>F5</kbd>.
That launches an Extension Development Host with the extension loaded; open
`examples/weather/` in it to see every file type at once.

### Tests

`tests/*.test.*` are assertion tests: caret lines under each token naming the
scope it must have. `tests/snap/` holds a snapshot test, used for `.prompt`
because frontmatter is anchored to byte 0 of the file and an assertion test's
header lines would displace it.

After an intentional grammar change, refresh the snapshot with
`npm run test:update` and read the diff before committing it.

### Publishing

Requires an Azure DevOps personal access token for the `getkastor` publisher:

```sh
npx vsce login getkastor
npm run package
npx vsce publish
```

## License

Apache-2.0, same as Kastor itself.
