# esuna

esuna is a local, cross-model code-review CLI that asks logged-in coding agents to find concrete defects in the current Git change, then scores finding confidence with TypeSafe. It is motivated by replacing a Greptile-style review workflow with a transparent, repository-local tool; v1 is not a hosted GitHub App or semantic-index service.

## Requirements and install

Build from the repository root:

```sh
go build -o esuna ./cmd/esuna
```

Runtime requires `TYPESAFE_API_KEY`, all four supported `claude`, `codex`, `muse`, and `grok` CLI binaries installed and logged in (the default pool runs all four), and Git. Universal Ctags must be on `PATH` when using `--deep`. Reviewer selection and order are configured per repository; the default pool contains all four and excludes the declared author model.

## Usage

Run from a Git repository with committed history (staged and unstaged changes are included; untracked files are not):

```sh
esuna                       # independent reviewers; merge findings (default)
esuna --gate                # fail for P0 findings meeting the configured confidence threshold
esuna --json                # stable machine-readable JSON result
esuna --chain --verify      # sequential reviewers; re-check findings with the first reviewer
esuna --deep                # add repository-wide symbols via Universal Ctags
esuna --with-tests          # ask reviewers to include tests and coverage
esuna --author-model=codex  # exclude the change's author CLI from review
esuna install-hook          # install an advisory Git pre-push hook
esuna install-hook --gate   # install a hook that propagates esuna failures
```

`--merge` runs reviewers independently and combines their findings; it is the default. `--chain` instead runs them sequentially in configured pool order and cannot be combined with `--merge`. `--verify` asks the first selected reviewer to re-check findings before scoring. Without `--gate`, a successful review is advisory; `--gate` returns nonzero when a P0 finding meets `gate_threshold`. Hook installation refuses to overwrite an existing pre-push hook; the hook requires `esuna` on `PATH`.

## Repository configuration

Copy [`.esuna.conf.example`](.esuna.conf.example) to `.esuna.conf` at the repository root. It uses simple `key=value` lines; blank lines and full-line `#` comments are allowed. Supported keys are `reviewer_pool` (ordered comma-separated CLI names), `gate_threshold` (finite number from 0 to 1), and `author_model` (optional model to exclude). Unknown keys, duplicate keys, and invalid values are errors. `--author-model` overrides the configured author, including an explicit empty value to clear it.

Copy [`.esuna/guidelines.md.example`](.esuna/guidelines.md.example) to `.esuna/guidelines.md` to provide repository-specific reviewer guidance. These files are optional.

## Confidence scoring

Jev/TypeSafe scores each reported finding's confidence as a probability from 0 to 1; the result's overall confidence is the arithmetic mean, or 1.0 when there are no findings. The declared author model is excluded from the reviewer pool to reduce self-review; reviewers may be selected and ordered in `.esuna.conf`. Scoring requires `TYPESAFE_API_KEY` and esuna does not skip scoring when it is unavailable.
