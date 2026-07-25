// hats — identity profiles for agents and shells.
// direnv scopes environments to directories; hats scopes them to identities.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// version is stamped at build time via -ldflags "-X main.version=..."
// (release builds get the tag; local builds report "dev").
var version = "dev"

type Profile struct {
	Description string `json:"description"`
	// Environment is the set of environment variables the hat injects: the process
	// environment it "wears" (config-dir pointers plus any other vars). Config-dir
	// vars here also seed `hats doctor`. `env` is accepted as a legacy alias.
	Environment map[string]string `json:"environment"`
	Env         map[string]string `json:"env"` // legacy alias for environment
	PathPrepend []string          `json:"path_prepend"`
	// Doctor refines the auto-derived credential checks. Base checks come from the
	// env config-dir vars (see doctorChecks); entries here override a derived path
	// by label (e.g. point "vercel" at auth.json instead of the dir) or add a check
	// with no env var (e.g. personal "claude" -> ~/.claude.json). Usually 1-2 lines.
	Doctor map[string]string `json:"doctor"`
	// Logins maps a short name (e.g. "gws") to the login command run under this
	// hat (e.g. "gws auth login"), so tokens land in this profile's config dir.
	Logins map[string]string `json:"logins"`
	// EnvFiles are gitignored KEY=VALUE files (secrets) sourced ONLY when wearing
	// this hat, so tokens stay identity-scoped instead of global. Loaded before
	// Env (so Env can override). Lines: `export KEY="val"`, `KEY=val`, `# comment`.
	EnvFiles []string `json:"env_files"`
	// Reachable lists other profiles this hat is allowed to reach explicitly
	// (e.g. a personal hat that may also touch nina). `hats boundary` treats
	// self + reachable as allowed and everything else as foreign, so a boundary
	// guard can permit sanctioned crossings while blocking the rest.
	Reachable []string `json:"reachable"`
	// Aliases are the launcher aliases that belong to this identity (gwsn, vern,
	// ccn, ...). Consumed by `hats boundary` to flag cross-identity aliases, and
	// available for future alias generation. Hyphenated <cli>-<identity> aliases
	// need not be listed: they equal config-dir basenames and are already caught
	// as path fragments.
	Aliases []string `json:"aliases"`
}

type Config struct {
	Profiles map[string]Profile `json:"profiles"`
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		die("cannot determine home directory: " + err.Error())
	}
	return h
}

func configPath() string {
	dir := os.Getenv("HATS_CONFIG")
	if dir == "" {
		dir = filepath.Join(home(), ".config", "hats")
	}
	return filepath.Join(dir, "profiles.json")
}

func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = home() + p[1:]
	}
	p = strings.ReplaceAll(p, "${HOME}", home())
	p = strings.ReplaceAll(p, "$HOME", home())
	return p
}

func loadConfig() Config {
	raw, err := os.ReadFile(configPath())
	if err != nil {
		die(fmt.Sprintf("no config at %s\nCreate it with a \"profiles\" object (see README).", configPath()))
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		die("config is not valid JSON: " + err.Error())
	}
	return cfg
}

func getProfile(cfg Config, name string) Profile {
	prof, ok := cfg.Profiles[name]
	if !ok {
		die(fmt.Sprintf("unknown profile '%s'. Available: %s", name, strings.Join(profileNames(cfg), ", ")))
	}
	return prof
}

func profileNames(cfg Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var envLineRe = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)

// loadEnvFile parses a simple KEY=VALUE / export KEY="VALUE" secrets file.
// Missing files are silently skipped (a secret file may not exist on every machine).
func loadEnvFile(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(expand(path))
	if err != nil {
		return out // absent/unreadable: skip quietly
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		m := envLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		val := strings.TrimSpace(m[2])
		// strip one layer of matching quotes
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		out[m[1]] = val
	}
	return out
}

// envMap returns the hat's declared environment variables, merging the legacy
// `env` field under the current `environment` field (environment wins on clash).
func (p Profile) envMap() map[string]string {
	m := map[string]string{}
	for k, v := range p.Env {
		m[k] = v
	}
	for k, v := range p.Environment {
		m[k] = v
	}
	return m
}

// resolveEnv returns the hat's extra environment: env_files first (secrets),
// then declared environment (which may override), all with ~ / $HOME expanded.
// PATH/HATS_PROFILE are handled separately by callers.
func resolveEnv(prof Profile) map[string]string {
	env := map[string]string{}
	for _, f := range prof.EnvFiles {
		for k, v := range loadEnvFile(f) {
			env[k] = v
		}
	}
	for k, v := range prof.envMap() {
		env[k] = expand(v)
	}
	return env
}

// applyEnv mutates this process's environment to wear the given hat,
// so a subsequent exec inherits it (and PATH lookups use the new PATH).
func applyEnv(name string, prof Profile) {
	for k, v := range resolveEnv(prof) {
		os.Setenv(k, v)
	}
	if len(prof.PathPrepend) > 0 {
		parts := make([]string, 0, len(prof.PathPrepend))
		for _, p := range prof.PathPrepend {
			parts = append(parts, expand(p))
		}
		os.Setenv("PATH", strings.Join(parts, ":")+":"+os.Getenv("PATH"))
	}
	os.Setenv("HATS_PROFILE", name)
}

func die(msg string) {
	fmt.Fprintf(os.Stderr, "hats: %s\n", msg)
	os.Exit(1)
}

func cmdLs(cfg Config) {
	names := profileNames(cfg)
	if len(names) == 0 {
		fmt.Println("(no profiles)")
		return
	}
	width := 0
	for _, n := range names {
		if len(n) > width {
			width = len(n)
		}
	}
	active := os.Getenv("HATS_PROFILE")
	for _, n := range names {
		marker := "  "
		if n == active {
			marker = "* "
		}
		fmt.Printf("%s%-*s  %s\n", marker, width, n, cfg.Profiles[n].Description)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func cmdEnv(cfg Config, name string, asJSON bool) {
	prof := getProfile(cfg, name)
	resolved := resolveEnv(prof) // env_files + Env, expanded
	// HATS_PROFILE is part of the hat's environment, so it belongs in both the
	// shell exports and the --json env map (orchestrators spawn workers from the
	// latter and need the identity marker for `hats which` / boundary hooks).
	resolved["HATS_PROFILE"] = name
	if asJSON {
		prepend := make([]string, 0, len(prof.PathPrepend))
		for _, p := range prof.PathPrepend {
			prepend = append(prepend, expand(p))
		}
		out, _ := json.MarshalIndent(map[string]any{
			"profile":      name,
			"env":          resolved,
			"path_prepend": prepend,
		}, "", "  ")
		fmt.Println(string(out))
		return
	}
	keys := make([]string, 0, len(resolved))
	for k := range resolved {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("export %s=%s\n", k, shellQuote(resolved[k]))
	}
	if len(prof.PathPrepend) > 0 {
		parts := make([]string, 0, len(prof.PathPrepend))
		for _, p := range prof.PathPrepend {
			parts = append(parts, expand(p))
		}
		fmt.Printf("export PATH=%s\":$PATH\"\n", shellQuote(strings.Join(parts, ":")))
	}
}

// execUnder replaces this process with the command, wearing the hat.
// True exec: signals, tty, and exit status flow naturally.
func execUnder(name string, prof Profile, argv []string) {
	applyEnv(name, prof)
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		die(fmt.Sprintf("command not found: %s", argv[0]))
	}
	if err := syscall.Exec(bin, argv, os.Environ()); err != nil {
		die("exec failed: " + err.Error())
	}
}

func cmdRun(cfg Config, name string, argv []string) {
	if len(argv) == 0 {
		die("nothing to run. Usage: hats run <profile> -- <command...>")
	}
	execUnder(name, getProfile(cfg, name), argv)
}

func cmdShell(cfg Config, name string) {
	prof := getProfile(cfg, name)
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	fmt.Fprintf(os.Stderr, "hats: entering '%s' shell (exit to leave)\n", name)
	execUnder(name, prof, []string{shell})
}

// cmdLogin runs a profile's declared login commands wearing the hat, so each
// CLI writes its token to this identity's config dir. Runs as subprocesses
// (sequential, interactive) rather than exec, since there may be several.
func cmdLogin(cfg Config, name, which string) {
	prof := getProfile(cfg, name)
	if len(prof.Logins) == 0 {
		die(fmt.Sprintf("profile '%s' declares no logins. Add a \"logins\" map to profiles.json, e.g. {\"gws\": \"gws auth login\"}.", name))
	}
	var targets []string
	if which != "" {
		if _, ok := prof.Logins[which]; !ok {
			keys := make([]string, 0, len(prof.Logins))
			for k := range prof.Logins {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			die(fmt.Sprintf("profile '%s' has no login '%s'. Available: %s", name, which, strings.Join(keys, ", ")))
		}
		targets = []string{which}
	} else {
		for k := range prof.Logins {
			targets = append(targets, k)
		}
		sort.Strings(targets)
	}

	// build the hat's environment once
	env := os.Environ()
	extra := resolveEnv(prof) // env_files + Env
	extra["HATS_PROFILE"] = name
	if len(prof.PathPrepend) > 0 {
		parts := make([]string, 0, len(prof.PathPrepend))
		for _, p := range prof.PathPrepend {
			parts = append(parts, expand(p))
		}
		extra["PATH"] = strings.Join(parts, ":") + ":" + os.Getenv("PATH")
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}

	failed := 0
	for _, t := range targets {
		cmdline := prof.Logins[t]
		fmt.Fprintf(os.Stderr, "\n\033[1mhats login %s: %s\033[0m  (%s)\n", name, t, cmdline)
		c := exec.Command("sh", "-c", cmdline)
		c.Env = env
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "\033[31m  ✗ %s login failed: %v\033[0m\n", t, err)
		}
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "\n%d login(s) failed. Re-run: hats login %s [name]\n", failed, name)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "\n\033[32mall logins for '%s' completed. Verify with: hats doctor %s\033[0m\n", name, name)
}

func cmdWhich() {
	p := os.Getenv("HATS_PROFILE")
	if p == "" {
		p = "(none)"
	}
	fmt.Println(p)
}

func checkPath(p string) string {
	full := expand(p)
	st, err := os.Stat(full)
	if err != nil {
		return "missing"
	}
	if st.IsDir() {
		entries, err := os.ReadDir(full)
		if err != nil || len(entries) == 0 {
			return "empty"
		}
		return "ok"
	}
	if st.Size() == 0 {
		return "empty"
	}
	return "ok"
}

// configDirLabels gives friendly doctor labels for well-known config-dir env
// vars whose name doesn't obviously map to the CLI (CLOUDSDK -> gcloud, etc.).
var configDirLabels = map[string]string{
	"CLAUDE_CONFIG_DIR":               "claude",
	"GOOGLE_WORKSPACE_CLI_CONFIG_DIR": "gws",
	"VERCEL_CONFIG_DIR":               "vercel",
	"RENDER_CLI_CONFIG_PATH":          "render",
	"CLOUDSDK_CONFIG":                 "gcloud",
	"NEON_CONFIG_DIR":                 "neon",
	"DOPPLER_CONFIG_DIR":              "doppler",
	"GH_CONFIG_DIR":                   "gh",
	"GLAB_CONFIG_DIR":                 "glab",
	"AWS_SHARED_CREDENTIALS_FILE":     "aws", // the keys file; its presence = logged in
}

// loginProofFile maps a config-dir env var to the file inside that dir whose
// presence (and non-emptiness) actually proves a login. A config dir can exist
// while empty/logged-out, so deriving a check on this file is stricter and truer
// than checking the dir itself. Overridable via a profile's `doctor` map.
var loginProofFile = map[string]string{
	"VERCEL_CONFIG_DIR": "auth.json",
	"CLAUDE_CONFIG_DIR": ".claude.json",
	"GH_CONFIG_DIR":     "hosts.yml", // gh writes authed hosts here; config.yml exists pre-login
}

// isConfigDirVar reports whether an env var names a CLI's config directory (so a
// doctor check can be derived from it). Avoids checking incidental path vars like
// BROWSER by requiring a config-dir naming convention (or a known name).
func isConfigDirVar(name string) bool {
	if _, ok := configDirLabels[name]; ok {
		return true
	}
	return strings.HasSuffix(name, "_CONFIG_DIR") ||
		strings.HasSuffix(name, "_CONFIG_PATH") ||
		strings.HasSuffix(name, "_CONFIG")
}

func doctorLabel(name string) string {
	if l, ok := configDirLabels[name]; ok {
		return l
	}
	l := name
	for _, suf := range []string{"_CLI_CONFIG_DIR", "_CONFIG_DIR", "_CONFIG_PATH", "_CONFIG", "_DIR"} {
		if strings.HasSuffix(l, suf) {
			l = strings.TrimSuffix(l, suf)
			break
		}
	}
	return strings.ToLower(strings.ReplaceAll(l, "_", "-"))
}

// doctorChecks builds the label->path checks for a profile: base checks derived
// from its env config-dir vars, then Doctor entries applied as overrides/additions
// (a specific login-proof file, or a check with no corresponding env var).
func doctorChecks(prof Profile) map[string]string {
	checks := map[string]string{}
	for name, val := range prof.envMap() {
		if isPathVal(val) && isConfigDirVar(name) {
			path := val
			if proof, ok := loginProofFile[name]; ok {
				path = filepath.Join(val, proof) // stricter: the login-proof file
			}
			checks[doctorLabel(name)] = path
		}
	}
	for label, path := range prof.Doctor {
		checks[label] = path
	}
	return checks
}

func cmdDoctor(cfg Config, name string) {
	targets := profileNames(cfg)
	if name != "" {
		getProfile(cfg, name) // validate
		targets = []string{name}
	}
	bad := 0
	for _, n := range targets {
		prof := cfg.Profiles[n]
		desc := ""
		if prof.Description != "" {
			desc = "  — " + prof.Description
		}
		fmt.Printf("\n%s%s\n", n, desc)
		checks := doctorChecks(prof)
		if len(checks) == 0 {
			fmt.Println("  (no credential dirs to check)")
			continue
		}
		labels := make([]string, 0, len(checks))
		width := 0
		for l := range checks {
			labels = append(labels, l)
			if len(l) > width {
				width = len(l)
			}
		}
		sort.Strings(labels)
		for _, label := range labels {
			p := checks[label]
			state := checkPath(p)
			icon := "✓"
			suffix := ""
			if state != "ok" {
				bad++
				suffix = "  [" + state + "]"
				if state == "empty" {
					icon = "○"
				} else {
					icon = "✗"
				}
			}
			fmt.Printf("  %s %-*s  %s%s\n", icon, width, label, expand(p), suffix)
		}
	}
	if bad == 0 {
		fmt.Println("\nall checks passed")
		os.Exit(0)
	}
	fmt.Printf("\n%d check(s) not ok (○ empty = not yet logged in, ✗ missing = path absent)\n", bad)
	os.Exit(1)
}

// isPathVal reports whether an env value looks like a filesystem path (so we can
// derive a distinctive directory basename from it). Non-path values like
// "Profile 9" or a project ID are ignored.
func isPathVal(v string) bool {
	return strings.HasPrefix(v, "~/") || strings.HasPrefix(v, "/") ||
		strings.HasPrefix(v, "$HOME") || strings.HasPrefix(v, "${HOME}")
}

// pathFragments returns the distinctive directory basenames of a profile's
// path-like env values, e.g. ~/.config/gws-nina -> "gws-nina", ~/.claude-nina
// -> ".claude-nina". Shared values (e.g. ~/.local/bin/claude-browser) also
// appear here but are subtracted out by the caller when they belong to self.
func pathFragments(prof Profile) map[string]bool {
	set := map[string]bool{}
	for _, v := range prof.envMap() {
		if !isPathVal(v) {
			continue
		}
		b := filepath.Base(expand(v))
		if b == "" || b == "/" || b == "." {
			continue
		}
		set[b] = true
	}
	return set
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// cmdBoundary emits, for a profile, the identity signals that belong to OTHER
// (non-reachable) profiles: their config-dir path fragments, their launcher
// aliases, and their profile names. A PreToolUse guard consumes this JSON so the
// block set is derived from profiles.json rather than hand-maintained.
func cmdBoundary(cfg Config, name string, asJSON bool) {
	self := getProfile(cfg, name)
	allowed := map[string]bool{name: true}
	for _, r := range self.Reachable {
		allowed[r] = true
	}

	// Path fragments and aliases claimed by self + reachable are NOT foreign
	// (this drops shared values like claude-browser, and sanctioned crossings).
	allowedFrags := map[string]bool{}
	allowedAliases := map[string]bool{}
	for pn, prof := range cfg.Profiles {
		if !allowed[pn] {
			continue
		}
		for f := range pathFragments(prof) {
			allowedFrags[f] = true
		}
		for _, a := range prof.Aliases {
			allowedAliases[a] = true
		}
	}

	foreignProfiles := map[string]bool{}
	foreignPaths := map[string]bool{}
	foreignAliases := map[string]bool{}
	for pn, prof := range cfg.Profiles {
		if allowed[pn] {
			continue
		}
		foreignProfiles[pn] = true
		for f := range pathFragments(prof) {
			if !allowedFrags[f] {
				foreignPaths[f] = true
			}
		}
		for _, a := range prof.Aliases {
			if !allowedAliases[a] {
				foreignAliases[a] = true
			}
		}
	}

	reach := append([]string{}, self.Reachable...)
	sort.Strings(reach)
	if asJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"profile":          name,
			"reachable":        reach,
			"foreign_profiles": sortedKeys(foreignProfiles),
			"foreign_paths":    sortedKeys(foreignPaths),
			"foreign_aliases":  sortedKeys(foreignAliases),
		}, "", "  ")
		fmt.Println(string(out))
		return
	}
	fmt.Printf("boundary for '%s' (reachable: %s)\n", name, strings.Join(reach, ", "))
	fmt.Printf("  foreign profiles: %s\n", strings.Join(sortedKeys(foreignProfiles), " "))
	fmt.Printf("  foreign paths:    %s\n", strings.Join(sortedKeys(foreignPaths), " "))
	fmt.Printf("  foreign aliases:  %s\n", strings.Join(sortedKeys(foreignAliases), " "))
}

// shimSpec describes a CLI that ignores a config-dir env var and needs a wrapper
// translating that env var into a flag. `hats init` generates these.
type shimSpec struct {
	Bin  string
	Env  string
	Flag string
}

var knownShims = []shimSpec{
	{Bin: "vercel", Env: "VERCEL_CONFIG_DIR", Flag: "--global-config"},
	{Bin: "neonctl", Env: "NEON_CONFIG_DIR", Flag: "--config-dir"},
}

func shimScript(s shimSpec) string {
	return fmt.Sprintf(`#!/bin/sh
# Generated by `+"`hats init`"+`. Honors $%[2]s for per-identity isolation
# (%[1]s has no config-dir env var, only the %[3]s flag). This shim must sit on
# PATH *ahead* of the real %[1]s, so put its dir in a profile's path_prepend.
self=$0
selfdir=$(CDPATH= cd -- "$(dirname -- "$self")" && pwd)
real=
IFS=:
for d in $PATH; do
  [ "$d" = "$selfdir" ] && continue
  if [ -x "$d/%[1]s" ]; then real=$d/%[1]s; break; fi
done
unset IFS
if [ -z "$real" ]; then echo "hats shim: real '%[1]s' not found on PATH" >&2; exit 127; fi
if [ -n "$%[2]s" ]; then exec "$real" %[3]s "$%[2]s" "$@"; fi
exec "$real" "$@"
`, s.Bin, s.Env, s.Flag)
}

const starterConfig = `{
  "profiles": {
    "personal": {
      "description": "Personal projects",
      "environment": {
        "CLAUDE_CONFIG_DIR": "~/.claude",
        "VERCEL_CONFIG_DIR": "~/.config/vercel-personal"
      }
    },
    "client": {
      "description": "Client contract",
      "environment": {
        "CLAUDE_CONFIG_DIR": "~/.claude-client",
        "VERCEL_CONFIG_DIR": "~/.config/vercel-client"
      },
      "path_prepend": ["~/.local/bin"]
    }
  }
}
`

// cmdInit scaffolds a starter profiles.json (if absent) and generates the known
// wrapper shims into a bin dir, so users don't hand-write them. Existing files
// are left alone unless --force.
func cmdInit(args []string) {
	dir := filepath.Join(home(), ".local", "bin")
	force := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--force", "-f":
			force = true
		case "--dir":
			if i+1 < len(args) {
				dir = expand(args[i+1])
				i++
			}
		}
	}

	cfgp := configPath()
	if _, err := os.Stat(cfgp); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(cfgp), 0o755); err != nil {
			die("cannot create config dir: " + err.Error())
		}
		if err := os.WriteFile(cfgp, []byte(starterConfig), 0o644); err != nil {
			die("cannot write config: " + err.Error())
		}
		fmt.Printf("created starter config: %s  (edit it, then `hats ls`)\n", cfgp)
	} else {
		fmt.Printf("config exists: %s  (left as-is)\n", cfgp)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		die("cannot create shim dir: " + err.Error())
	}
	wrote := 0
	for _, s := range knownShims {
		p := filepath.Join(dir, s.Bin)
		if _, err := os.Lstat(p); err == nil && !force {
			fmt.Printf("  skip  %s  (exists; --force to overwrite)\n", p)
			continue
		}
		if err := os.WriteFile(p, []byte(shimScript(s)), 0o755); err != nil {
			fmt.Printf("  error %s: %v\n", p, err)
			continue
		}
		fmt.Printf("  wrote %s  (%s -> %s)\n", p, s.Env, s.Flag)
		wrote++
	}
	fmt.Printf("\n%d shim(s) written to %s\n", wrote, dir)
	fmt.Printf("Make sure %s is on PATH ahead of the real CLIs (add it to a profile's path_prepend).\n", dir)
}

func usage() {
	fmt.Printf(`hats — identity profiles for agents and shells

usage:
  hats ls                        list profiles (* = active)
  hats env <profile> [--json]    print eval-able exports (or JSON for tooling)
  hats wear <profile> -- <cmd>   run command under an identity (alias: run)
  hats shell <profile>           subshell under an identity
  hats login <profile> [name]    log declared CLIs in, wearing the hat
  hats which                     active profile
  hats doctor [profile]          check credential dirs
  hats boundary <profile> [--json]  foreign identity signals (for a guard hook)
  hats init [--dir D] [--force]  scaffold config + generate wrapper shims

config: %s
       (override the directory with the HATS_CONFIG env var)
`, configPath())
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage()
		return
	}
	cmd := args[0]
	if cmd == "version" || cmd == "--version" || cmd == "-v" {
		fmt.Println("hats " + version)
		return
	}
	if cmd == "which" {
		cmdWhich()
		return
	}
	if cmd == "init" {
		cmdInit(args[1:])
		return
	}
	cfg := loadConfig()
	switch cmd {
	case "ls", "list":
		cmdLs(cfg)
	case "env":
		rest := args[1:]
		asJSON := false
		name := ""
		for _, a := range rest {
			if a == "--json" {
				asJSON = true
			} else if name == "" {
				name = a
			}
		}
		if name == "" {
			die("usage: hats env <profile> [--json]")
		}
		cmdEnv(cfg, name, asJSON)
	case "wear", "run": // 'wear' is the flagship spelling; 'run' is a familiar alias
		if len(args) < 2 {
			die("usage: hats wear <profile> -- <command...>")
		}
		name := args[1]
		argv := args[2:]
		if len(argv) > 0 && argv[0] == "--" {
			argv = argv[1:]
		}
		cmdRun(cfg, name, argv)
	case "shell":
		if len(args) < 2 {
			die("usage: hats shell <profile>")
		}
		cmdShell(cfg, args[1])
	case "login":
		if len(args) < 2 {
			die("usage: hats login <profile> [name]   (logs each declared CLI in, wearing the hat)")
		}
		which := ""
		if len(args) > 2 {
			which = args[2]
		}
		cmdLogin(cfg, args[1], which)
	case "doctor":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		cmdDoctor(cfg, name)
	case "boundary":
		rest := args[1:]
		asJSON := false
		name := ""
		for _, a := range rest {
			if a == "--json" {
				asJSON = true
			} else if name == "" {
				name = a
			}
		}
		if name == "" {
			die("usage: hats boundary <profile> [--json]")
		}
		cmdBoundary(cfg, name, asJSON)
	default:
		die(fmt.Sprintf("unknown command '%s' (try: hats help)", cmd))
	}
}
