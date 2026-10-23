## Git workflow

All changes reach `main` through a pull request; GitHub rejects direct pushes to `main`.

1. Branch from `main` as `<type>/<topic>` (`feat/`, `fix/`, `docs/`, `chore/`).
2. Commit, push, and open a PR against `main` (`gh pr create`). Done when the PR is open on GitHub.
3. The owner merges the PR on GitHub. Afterwards `git checkout main && git pull` (or reset to `origin/main` after a squash merge). Done when local `main` matches `origin/main`.

## Agent skills

### Issue tracker

Issues are tracked in GitHub Issues (`XR0118/20261024`), via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Domain docs

Single-context: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
