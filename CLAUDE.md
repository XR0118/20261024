## Git workflow

All changes reach `main` through a pull request; GitHub rejects direct pushes to `main`.

1. Branch from `main` as `<type>/<topic>` (`feat/`, `fix/`, `docs/`, `chore/`).
2. Commit, push, and open a PR against `main` (`gh pr create`). Done when the PR is open on GitHub.
3. The owner merges the PR on GitHub. Afterwards `git checkout main && git pull` (or reset to `origin/main` after a squash merge). Done when local `main` matches `origin/main`.

### Branch and PR discipline (prevents orphaned commits)

A push to an already-merged branch is invisible to the PR flow — the commits
linger on the branch and get lost (this happened once; do not repeat it).

- **Always start from a fresh `main`**: `git checkout main && git pull` before
  creating any branch. Never branch off a stale local branch.
- **Before pushing a follow-up commit to a branch, verify the PR is still
  open**: `gh pr view <num> --json state -q .state`. If `MERGED` or `CLOSED`,
  rebase/cherry-pick onto fresh `main` and open a new PR instead.
- **Delete remote branches after merge** (`gh api -X DELETE repos/XR0118/20261024/git/refs/heads/<branch>`), so dead branches can't be pushed to.
- **Claim "appended to PR #N" only after verifying**: `gh pr view <num> --json commits` actually lists the new commit.
- **Develop in a git worktree per branch** (isolated directory, e.g. via the
  EnterWorktree tool), and always create the worktree from `origin/main`
  (`git fetch` first, base `origin/main`, never stale local `main` or another
  feature branch). One worktree = one branch = one PR; switch back to the main
  checkout only for repo-wide tasks.

## Agent skills

### Issue tracker

Issues are tracked in GitHub Issues (`XR0118/20261024`), via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Domain docs

Single-context: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
