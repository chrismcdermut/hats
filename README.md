# manyhats 🎩

**Identity profiles for agents and shells.**

direnv scopes environments to *directories*. hats scopes them to *identities*.

## What is a hat?

A hat is a named bundle of credentials: which Vercel account, which Google
Workspace, which cloud project, which agent config a process sees. Run any
command "wearing" a hat and it inherits that identity's environment, so every
CLI it touches reads the right credential directory automatically.

```sh
hats wear client -- vercel deploy   # deploys with your client's Vercel account
hats wear dayjob -- vercel deploy   # same command, employer's account
hats wear client -- claude          # an agent session, born scoped to the client
```

(`hats wear` is the flagship verb; `hats run` is a familiar alias - use whichever.)

Agents roam directories, but they must never roam identities. If you run coding
agents (Claude Code, Codex, your own orchestrator) across multiple clients,
employers, or businesses, the question isn't "which folder am I in," it's
"whose credentials is this process holding?" hats makes the answer structural:
every process is born wearing exactly one hat.

## Why not just prompt the agent?

Prompts are opt-in correctness: every command must remember the instruction,
and one context compaction, subagent, or injection later it's gone.
Environment injection is opt-out correctness: the default path is the right
account, wrong-identity access requires a deliberate act, and subprocesses
inherit it for free.

> Agents inherit identity from their execution context, never from their prompt.

## Install

```sh
brew install chrismcdermut/tap/hats
# or
go install github.com/chrismcdermut/hats@latest
# or (Node shim)
npm install -g manyhats
```

## Quickstart

Define your hats in `~/.config/hats/profiles.json`:

Notice the pattern: **same variables, different directory per identity.** That
`-<identity>` suffix is the whole idea.

```json
{
  "profiles": {
    "personal": {
      "description": "Personal projects",
      "env": {
        "CLAUDE_CONFIG_DIR": "~/.claude",
        "GOOGLE_WORKSPACE_CLI_CONFIG_DIR": "~/.config/gws-personal",
        "VERCEL_CONFIG_DIR": "~/.config/vercel-personal",
        "RENDER_CLI_CONFIG_PATH": "~/.config/render-personal"
      }
    },
    "dayjob": {
      "description": "Employer",
      "env": {
        "CLAUDE_CONFIG_DIR": "~/.claude-dayjob",
        "GOOGLE_WORKSPACE_CLI_CONFIG_DIR": "~/.config/gws-dayjob",
        "VERCEL_CONFIG_DIR": "~/.config/vercel-dayjob",
        "RENDER_CLI_CONFIG_PATH": "~/.config/render-dayjob"
      },
      "env_files": ["~/.env-dayjob"],
      "doctor": {
        "claude": "~/.claude-dayjob/.claude.json",
        "gws": "~/.config/gws-dayjob",
        "vercel": "~/.config/vercel-dayjob/auth.json"
      },
      "logins": { "gws": "gws auth login", "vercel": "vercel login" }
    },
    "client": {
      "description": "Client contract",
      "env": {
        "CLAUDE_CONFIG_DIR": "~/.claude-client",
        "GOOGLE_WORKSPACE_CLI_CONFIG_DIR": "~/.config/gws-client",
        "VERCEL_CONFIG_DIR": "~/.config/vercel-client",
        "RENDER_CLI_CONFIG_PATH": "~/.config/render-client"
      },
      "env_files": ["~/.env-client"]
    }
  }
}
```

Every identity scopes the *same* tools (Claude, Google Workspace, Vercel,
Render) - only the directory changes (`gws-personal` vs `gws-dayjob` vs
`gws-client`). So `hats wear dayjob -- gws ...` reads the employer's Google
Workspace; `hats wear client -- gws ...` reads the client's. Same command, same
tool, different identity, by construction.

Then:

```sh
hats ls                            # list hats (* = the one you're wearing)
hats wear client -- vercel deploy   # one command under an identity
hats shell client                  # a subshell wearing the hat
hats login client                  # log this hat's CLIs in (writes to its dirs)
hats which                         # what hat is this process wearing?
hats doctor                        # is every identity aligned and logged in?
hats env client                    # eval-able exports, for scripts
hats env client --json             # machine-readable, for orchestrators
```

## What `wear` actually does

No magic: `hats wear <profile> -- <cmd>` sets the profile's env vars, prepends
any `path_prepend` dirs to `PATH`, sets `HATS_PROFILE`, and then **execs** the
command (a true exec: hats replaces itself with your command, so signals, tty,
and exit codes flow naturally). Every subprocess inherits the hat.

- Most CLIs support this natively via a config-dir env var: gcloud
  (`CLOUDSDK_CONFIG`), doppler (`DOPPLER_CONFIG_DIR`), render
  (`RENDER_CLI_CONFIG_PATH`), Claude Code (`CLAUDE_CONFIG_DIR`), and more.
- CLIs without one (vercel, neon) need a small wrapper shim that translates an
  env var into their `--config` flag. Put shims in a `path_prepend` dir and
  hats will front-load them onto `PATH`.

Because each identity's credentials live in their own directory, isolation is
structural: a process wearing the client hat cannot accidentally deploy with
the dayjob account, because dayjob's tokens are simply not on its path.

## Logging in (populating a hat)

A hat's `env` only *points* at credential directories. You still have to put
credentials in them, and the golden rule is:

> **Always log in through the hat.** `hats wear <profile> -- <cli> login`
> writes the token to that profile's directory. A bare `<cli> login` writes to
> the CLI's default location and silently ends up under the wrong (or no)
> identity.

So to set up a new hat, log each CLI in wearing it:

```sh
hats wear client -- gws auth login        # -> ~/.config/gws-client
hats wear client -- vercel login          # -> ~/.config/vercel-client (needs shim, see below)
hats wear client -- render login          # -> ~/.config/render-client
hats wear client -- gcloud auth login     # -> ~/.config/gcloud-client
hats wear client -- neonctl auth          # -> ~/.config/neon-client (needs shim)
hats wear client -- doppler login         # -> ~/.config/doppler-client

hats doctor client                       # confirm each one landed
```

Each is a normal browser OAuth flow; the only thing hats changes is *where the
resulting token is saved*. Because the env var is set for that command, the CLI
reads and writes the right directory. Do this once per identity per machine
(tokens don't sync between machines, so you re-login on each, but the hat
definition travels).

### The vercel / neon shim caveat

CLIs with a native config-dir env var (gcloud, doppler, render, gws, Claude
Code) work out of the box. But **vercel and neon ignore env vars** and always
use a fixed default location, so `hats wear client -- vercel login` would still
clobber your default vercel login. The fix is a tiny wrapper on `PATH` that
translates an env var into their `--config` flag:

```sh
#!/bin/sh
# ~/.local/bin/vercel  (shim; real binary must be elsewhere on PATH)
if [ -n "$VERCEL_CONFIG_DIR" ]; then
  exec vercel-real --global-config "$VERCEL_CONFIG_DIR" "$@"
fi
exec vercel-real "$@"
```

Put the shim dir in the profile's `path_prepend`, and `VERCEL_CONFIG_DIR` in
its `env`. Then vercel logins land per-hat like everything else. (A future hats
release will generate these shims for you.)

## Launcher aliases (Claude Code, Codex, ...)

Hats carry all the identity; aliases are just muscle memory. Point short
launchers at `hats wear`:

A convention that scales: `cc` + an identity initial, plus `y` for yolo
(skip-permissions) sessions:

```sh
# Claude Code: cc<initial>, add y for autonomous mode
alias cc='hats wear personal -- claude'
alias ccy='hats wear personal -- claude --dangerously-skip-permissions'
alias ccd='hats wear dayjob -- claude'
alias ccdy='hats wear dayjob -- claude --dangerously-skip-permissions'
alias ccc='hats wear client -- claude'
alias cccy='hats wear client -- claude --dangerously-skip-permissions'

# Codex: same pattern
alias cx='hats wear personal -- codex'
alias cxdy='hats wear dayjob -- codex --full-auto'

# or ad hoc, no alias needed
hats wear client -- claude -p "summarize this repo"
```

The same trick works for any CLI, so you can reach a specific identity's
Vercel/Render/Google Workspace without wearing the whole hat. Name them
`<cli><identity>`:

```sh
# Google Workspace per identity (gws<initial>)
alias gwsp='hats wear personal -- gws'
alias gwsd='hats wear dayjob -- gws'
alias gwsc='hats wear client -- gws'

# Vercel / Render per identity
alias verp='hats wear personal -- vercel'
alias verc='hats wear client -- vercel'
alias rendd='hats wear dayjob -- render'
alias rendc='hats wear client -- render'
```

Now `gwsc drive files list` runs Google Workspace as the client, and
`rendd deploy` deploys Render as your employer, from any directory. Because
these delegate to `hats run`, the identity still lives only in profiles.json:
every alias is a thin pointer with zero credentials or paths baked in.

Adding identity #4 is a profiles.json edit. No alias surgery, no duplicated
env blocks, and the aliases contain zero identity information.

## Doctor

`hats doctor` audits every hat: does each credential path exist, and is it
non-empty (logged in)? It's the "is everything aligned" check you'd otherwise
do by hand after every laptop migration, reauth, or 2am login mishap.

```
dayjob  -  Employer
  ✓ claude   ~/.claude-dayjob/.claude.json
  ✓ gws      ~/.config/gws-dayjob
  ○ vercel   ~/.config/vercel-dayjob/auth.json  [empty]

1 check(s) not ok (○ empty = not yet logged in, ✗ missing = path absent)
```

## For orchestrators

`hats env <profile> --json` emits the resolved environment as JSON, so an
orchestrator can spawn each worker wearing the right hat:

```js
const { env } = JSON.parse(execSync("hats env client --json"));
spawn(agentCmd, { env: { ...process.env, ...env } });
```


## Secrets (identity-scoped)

Put a hat's tokens in a gitignored `env_files` secret file so they load **only**
when you wear that hat, instead of globally:

```json
"kanda": {
  "env": { "CLAUDE_CONFIG_DIR": "~/.claude-kanda" },
  "env_files": ["~/.env-kanda-secrets"]
}
```

```sh
# ~/.env-kanda-secrets  (chmod 600, never committed)
export JIRA_WORK_BASIC_AUTH="..."
```

Now `hats wear kanda -- ...` has the token; `hats wear personal` does not. Missing
files are skipped, so `profiles.json` stays portable (secrets are per-machine).

## Boundaries (`hats boundary`)

Env injection makes the *default* identity correct, but a determined command can
still reach another hat explicitly (`hats run other -- ...`, or another
identity's alias). For sessions where that must be blocked, `hats boundary`
emits the identity signals belonging to *other* hats, derived from
`profiles.json`, so a guard never hand-maintains a block list:

```sh
hats boundary dayjob --json
# { "profile": "dayjob", "reachable": [],
#   "foreign_profiles": ["client", "personal"],
#   "foreign_paths":   ["gws-client", "vercel-personal", ...],
#   "foreign_aliases": ["gwsc", "vercelp", ...] }
```

- **foreign_paths** are the config-dir basenames of other hats (shared values
  like a common browser are subtracted out automatically).
- **foreign_aliases** come from each profile's optional `aliases` field (the
  short launchers; hyphenated `<cli>-<identity>` aliases are already caught as
  path fragments).
- **reachable** lets a hat sanction specific crossings: list them and they move
  from foreign to allowed, so a combined session can touch, say, personal and
  client but nothing else.

A tiny PreToolUse hook (or any harness's equivalent) feeds a command through
this and blocks on a hit. Because the block set is computed from
`profiles.json`, adding or renaming a hat updates every guard for free. This is
mistake/casual-misuse prevention, not hard isolation (a heuristic is defeatable
by obfuscation) - it's the middle rung below.

## The boundary ladder

1. **Mistake prevention** (`hats wear`): correct-by-default env injection.
2. **Misuse prevention** (`hats boundary` + a guard hook): block explicit
   cross-identity reach, from a config-derived block list.
3. **Compromise prevention**: OS-level isolation (separate users, containers).

Most failures are rung-1 failures. Climb only as your threat model demands.

## License

MIT
