# manyhats 🎩

**Identity profiles for agents and shells.**

direnv scopes environments to *directories*; hats scopes them to *identities*.

Agents roam directories — but they must never roam identities. If you run coding
agents (Claude Code, Codex, your own orchestrator) across multiple clients,
employers, or businesses, the question isn't "which folder am I in," it's
**"whose credentials is this process holding?"** hats makes the answer structural:
every process is born wearing exactly one hat.

## Why not just prompt the agent?

Prompts are **opt-in correctness** — every command must remember the instruction,
and one compaction, subagent, or injection later it's gone. Environment injection
is **opt-out correctness**: the default path is the right account, wrong-identity
access requires a deliberate act, and subprocesses inherit it for free.

> Agents inherit identity from their execution context, never from their prompt.

## Install

```sh
go install github.com/chrismcdermut/hats@latest   # installs the `hats` binary
# or build from source:
git clone https://github.com/chrismcdermut/hats && cd hats && go build -o hats .
```

(Homebrew tap and an npm binary shim — `npm i -g manyhats` — planned once released.)

## Usage

```sh
hats ls                          # list profiles (* = active)
hats run acme -- vercel deploy   # one command under an identity
hats run dayjob -- claude         # an agent session, born scoped
hats shell acme                  # subshell wearing the hat
hats env acme                    # eval-able exports (direnv/scripts)
hats which                       # what hat is this process wearing?
hats doctor                      # are all identities aligned & logged in?
```

## Config

`~/.config/hats/profiles.json`:

```json
{
  "profiles": {
    "acme": {
      "description": "Acme Corp (client)",
      "env": {
        "CLAUDE_CONFIG_DIR": "~/.claude-acme",
        "GOOGLE_WORKSPACE_CLI_CONFIG_DIR": "~/.config/gws-acme",
        "VERCEL_CONFIG_DIR": "~/.config/vercel-acme"
      },
      "path_prepend": ["~/.local/bin"],
      "doctor": {
        "claude": "~/.claude-acme/.claude.json",
        "gws": "~/.config/gws-acme",
        "vercel": "~/.config/vercel-acme/auth.json"
      }
    }
  }
}
```

- **env** — variables that point each CLI at this identity's credential dir.
  Works natively for CLIs with config-dir env vars (gcloud `CLOUDSDK_CONFIG`,
  doppler `DOPPLER_CONFIG_DIR`, render `RENDER_CLI_CONFIG_PATH`, …). CLIs
  without one (vercel, neon) need a small wrapper shim that translates an env
  var into their `--config` flag — put shims in a `path_prepend` dir.
- **path_prepend** — dirs prepended to `PATH` (wrapper shims live here).
- **doctor** — label → path that must exist and be non-empty. `hats doctor` is
  your "is every identity logged into the right place" audit.

## The boundary ladder

hats is rung 1 of a 3-rung ladder:

1. **Mistake prevention** (hats): correct-by-default env injection.
2. **Misuse prevention**: harness-level policy — e.g. Claude Code PreToolUse
   hooks that block commands referencing other identities' credential paths.
3. **Compromise prevention**: OS-level isolation (separate users, containers).

Most failures are rung-1 failures. Climb only as your threat model demands.

## License

MIT
